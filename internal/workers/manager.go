package workers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"watcher/internal/model"
	"watcher/internal/store"
)

type Manager struct {
	repoRoot string
	shell    model.ShellStatus
	store    *store.LocalStore
	publish  func(context.Context, model.EventEnvelope)

	mu          sync.RWMutex
	runtimes    map[string]*workerRuntime
	diagnostics map[string]model.ComponentRuntimeDiagnostics
}

type workerRuntime struct {
	manifest model.ComponentManifest
	sendCh   chan []byte

	mu                sync.RWMutex
	inFlight          map[string]struct{}
	processCancel     context.CancelFunc
	processGeneration int
	restartRequested  bool
}

func NewManager(
	repoRoot string,
	shell model.ShellStatus,
	statuses []model.ComponentStatus,
	localStore *store.LocalStore,
	publish func(context.Context, model.EventEnvelope),
) *Manager {
	manager := &Manager{
		repoRoot:    repoRoot,
		shell:       shell,
		store:       localStore,
		publish:     publish,
		runtimes:    make(map[string]*workerRuntime),
		diagnostics: make(map[string]model.ComponentRuntimeDiagnostics),
	}
	for _, status := range statuses {
		if !status.RuntimeEnabled {
			runtimeStatus := status.RuntimeStatus
			if runtimeStatus == "" {
				runtimeStatus = model.RuntimeStatusStopped
			}
			manager.diagnostics[status.Manifest.ID] = model.ComponentRuntimeDiagnostics{
				Enabled:   false,
				Status:    runtimeStatus,
				LastError: status.ValidationError,
			}
			continue
		}
		if status.Manifest.RuntimeShape != model.RuntimeShapeWorker {
			manager.diagnostics[status.Manifest.ID] = model.ComponentRuntimeDiagnostics{
				Enabled:   status.RuntimeEnabled,
				Status:    model.RuntimeStatusReady,
				LastError: "",
			}
			continue
		}
		manager.runtimes[status.Manifest.ID] = &workerRuntime{
			manifest: status.Manifest,
			sendCh:   make(chan []byte, 64),
			inFlight: make(map[string]struct{}),
		}
		manager.diagnostics[status.Manifest.ID] = model.ComponentRuntimeDiagnostics{
			Enabled:   true,
			Status:    model.RuntimeStatusStopped,
			LastError: "",
		}
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	for componentID, runtime := range m.runtimes {
		go m.runWorkerLoop(ctx, componentID, runtime)
	}
}

func (m *Manager) RuntimeDiagnostics() map[string]model.ComponentRuntimeDiagnostics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]model.ComponentRuntimeDiagnostics, len(m.diagnostics))
	for componentID, diagnostic := range m.diagnostics {
		if runtime, ok := m.runtimes[componentID]; ok {
			runtime.mu.RLock()
			diagnostic.InflightOperations = len(runtime.inFlight)
			runtime.mu.RUnlock()
		}
		out[componentID] = diagnostic
	}
	return out
}

func (m *Manager) Restart(componentID string) error {
	m.mu.RLock()
	runtime, ok := m.runtimes[componentID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("worker component %s is not registered", componentID)
	}
	runtime.mu.Lock()
	runtime.restartRequested = true
	cancel := runtime.processCancel
	generation := runtime.processGeneration
	runtime.mu.Unlock()

	m.recordDiagnostic(componentID, "worker.restarted", "warn", "manual worker restart requested", map[string]any{
		"component_id": componentID,
	})
	m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
		diag.RestartCount++
	})
	if cancel == nil {
		m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
			diag.Status = model.RuntimeStatusStarting
		})
		return nil
	}

	shutdownMessage, err := marshalEnvelope(messageShutdown, map[string]any{
		"requested_at": model.NowString(),
	})
	if err == nil {
		select {
		case runtime.sendCh <- shutdownMessage:
		default:
		}
	}

	go func(expectedGeneration int, cancel context.CancelFunc) {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		<-timer.C
		runtime.mu.RLock()
		sameGeneration := runtime.processGeneration == expectedGeneration
		runtime.mu.RUnlock()
		if sameGeneration {
			cancel()
		}
	}(generation, cancel)

	return nil
}

func (m *Manager) StartOperation(componentID string, operation model.ComponentOperation) error {
	m.mu.RLock()
	runtime, ok := m.runtimes[componentID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("worker component %s is not registered", componentID)
	}
	payload, err := marshalEnvelope(messageOperationStart, operationStartPayload{Operation: operation})
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.inFlight[operation.OperationID] = struct{}{}
	runtime.mu.Unlock()
	runtime.sendCh <- payload
	return nil
}

