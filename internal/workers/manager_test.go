package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"watcher/internal/model"
	"watcher/internal/store"
)

func TestWorkerManagerSkipsDisabledArchivedRuntime(t *testing.T) {
	localStore := openTestLocalStore(t)
	statuses := []model.ComponentStatus{{
		Manifest:                probeManifest(),
		ManifestValid:           true,
		ShellContractCompatible: true,
		RuntimeEnabled:          false,
		RuntimeStatus:           model.RuntimeStatusArchived,
	}}
	manager := NewManager(t.TempDir(), testShellStatus(), statuses, localStore, func(context.Context, model.EventEnvelope) {})

	diagnostics := manager.RuntimeDiagnostics()
	diagnostic, ok := diagnostics["probe"]
	if !ok {
		t.Fatalf("missing disabled runtime diagnostics: %+v", diagnostics)
	}
	if diagnostic.Enabled || diagnostic.Status != model.RuntimeStatusArchived {
		t.Fatalf("diagnostic = %+v, want disabled archived", diagnostic)
	}
	if err := manager.StartOperation("probe", model.ComponentOperation{OperationID: "op_archived"}); err == nil {
		t.Fatalf("expected archived worker start to fail")
	}
}

func TestWorkerManagerRunsProbeJob(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for worker tests")
	}

	localStore := openTestLocalStore(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(repoRoot): %v", err)
	}
	shell := testShellStatus()
	statuses := []model.ComponentStatus{{
		Manifest:                probeManifest(),
		ManifestValid:           true,
		ShellContractCompatible: true,
		RuntimeEnabled:          true,
	}}

	var (
		mu        sync.Mutex
		envelopes []model.EventEnvelope
	)
	manager := NewManager(repoRoot, shell, statuses, localStore, func(_ context.Context, envelope model.EventEnvelope) {
		mu.Lock()
		defer mu.Unlock()
		envelopes = append(envelopes, envelope)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	input, _ := json.Marshal(map[string]any{"label": "probe-test", "duration_ms": 100})
	operation, err := localStore.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "probe",
		OperationName: "job.run",
		ResourceID:    model.NewID("job"),
		Status:        model.OperationStatusAccepted,
		Input:         input,
		AcceptedAt:    model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation: %v", err)
	}
	if err := manager.StartOperation("probe", operation); err != nil {
		t.Fatalf("StartOperation: %v", err)
	}

	completed := waitForOperationStatus(t, localStore, operation.OperationID, model.OperationStatusCompleted, 5*time.Second)
	if len(completed.Result) == 0 {
		t.Fatalf("expected completed result payload")
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, envelope := range envelopes {
		if envelope.Stream == model.EventStreamProbeJob && envelope.Kind == "job.completed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected probe.job completion envelope, got %+v", envelopes)
	}
}

func TestWorkerManagerInterruptsInflightOperationOnCrashAndRecovers(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for worker tests")
	}

	root := t.TempDir()
	marker := filepath.Join(root, "crashed.marker")
	workerPath := filepath.Join(root, "crashy_worker.py")
	if err := os.WriteFile(workerPath, []byte(crashyWorkerScript(marker)), 0o755); err != nil {
		t.Fatalf("write worker: %v", err)
	}

	localStore := openTestLocalStore(t)
	shell := testShellStatus()
	statuses := []model.ComponentStatus{{
		Manifest: model.ComponentManifest{
			ID:                "probe",
			Name:              "Probe",
			Version:           "0.1.0",
			Stage:             "prototype",
			ReleaseLine:       "component-probe",
			ReleaseChannel:    "public_preview",
			ShellContract:     model.ShellContractV2,
			ComponentClass:    model.ComponentClassHeavy,
			RuntimeShape:      model.RuntimeShapeWorker,
			RuntimeOwner:      "watcher-service",
			Streams:           []string{model.EventStreamProbeJob},
			Resources:         []string{"job"},
			Operations:        []string{"job.run"},
			ShellDependencies: []string{"event_bus", "operation_contract", "diagnostics", "worker_lane"},
			Docs:              []string{"modules/probe/README.md"},
			Worker: &model.ComponentWorkerConfig{
				Entrypoint:  "python3",
				Args:        []string{workerPath},
				Healthcheck: "process_and_ping",
				Operations:  []string{"job.run"},
				Streams:     []string{model.EventStreamProbeJob},
			},
		},
		ManifestValid:           true,
		ShellContractCompatible: true,
		RuntimeEnabled:          true,
	}}

	manager := NewManager(root, shell, statuses, localStore, func(_ context.Context, _ model.EventEnvelope) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	input, _ := json.Marshal(map[string]any{"label": "crash-once", "duration_ms": 50})
	first, err := localStore.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "probe",
		OperationName: "job.run",
		ResourceID:    model.NewID("job"),
		Status:        model.OperationStatusAccepted,
		Input:         input,
		AcceptedAt:    model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation(first): %v", err)
	}
	if err := manager.StartOperation("probe", first); err != nil {
		t.Fatalf("StartOperation(first): %v", err)
	}

	interrupted := waitForOperationStatus(t, localStore, first.OperationID, model.OperationStatusInterrupted, 6*time.Second)
	if interrupted.LastError == "" {
		t.Fatalf("expected interrupted operation to carry last_error")
	}

	secondInput, _ := json.Marshal(map[string]any{"label": "recovered", "duration_ms": 50})
	second, err := localStore.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "probe",
		OperationName: "job.run",
		ResourceID:    model.NewID("job"),
		Status:        model.OperationStatusAccepted,
		Input:         secondInput,
		AcceptedAt:    model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation(second): %v", err)
	}
	if err := manager.StartOperation("probe", second); err != nil {
		t.Fatalf("StartOperation(second): %v", err)
	}
	waitForOperationStatus(t, localStore, second.OperationID, model.OperationStatusCompleted, 6*time.Second)
}