func (m *Manager) CancelOperation(componentID, operationID string) error {
	m.mu.RLock()
	runtime, ok := m.runtimes[componentID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("worker component %s is not registered", componentID)
	}
	runtime.mu.RLock()
	_, inFlight := runtime.inFlight[operationID]
	runtime.mu.RUnlock()
	if !inFlight {
		return fmt.Errorf("operation %s is not in flight for component %s", operationID, componentID)
	}
	payload, err := marshalEnvelope(messageOperationCancel, operationCancelPayload{OperationID: operationID})
	if err != nil {
		return err
	}
	runtime.sendCh <- payload
	return nil
}

func (m *Manager) runWorkerLoop(ctx context.Context, componentID string, runtime *workerRuntime) {
	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
				diag.Enabled = true
				diag.Status = model.RuntimeStatusStopped
			})
			return
		}

		if err := m.runWorkerProcess(ctx, componentID, runtime); err != nil {
			if ctx.Err() != nil {
				m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
					diag.Enabled = true
					diag.Status = model.RuntimeStatusStopped
				})
				return
			}
			restartRequested := runtime.takeRestartRequested()
			reason := err.Error()
			if restartRequested {
				reason = "manual restart requested"
				backoff = 2 * time.Second
			}
			m.markInflightInterrupted(componentID, runtime, reason)
			m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
				diag.Enabled = true
				diag.Status = model.RuntimeStatusBackoff
				diag.LastError = reason
			})
			if restartRequested {
				log.Printf("worker %s restarting", componentID)
			} else {
				m.recordDiagnostic(componentID, "worker.crashed", "error", reason, map[string]any{
					"component_id": componentID,
					"error":        reason,
				})
				m.recordDiagnostic(componentID, "worker.backoff", "warn", "worker entered backoff before restart", map[string]any{
					"component_id": componentID,
					"delay_ms":     backoff.Milliseconds(),
				})
				log.Printf("worker %s exited: %v (backoff %s)", componentID, err, backoff)
				backoff = backoff * 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (m *Manager) runWorkerProcess(ctx context.Context, componentID string, runtime *workerRuntime) error {
	worker := runtime.manifest.Worker
	if worker == nil {
		return fmt.Errorf("component %s has no worker config", componentID)
	}
	entrypoint := strings.TrimSpace(worker.Entrypoint)
	if entrypoint == "" {
		return fmt.Errorf("component %s worker entrypoint is empty", componentID)
	}

	args := make([]string, 0, len(worker.Args))
	for _, arg := range worker.Args {
		args = append(args, resolveWorkerPath(m.repoRoot, arg))
	}

	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(processCtx, entrypoint, args...)
	cmd.Dir = m.repoRoot
	cmd.Env = os.Environ()
	for key, value := range worker.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	runtime.mu.Lock()
	runtime.processCancel = cancel
	runtime.processGeneration++
	processGeneration := runtime.processGeneration
	runtime.restartRequested = false
	runtime.mu.Unlock()
	m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
		diag.Enabled = true
		diag.Status = model.RuntimeStatusStarting
		diag.WorkerPID = cmd.Process.Pid
		diag.LastStartAt = model.NowString()
	})
	m.recordDiagnostic(componentID, "worker.started", "info", "worker process started", map[string]any{
		"component_id": componentID,
		"pid":          cmd.Process.Pid,
	})

	defer func() {
		runtime.mu.Lock()
		if runtime.processGeneration == processGeneration {
			runtime.processCancel = nil
		}
		runtime.mu.Unlock()
	}()
	errCh := make(chan error, 4)
	go m.writeLoop(processCtx, runtime.sendCh, stdin, errCh)
	go m.stdoutLoop(processCtx, componentID, runtime, stdout, errCh)
	go m.stderrLoop(componentID, stderr)

	initMessage, err := marshalEnvelope(messageSpawnInit, spawnInitPayload{
		ShellID:          m.shell.Manifest.ID,
		ShellVersion:     m.shell.Version,
		ShellContract:    m.shell.Manifest.ContractVersion,
		ComponentID:      runtime.manifest.ID,
		ComponentName:    runtime.manifest.Name,
		RepoRoot:         m.repoRoot,
		RuntimeOwner:     runtime.manifest.RuntimeOwner,
		WorkerStreams:    append([]string(nil), worker.Streams...),
		WorkerOperations: append([]string(nil), worker.Operations...),
	})
	if err != nil {
		return err
	}
	runtime.sendCh <- initMessage

	pingMessage, err := marshalEnvelope(messageHealthPing, healthPingPayload{Timestamp: model.NowString()})
	if err != nil {
		return err
	}
	runtime.sendCh <- pingMessage

	healthTicker := time.NewTicker(15 * time.Second)
	defer healthTicker.Stop()
	go func() {
		for {
			select {
			case <-processCtx.Done():
				return
			case <-healthTicker.C:
				message, err := marshalEnvelope(messageHealthPing, healthPingPayload{Timestamp: model.NowString()})
				if err != nil {
					errCh <- err
					return
				}
				runtime.sendCh <- message
			}
		}
	}()

	waitErr := cmd.Wait()
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			m.captureExit(componentID, waitErr, err.Error(), true)
			return err
		}
	default:
	}
	if waitErr != nil {
		m.captureExit(componentID, waitErr, waitErr.Error(), true)
		return waitErr
	}
	m.captureExit(componentID, nil, "worker stopped unexpectedly", false)
	return fmt.Errorf("worker %s stopped unexpectedly", componentID)
}