func TestWorkerManagerRestart(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for worker tests")
	}

	localStore := openTestLocalStore(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(repoRoot): %v", err)
	}
	manager := NewManager(repoRoot, testShellStatus(), []model.ComponentStatus{{
		Manifest:                probeManifest(),
		ManifestValid:           true,
		ShellContractCompatible: true,
		RuntimeEnabled:          true,
	}}, localStore, func(_ context.Context, _ model.EventEnvelope) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	waitForWorkerRuntimeStatus(t, manager, "probe", model.RuntimeStatusRunning, 5*time.Second)
	before := manager.RuntimeDiagnostics()["probe"]
	if before.WorkerPID <= 0 {
		t.Fatalf("expected running worker pid, got %+v", before)
	}

	if err := manager.Restart("probe"); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	waitForWorkerRuntimeStatus(t, manager, "probe", model.RuntimeStatusRunning, 5*time.Second)
	after := manager.RuntimeDiagnostics()["probe"]
	if after.RestartCount <= before.RestartCount {
		t.Fatalf("expected restart_count to increase: before=%+v after=%+v", before, after)
	}

	diagnostics, err := localStore.ListShellDiagnostics(10, "probe")
	if err != nil {
		t.Fatalf("ListShellDiagnostics: %v", err)
	}
	foundRestart := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == "worker.restarted" {
			foundRestart = true
			break
		}
	}
	if !foundRestart {
		t.Fatalf("expected worker.restarted diagnostic, got %+v", diagnostics)
	}
}

func openTestLocalStore(t *testing.T) *store.LocalStore {
	t.Helper()
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() {
		_ = localStore.Close()
	})
	return localStore
}

func testShellStatus() model.ShellStatus {
	return model.ShellStatus{
		Manifest: model.ShellManifest{
			ID:              "watcher.shell",
			Name:            "Watcher Shell",
			ContractVersion: model.ShellContractV2,
		},
		Version: "test",
	}
}

func probeManifest() model.ComponentManifest {
	return model.ComponentManifest{
		ID:                "probe",
		Name:              "Probe",
		Version:           "0.1.0",
		Stage:             "prototype",
		ReleaseLine:       "component-probe",
		ReleaseChannel:    "public_preview",
		ShellContract:     model.ShellContractV2,
		ComponentClass:    model.ComponentClassHeavy,
		RuntimeShape:      model.RuntimeShapeWorker,
		RuntimeOwner:      "watcher-service",
		Streams:           []string{model.EventStreamProbeJob},
		Resources:         []string{"job"},
		Operations:        []string{"job.run"},
		ShellDependencies: []string{"event_bus", "operation_contract", "diagnostics", "worker_lane"},
		Docs:              []string{"modules/probe/README.md"},
		Worker: &model.ComponentWorkerConfig{
			Entrypoint:  "python3",
			Args:        []string{filepath.Join("modules", "probe", "worker.py")},
			Env:         map[string]string{"PYTHONUNBUFFERED": "1"},
			Healthcheck: "process_and_ping",
			Operations:  []string{"job.run"},
			Streams:     []string{model.EventStreamProbeJob},
		},
	}
}

func waitForOperationStatus(t *testing.T, localStore *store.LocalStore, operationID, want string, timeout time.Duration) model.ComponentOperation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		operation, err := localStore.GetComponentOperation(operationID)
		if err == nil && operation.Status == want {
			return operation
		}
		time.Sleep(50 * time.Millisecond)
	}
	operation, err := localStore.GetComponentOperation(operationID)
	if err != nil {
		t.Fatalf("GetComponentOperation(%s): %v", operationID, err)
	}
	t.Fatalf("operation %s status = %s, want %s", operationID, operation.Status, want)
	return model.ComponentOperation{}
}

func waitForWorkerRuntimeStatus(t *testing.T, manager *Manager, componentID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		diagnostics := manager.RuntimeDiagnostics()
		if diagnostic, ok := diagnostics[componentID]; ok && diagnostic.Status == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	diagnostics := manager.RuntimeDiagnostics()
	t.Fatalf("component %s runtime status = %+v, want %s", componentID, diagnostics[componentID], want)
}

func crashyWorkerScript(marker string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
import json
import os
import sys
import time
from datetime import datetime, timezone

MARKER = %q

def now():
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

def send(message_type, payload=None):
    message = {"type": message_type}
    if payload is not None:
        message["payload"] = payload
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    message = json.loads(raw)
    kind = message.get("type")
    payload = message.get("payload") or {}
    if kind == "health.ping":
        send("health.ok", {"timestamp": now(), "ready": True, "detail": "crashy"})
    elif kind == "operation.start":
        operation = payload.get("operation") or {}
        operation_id = operation.get("operation_id")
        resource_id = operation.get("resource_id") or ("job:" + operation_id)
        send("operation.update", {"operation_id": operation_id, "status": "running", "resource_id": resource_id})
        if not os.path.exists(MARKER):
            open(MARKER, "w").close()
            sys.exit(2)
        time.sleep(0.05)
        send("operation.update", {
            "operation_id": operation_id,
            "status": "completed",
            "resource_id": resource_id,
            "result": {"job_id": resource_id, "message": "recovered"}
        })
`, marker)
}