func (m *Manager) writeLoop(ctx context.Context, sendCh <-chan []byte, stdin io.WriteCloser, errCh chan<- error) {
	writer := bufio.NewWriter(stdin)
	defer stdin.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-sendCh:
			if len(payload) == 0 {
				continue
			}
			if _, err := writer.Write(payload); err != nil {
				errCh <- err
				return
			}
			if err := writer.WriteByte('\n'); err != nil {
				errCh <- err
				return
			}
			if err := writer.Flush(); err != nil {
				errCh <- err
				return
			}
		}
	}
}

func (m *Manager) stdoutLoop(ctx context.Context, componentID string, runtime *workerRuntime, stdout io.Reader, errCh chan<- error) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg envelope
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			errCh <- fmt.Errorf("worker %s emitted invalid JSON: %w", componentID, err)
			return
		}
		if err := m.handleWorkerMessage(ctx, componentID, runtime, msg); err != nil {
			errCh <- err
			return
		}
	}
	if err := scanner.Err(); err != nil {
		errCh <- err
	}
}

func (m *Manager) stderrLoop(componentID string, stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("worker %s stderr: %s", componentID, scanner.Text())
	}
}

func (m *Manager) handleWorkerMessage(ctx context.Context, componentID string, runtime *workerRuntime, msg envelope) error {
	switch msg.Type {
	case messageHealthOK:
		var payload healthOKPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		status := model.RuntimeStatusRunning
		if !payload.Ready {
			status = model.RuntimeStatusStarting
		}
		m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
			diag.Enabled = true
			diag.Status = status
			diag.LastHeartbeatAt = payload.Timestamp
			diag.LastError = ""
		})
		return nil
	case messageLogLine:
		var payload logLinePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		log.Printf("worker %s %s: %s", componentID, payload.Level, payload.Message)
		return nil
	case messageEventPublish:
		var payload eventPublishPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		if payload.Envelope.EventID == "" {
			payload.Envelope.EventID = model.NewID("evt")
		}
		if payload.Envelope.OccurredAt == "" {
			payload.Envelope.OccurredAt = model.NowString()
		}
		if m.publish != nil {
			m.publish(ctx, payload.Envelope)
		}
		return nil
	case messageOperationUpdate:
		var payload operationUpdatePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		return m.handleOperationUpdate(componentID, runtime, payload)
	case messageShutdownReady:
		m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
			diag.Enabled = true
			diag.Status = model.RuntimeStatusStopped
		})
		return nil
	default:
		return fmt.Errorf("worker %s sent unsupported message type %q", componentID, msg.Type)
	}
}

func (m *Manager) handleOperationUpdate(componentID string, runtime *workerRuntime, payload operationUpdatePayload) error {
	operation, err := m.store.GetComponentOperation(payload.OperationID)
	if err != nil {
		return err
	}
	operation.Status = payload.Status
	if payload.ResourceID != "" {
		operation.ResourceID = payload.ResourceID
	}
	if len(payload.Result) > 0 {
		operation.Result = payload.Result
	}
	if payload.Error != "" {
		operation.LastError = payload.Error
	}
	now := model.NowString()
	switch payload.Status {
	case model.OperationStatusRunningOp:
		if operation.StartedAt == "" {
			operation.StartedAt = now
		}
	case model.OperationStatusCompleted, model.OperationStatusFailed, model.OperationStatusInterrupted:
		if operation.StartedAt == "" {
			operation.StartedAt = now
		}
		operation.CompletedAt = now
		runtime.mu.Lock()
		delete(runtime.inFlight, payload.OperationID)
		runtime.mu.Unlock()
	}
	_, err = m.store.SaveComponentOperation(operation)
	return err
}

func (m *Manager) markInflightInterrupted(componentID string, runtime *workerRuntime, errText string) {
	runtime.mu.RLock()
	inflight := make([]string, 0, len(runtime.inFlight))
	for operationID := range runtime.inFlight {
		inflight = append(inflight, operationID)
	}
	runtime.mu.RUnlock()
	for _, operationID := range inflight {
		operation, err := m.store.GetComponentOperation(operationID)
		if err != nil {
			continue
		}
		if operation.Status == model.OperationStatusCompleted || operation.Status == model.OperationStatusFailed || operation.Status == model.OperationStatusInterrupted {
			continue
		}
		operation.Status = model.OperationStatusInterrupted
		operation.LastError = errText
		if operation.StartedAt == "" {
			operation.StartedAt = model.NowString()
		}
		operation.CompletedAt = model.NowString()
		if _, err := m.store.SaveComponentOperation(operation); err != nil {
			log.Printf("save interrupted component operation %s: %v", operationID, err)
		}
		if m.publish != nil {
			m.publish(context.Background(), model.EventEnvelope{
				EventID:     model.NewID("evt"),
				Stream:      componentDefaultStream(runtime.manifest),
				Kind:        "worker.interrupted",
				ResourceID:  operation.ResourceID,
				OperationID: operation.OperationID,
				OccurredAt:  model.NowString(),
				Payload: mustJSON(map[string]any{
					"component_id": componentID,
					"operation":    operation,
					"error":        errText,
				}),
			})
		}
	}
	runtime.mu.Lock()
	runtime.inFlight = make(map[string]struct{})
	runtime.mu.Unlock()
}

func (m *Manager) mutateDiagnostic(componentID string, mutate func(*model.ComponentRuntimeDiagnostics)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	diagnostic := m.diagnostics[componentID]
	mutate(&diagnostic)
	m.diagnostics[componentID] = diagnostic
}

func (m *Manager) recordDiagnostic(componentID, kind, severity, message string, payload map[string]any) {
	if m == nil || m.store == nil {
		return
	}
	rawPayload := mustJSON(payload)
	if _, err := m.store.SaveShellDiagnostic(model.ShellDiagnosticEvent{
		ComponentID: componentID,
		Kind:        kind,
		Severity:    severity,
		Message:     message,
		OccurredAt:  model.NowString(),
		Payload:     rawPayload,
	}); err != nil {
		log.Printf("save shell diagnostic kind=%s component=%s: %v", kind, componentID, err)
	}
}

func (m *Manager) captureExit(componentID string, waitErr error, reason string, incrementRestart bool) {
	exitCode := 0
	if waitErr != nil {
		if exitCoder, ok := waitErr.(interface{ ExitCode() int }); ok {
			exitCode = exitCoder.ExitCode()
		} else {
			exitCode = -1
		}
	}
	m.mutateDiagnostic(componentID, func(diag *model.ComponentRuntimeDiagnostics) {
		diag.Enabled = true
		diag.WorkerPID = 0
		diag.LastExitCode = exitCode
		diag.LastExitReason = reason
		diag.LastError = reason
		if incrementRestart {
			diag.RestartCount++
		}
	})
}

func (runtime *workerRuntime) takeRestartRequested() bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	requested := runtime.restartRequested
	runtime.restartRequested = false
	return requested
}

func componentDefaultStream(manifest model.ComponentManifest) string {
	if manifest.Worker != nil && len(manifest.Worker.Streams) > 0 {
		return manifest.Worker.Streams[0]
	}
	if len(manifest.Streams) > 0 {
		return manifest.Streams[0]
	}
	return model.EventStreamSystemRelease
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func resolveWorkerPath(repoRoot, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "/") {
		return value
	}
	if strings.Contains(value, string(filepath.Separator)) {
		return filepath.Join(repoRoot, value)
	}
	return value
}
