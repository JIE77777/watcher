package main

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"watcher/internal/components"
	"watcher/internal/model"
	"watcher/internal/push"
)

func (a *App) handleShellV2(w http.ResponseWriter, _ *http.Request) {
	shell, _, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shell": shell,
	})
}

func (a *App) handleShellHomeV2(w http.ResponseWriter, _ *http.Request) {
	home, err := a.shellHomeSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"home": home,
	})
}

func (a *App) handleComponentsV2(w http.ResponseWriter, _ *http.Request) {
	shell, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shell":      shell,
		"components": componentStatuses,
	})
}

func (a *App) shellHomeSnapshot() (model.ShellHome, error) {
	shell, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		return model.ShellHome{}, err
	}
	statuses := make(map[string]model.ComponentStatus, len(componentStatuses))
	for _, status := range componentStatuses {
		statuses[status.Manifest.ID] = status
	}

	components := a.shellHomeComponentCells(componentStatuses)

	signals := make([]model.ShellSignal, 0, 8)
	if cell := componentCellByID(components, "codex"); cell != nil {
		signals = append(signals, a.codexHomeSignals(cell)...)
	}
	if cell := componentCellByID(components, "box"); cell != nil {
		signals = append(signals, a.boxHomeSignals(cell)...)
	}
	if cell := componentCellByID(components, "host"); cell != nil {
		signals = append(signals, a.hostHomeSignals(cell)...)
	}
	if cell := componentCellByID(components, "opencode"); cell != nil {
		signals = append(signals, a.opencodeHomeSignals(cell)...)
	}
	if cell := componentCellByID(components, "pilot"); cell != nil {
		signals = append(signals, a.pilotHomeSignals(cell)...)
	}
	if cell := componentCellByID(components, "cc"); cell != nil {
		signals = append(signals, a.ccHomeSignals(cell)...)
	}
	signals = append(signals, componentHealthSignals(components, statuses)...)
	signals = filterAndSortShellSignals(signals, 5)

	return model.ShellHome{
		Status:     shellHomeStatus(shell, componentStatuses),
		UpdatedAt:  model.NowString(),
		Signals:    nonNilSignals(signals),
		Components: nonNilCells(components),
	}, nil
}

type shellHomeComponentPreference struct {
	label string
	icon  string
	order int
}

var shellHomeComponentPreferences = map[string]shellHomeComponentPreference{
	"codex":    {label: "Codex", icon: "λ", order: 10},
	"box":      {label: "Box", icon: "◇", order: 20},
	"host":     {label: "Host", icon: "◎", order: 25},
	"opencode": {label: "Opencode", icon: "⌁", order: 30},
	"pilot":    {label: "Pilot", icon: "∴", order: 40},
	"cc":       {label: "CC", icon: "μ", order: 50},
}

func (a *App) shellHomeComponentCells(statuses []model.ComponentStatus) []model.ComponentCell {
	cells := make([]model.ComponentCell, 0, len(statuses)+1)
	for _, status := range statuses {
		if !moduleHasAndroidEntry(status.Manifest) {
			continue
		}
		pref := shellHomePreference(status.Manifest.ID)
		cells = append(cells, a.componentCell(
			status.Manifest.ID,
			pref.label,
			pref.icon,
			status,
			normalizedModuleDefaultTarget(status.Manifest, normalizedModuleSurfaces(status.Manifest)),
		))
	}
	sort.SliceStable(cells, func(i, j int) bool {
		left := shellHomePreference(cells[i].ComponentID)
		right := shellHomePreference(cells[j].ComponentID)
		if left.order != right.order {
			return left.order < right.order
		}
		return cells[i].ComponentID < cells[j].ComponentID
	})
	cells = append(cells, model.ComponentCell{
		ComponentID: "game",
		Label:       "打砖块",
		Icon:        "▣",
		State:       "ready",
		Target:      model.ShellTarget{ComponentID: "game", Surface: "play"},
	})
	return cells
}

func shellHomePreference(componentID string) shellHomeComponentPreference {
	if pref, ok := shellHomeComponentPreferences[componentID]; ok {
		return pref
	}
	label := strings.ToUpper(componentID)
	if label == "" {
		label = "Module"
	}
	return shellHomeComponentPreference{label: label, icon: "□", order: 1000}
}

func moduleHasAndroidEntry(manifest model.ComponentManifest) bool {
	if strings.TrimSpace(manifest.ID) == "" {
		return false
	}
	if components.IsArchivedComponent(manifest) {
		return false
	}
	return len(manifest.AndroidSurfaces) > 0
}

func componentCellByID(cells []model.ComponentCell, componentID string) *model.ComponentCell {
	for index := range cells {
		if cells[index].ComponentID == componentID {
			return &cells[index]
		}
	}
	return nil
}

func (a *App) componentCell(componentID, label, icon string, status model.ComponentStatus, target model.ShellTarget) model.ComponentCell {
	cell := model.ComponentCell{
		ComponentID: componentID,
		Label:       label,
		Icon:        icon,
		State:       "off",
		Target:      target,
	}
	if status.Manifest.ID == "" {
		return cell
	}
	cell.Label = firstNonBlank(status.Manifest.Name, label)
	if target := normalizedModuleDefaultTarget(status.Manifest, normalizedModuleSurfaces(status.Manifest)); target.ComponentID != "" && target.Surface != "" {
		cell.Target = target
	}
	cell.State = cellStateFromComponentStatus(status)
	return cell
}

func (a *App) codexHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	signals := []model.ShellSignal{}
	requests, err := a.store.ListCodexPendingServerRequests("", 8)
	if err == nil {
		activeCount := 0
		for _, request := range requests {
			if !isActiveCodexServerRequest(request) {
				continue
			}
			activeCount++
			signals = append(signals, model.ShellSignal{
				SignalID:       "codex_request_" + request.RequestID,
				ComponentID:    "codex",
				Level:          "action",
				Title:          "Codex waiting",
				Subtitle:       codexServerRequestSubtitle(request),
				Target:         model.ShellTarget{ComponentID: "codex", Surface: "thread", ResourceID: request.ThreadID},
				OccurredAt:     firstNonBlank(request.CreatedAt, request.UpdatedAt, model.NowString()),
				ActionRequired: true,
			})
		}
		if activeCount > 0 {
			cell.State = "wait"
			cell.Badge = "Wait " + strconv.Itoa(activeCount)
			return signals
		}
	}

	activeOps, err := a.store.ListCodexOperationsByStatuses([]string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}, 4)
	if err == nil && len(activeOps) > 0 {
		op := activeOps[0]
		cell.State = stateForOperationStatus(op.Status)
		cell.Badge = shortOperationBadge(op.Status)
		signals = append(signals, model.ShellSignal{
			SignalID:    "codex_operation_" + op.OperationID,
			ComponentID: "codex",
			Level:       levelForOperationStatus(op.Status),
			Title:       "Codex " + shortOperationBadge(op.Status),
			Subtitle:    firstNonBlank(op.Prompt, op.Kind),
			Target:      model.ShellTarget{ComponentID: "codex", Surface: "thread", ResourceID: op.ThreadID},
			OccurredAt:  firstNonBlank(op.UpdatedAt, op.CreatedAt, model.NowString()),
		})
	}
	return signals
}

func (a *App) boxHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	events, err := a.store.ListWatcherTaskEvents(2, "", "")
	if err != nil || len(events) == 0 {
		return nil
	}
	cell.State = "new"
	cell.Badge = "New " + strconv.Itoa(len(events))
	signals := make([]model.ShellSignal, 0, len(events))
	for _, event := range events {
		signals = append(signals, model.ShellSignal{
			SignalID:    "box_event_" + event.EventID,
			ComponentID: "box",
			Level:       signalLevelFromSeverity(event.Severity),
			Title:       cleanSignalText(firstNonBlank(event.DisplayTitle(), event.TaskName, "Box event")),
			Subtitle:    cleanSignalText(firstNonBlank(event.Summary, event.ChangeType)),
			Target:      model.ShellTarget{ComponentID: "box", Surface: "event", ResourceID: event.EventID},
			OccurredAt:  firstNonBlank(event.OccurredAt, model.NowString()),
		})
	}
	return signals
}

func (a *App) hostHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	overview := a.hostOverview()
	for _, disk := range overview.Disks {
		if disk.UsedPercent < 90 && disk.AvailableBytes >= 1<<30 {
			continue
		}
		cell.State = "degraded"
		cell.Badge = "Disk"
		return []model.ShellSignal{{
			SignalID:    "host_disk_low_" + disk.RootID,
			ComponentID: hostComponentID,
			Level:       "warning",
			Title:       "Host disk space is low",
			Subtitle:    cleanSignalText(disk.Label + " " + strconv.Itoa(int(disk.UsedPercent)) + "% used"),
			Target:      model.ShellTarget{ComponentID: hostComponentID, Surface: "overview"},
			OccurredAt:  model.NowString(),
		}}
	}
	if overview.Memory.UsedPercent >= 90 {
		cell.State = "degraded"
		cell.Badge = "Mem"
		return []model.ShellSignal{{
			SignalID:    "host_memory_high",
			ComponentID: hostComponentID,
			Level:       "warning",
			Title:       "Host memory usage is high",
			Subtitle:    strconv.Itoa(int(overview.Memory.UsedPercent)) + "% used",
			Target:      model.ShellTarget{ComponentID: hostComponentID, Surface: "overview"},
			OccurredAt:  model.NowString(),
		}}
	}
	return nil
}

func (a *App) pilotHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	operations, err := a.store.ListComponentOperationsByStatuses("pilot", activeComponentOperationStatuses(), 3)
	if err != nil || len(operations) == 0 {
		return nil
	}
	op := operations[0]
	cell.State = stateForOperationStatus(op.Status)
	cell.Badge = shortOperationBadge(op.Status)
	return []model.ShellSignal{{
		SignalID:    "pilot_operation_" + op.OperationID,
		ComponentID: "pilot",
		Level:       levelForOperationStatus(op.Status),
		Title:       "Pilot " + shortOperationBadge(op.Status),
		Subtitle:    firstNonBlank(op.OperationName, "operation"),
		Target:      model.ShellTarget{ComponentID: "pilot", Surface: "session", ResourceID: op.ResourceID},
		OccurredAt:  firstNonBlank(op.UpdatedAt, op.CreatedAt, model.NowString()),
	}}
}

func (a *App) opencodeHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	signals := []model.ShellSignal{}
	permissions, err := a.store.ListOpencodePermissionRequestsByStatus("pending", 3)
	if err == nil && len(permissions) > 0 {
		cell.State = "wait"
		cell.Badge = "Wait " + strconv.Itoa(len(permissions))
		for _, request := range permissions {
			target := model.ShellTarget{ComponentID: "opencode", Surface: "turn", ResourceID: request.TurnID}
			if turn, err := a.store.GetOpencodeTurn(request.TurnID); err == nil {
				target = model.ShellTarget{ComponentID: "opencode", Surface: "session", ResourceID: turn.SessionID}
			}
			signals = append(signals, model.ShellSignal{
				SignalID:       "opencode_permission_" + request.RequestID,
				ComponentID:    "opencode",
				Level:          "action",
				Title:          "Opencode waiting",
				Subtitle:       firstNonBlank(request.Kind, "permission"),
				Target:         target,
				OccurredAt:     firstNonBlank(request.RequestedAt, model.NowString()),
				ActionRequired: true,
			})
		}
		return signals
	}

	questions, err := a.store.ListOpencodeQuestionRequestsByStatus("pending", 3)
	if err == nil && len(questions) > 0 {
		cell.State = "wait"
		cell.Badge = "Wait " + strconv.Itoa(len(questions))
		for _, request := range questions {
			target := model.ShellTarget{ComponentID: "opencode", Surface: "turn", ResourceID: request.TurnID}
			if turn, err := a.store.GetOpencodeTurn(request.TurnID); err == nil {
				target = model.ShellTarget{ComponentID: "opencode", Surface: "session", ResourceID: firstNonBlank(turn.SessionID, request.NativeSessionID)}
			} else if request.NativeSessionID != "" {
				target = model.ShellTarget{ComponentID: "opencode", Surface: "session", ResourceID: request.NativeSessionID}
			}
			signals = append(signals, model.ShellSignal{
				SignalID:       "opencode_question_" + request.RequestID,
				ComponentID:    "opencode",
				Level:          "action",
				Title:          "Opencode waiting",
				Subtitle:       "question",
				Target:         target,
				OccurredAt:     firstNonBlank(request.AskedAt, model.NowString()),
				ActionRequired: true,
			})
		}
		return signals
	}

	mirrorSignals := a.opencodeMirrorHomeSignals(cell)
	if len(mirrorSignals) > 0 {
		return mirrorSignals
	}

	operations, err := a.store.ListComponentOperationsByStatuses("opencode", activeComponentOperationStatuses(), 3)
	if err != nil || len(operations) == 0 {
		return signals
	}
	op := operations[0]
	cell.State = stateForOperationStatus(op.Status)
	cell.Badge = shortOperationBadge(op.Status)
	signals = append(signals, model.ShellSignal{
		SignalID:    "opencode_operation_" + op.OperationID,
		ComponentID: "opencode",
		Level:       levelForOperationStatus(op.Status),
		Title:       "Opencode " + shortOperationBadge(op.Status),
		Subtitle:    firstNonBlank(op.ResourceID, op.OperationName),
		Target:      model.ShellTarget{ComponentID: "opencode", Surface: "session", ResourceID: op.ResourceID},
		OccurredAt:  firstNonBlank(op.UpdatedAt, op.CreatedAt, model.NowString()),
	})
	return signals
}

func (a *App) opencodeMirrorHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	sessions, err := a.store.ListOpencodeMirrorSessions(20)
	if err != nil || len(sessions) == 0 {
		return nil
	}
	signals := []model.ShellSignal{}
	activeCount := 0
	waitCount := 0
	for _, session := range sessions {
		if !validOpencodeNativeSessionID(session.NativeSessionID) {
			continue
		}
		messages, err := a.store.ListOpencodeMirrorMessages(session.NativeSessionID, 8)
		if err != nil {
			messages = nil
		}
		events, err := a.store.ListOpencodeMirrorRecentEvents(session.NativeSessionID, 120)
		if err != nil {
			events = nil
		}
		presentation := opencodeMirrorBuildPresentation(session, messages, events, 120)
		target := model.ShellTarget{
			ComponentID: "opencode",
			Surface:     "session",
			ResourceID:  session.NativeSessionID,
		}
		occurredAt := firstNonBlank(session.UpdatedAt, session.SyncedAt, session.CreatedAt, model.NowString())
		summary := cleanSignalText(opencodeMirrorSessionListSummary(session, messages, presentation))
		detail := cleanSignalText(opencodeMirrorSessionListDetail(session, messages))
		switch {
		case presentation.PendingQuestionCount > 0:
			waitCount += presentation.PendingQuestionCount
			signals = append(signals, model.ShellSignal{
				SignalID:       "opencode_mirror_question_" + firstNonBlank(presentation.PendingQuestionID, session.NativeSessionID),
				ComponentID:    "opencode",
				Level:          "action",
				Title:          "Opencode waiting",
				Subtitle:       firstNonBlank(summary, detail, session.NativeSessionID),
				Target:         target,
				OccurredAt:     occurredAt,
				ActionRequired: true,
			})
		case session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry:
			activeCount++
			signals = append(signals, model.ShellSignal{
				SignalID:    "opencode_mirror_active_" + session.NativeSessionID,
				ComponentID: "opencode",
				Level:       "info",
				Title:       "Opencode running",
				Subtitle:    firstNonBlank(summary, detail, session.NativeSessionID),
				Target:      target,
				OccurredAt:  occurredAt,
			})
		}
		if len(signals) >= 3 {
			break
		}
	}
	if waitCount > 0 {
		cell.State = "wait"
		cell.Badge = "Wait " + strconv.Itoa(waitCount)
	} else if activeCount > 0 {
		cell.State = "run"
		cell.Badge = "Run " + strconv.Itoa(activeCount)
	}
	return signals
}

func (a *App) ccHomeSignals(cell *model.ComponentCell) []model.ShellSignal {
	signals := []model.ShellSignal{}
	if sessions, err := a.listCCMimoSessions(8); err == nil {
		running := 0
		for _, session := range sessions {
			if session.Status == "accepted" || session.Status == "queued" || session.Status == "running" || session.Status == "waiting_user_input" {
				running++
				signals = append(signals, model.ShellSignal{
					SignalID:    "cc_session_" + session.SessionID,
					ComponentID: "cc",
					Level:       "info",
					Title:       "CC session " + shortOperationBadge(session.Status),
					Subtitle:    firstNonBlank(session.Title, session.SessionID),
					Target:      model.ShellTarget{ComponentID: "cc", Surface: "session", ResourceID: session.SessionID},
					OccurredAt:  firstNonBlank(session.UpdatedAt, session.CreatedAt, model.NowString()),
				})
			}
		}
		if running > 0 {
			cell.State = "run"
			cell.Badge = "Run " + strconv.Itoa(running)
			return signals
		}
	}
	operations, err := a.store.ListComponentOperationsByStatuses("cc", activeComponentOperationStatuses(), 3)
	if err == nil && len(operations) > 0 {
		op := operations[0]
		cell.State = stateForOperationStatus(op.Status)
		cell.Badge = shortOperationBadge(op.Status)
		signals = append(signals, model.ShellSignal{
			SignalID:    "cc_operation_" + op.OperationID,
			ComponentID: "cc",
			Level:       levelForOperationStatus(op.Status),
			Title:       "CC " + shortOperationBadge(op.Status),
			Subtitle:    firstNonBlank(op.ResourceID, op.OperationName),
			Target:      model.ShellTarget{ComponentID: "cc", Surface: "session", ResourceID: op.ResourceID},
			OccurredAt:  firstNonBlank(op.UpdatedAt, op.CreatedAt, model.NowString()),
		})
	}
	return signals
}

func componentHealthSignals(cells []model.ComponentCell, statuses map[string]model.ComponentStatus) []model.ShellSignal {
	signals := []model.ShellSignal{}
	for _, cell := range cells {
		status, ok := statuses[cell.ComponentID]
		if !ok || !isComponentUnhealthy(status) {
			continue
		}
		signals = append(signals, model.ShellSignal{
			SignalID:    "component_health_" + cell.ComponentID,
			ComponentID: cell.ComponentID,
			Level:       "warning",
			Title:       cell.Label + " degraded",
			Subtitle:    firstNonBlank(status.LastError, status.ValidationError, status.RuntimeStatus),
			Target:      model.ShellTarget{ComponentID: cell.ComponentID, Surface: "settings"},
			OccurredAt:  model.NowString(),
		})
	}
	return signals
}

func filterAndSortShellSignals(signals []model.ShellSignal, limit int) []model.ShellSignal {
	now := time.Now().UTC()
	filtered := make([]model.ShellSignal, 0, len(signals))
	for _, signal := range signals {
		if strings.TrimSpace(signal.SignalID) == "" || strings.TrimSpace(signal.Title) == "" {
			continue
		}
		if signal.ExpiresAt != "" {
			if expires, err := time.Parse(time.RFC3339, signal.ExpiresAt); err == nil && expires.Before(now) {
				continue
			}
		}
		filtered = append(filtered, signal)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].ActionRequired != filtered[j].ActionRequired {
			return filtered[i].ActionRequired
		}
		return filtered[i].OccurredAt > filtered[j].OccurredAt
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

func nonNilSignals(signals []model.ShellSignal) []model.ShellSignal {
	if signals == nil {
		return []model.ShellSignal{}
	}
	return signals
}

func nonNilCells(cells []model.ComponentCell) []model.ComponentCell {
	if cells == nil {
		return []model.ComponentCell{}
	}
	return cells
}

func shellHomeStatus(shell model.ShellStatus, components []model.ComponentStatus) string {
	for _, component := range components {
		if component.RuntimeStatus == model.RuntimeStatusBackoff || component.RuntimeStatus == model.RuntimeStatusInvalid || !component.ManifestValid {
			return "degraded"
		}
	}
	if shell.ServiceStatus == "running" {
		return "ready"
	}
	return "down"
}

func cellStateFromComponentStatus(status model.ComponentStatus) string {
	switch {
	case status.Manifest.ID == "" || !status.Enabled || !status.RuntimeEnabled:
		return "off"
	case !status.ManifestValid || !status.ShellContractCompatible || status.RuntimeStatus == model.RuntimeStatusInvalid:
		return "down"
	case status.RuntimeStatus == model.RuntimeStatusBackoff || status.RuntimeStatus == model.RuntimeStatusDegraded:
		return "degraded"
	case status.RuntimeStatus == model.RuntimeStatusRunning:
		return "ready"
	case status.RuntimeStatus == model.RuntimeStatusStarting:
		return "run"
	case status.RuntimeStatus == model.RuntimeStatusReady:
		return "ready"
	default:
		return "idle"
	}
}

func isComponentUnhealthy(status model.ComponentStatus) bool {
	return !status.ManifestValid || !status.ShellContractCompatible ||
		status.RuntimeStatus == model.RuntimeStatusBackoff ||
		status.RuntimeStatus == model.RuntimeStatusDegraded ||
		status.RuntimeStatus == model.RuntimeStatusInvalid
}

func isActiveCodexServerRequest(request model.CodexPendingServerRequest) bool {
	return request.Status == "pending" || strings.HasPrefix(request.Status, "created")
}

func codexServerRequestSubtitle(request model.CodexPendingServerRequest) string {
	switch request.Method {
	case "item/tool/requestUserInput":
		return "input requested"
	case "item/commandExecution/requestApproval":
		return "command approval"
	case "item/fileChange/requestApproval":
		return "file approval"
	case "item/permissions/requestApproval":
		return "permissions"
	default:
		return firstNonBlank(request.UIKind, request.Method)
	}
}

func activeComponentOperationStatuses() []string {
	return []string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}
}

func stateForOperationStatus(status string) string {
	switch status {
	case model.OperationStatusAccepted, model.OperationStatusQueued:
		return "wait"
	case model.OperationStatusRunningOp:
		return "run"
	case model.OperationStatusWaiting:
		return "wait"
	case model.OperationStatusFailed, model.OperationStatusInterrupted:
		return "degraded"
	default:
		return "idle"
	}
}

func shortOperationBadge(status string) string {
	switch status {
	case model.OperationStatusAccepted:
		return "Accept"
	case model.OperationStatusQueued:
		return "Queue"
	case model.OperationStatusRunningOp:
		return "Run"
	case model.OperationStatusWaiting:
		return "Wait"
	case model.OperationStatusFailed:
		return "Fail"
	case model.OperationStatusInterrupted:
		return "Stop"
	default:
		return firstNonBlank(status, "Idle")
	}
}

func levelForOperationStatus(status string) string {
	switch status {
	case model.OperationStatusWaiting:
		return "action"
	case model.OperationStatusFailed, model.OperationStatusInterrupted:
		return "warning"
	default:
		return "info"
	}
}

func signalLevelFromSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "error":
		return "critical"
	case "warning", "warn":
		return "warning"
	default:
		return "info"
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned != "" && cleaned != "<nil>" && strings.ToLower(cleaned) != "null" {
			return value
		}
	}
	return ""
}

func cleanSignalText(value string) string {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.ReplaceAll(cleaned, "<nil>", "empty")
	cleaned = strings.ReplaceAll(cleaned, "null", "empty")
	return cleaned
}

func (a *App) handleComponentV2(w http.ResponseWriter, r *http.Request) {
	componentID := strings.TrimSpace(r.PathValue("componentID"))
	if componentID == "" {
		http.Error(w, "component id is required", http.StatusBadRequest)
		return
	}
	_, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status, ok := findComponentStatus(componentStatuses, componentID)
	if !ok {
		http.Error(w, "component not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"component": status,
	})
}

func (a *App) handleShellDiagnosticsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	componentID := strings.TrimSpace(r.URL.Query().Get("component_id"))
	diagnostics, err := a.store.ListShellDiagnostics(limit, componentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diagnostics": diagnostics,
	})
}

func (a *App) handleShellRestartV2(w http.ResponseWriter, r *http.Request) {
	if err := a.requestShellServiceRestart(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "restart_requested",
	})
}

func (a *App) requestShellServiceRestart() error {
	if notifySocket := strings.TrimSpace(os.Getenv("NOTIFY_SOCKET")); notifySocket != "" {
		cmd := exec.Command("systemctl", "--user", "restart", "watcher-service.service")
		if err := cmd.Start(); err == nil {
			go func() {
				if err := cmd.Wait(); err != nil {
					log.Printf("shell: systemd restart command failed: %v", err)
				}
			}()
			return nil
		}
	}

	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	return nil
}

func (a *App) handleComponentRestartV2(w http.ResponseWriter, r *http.Request) {
	componentID := strings.TrimSpace(r.PathValue("componentID"))
	if componentID == "" {
		http.Error(w, "component id is required", http.StatusBadRequest)
		return
	}
	_, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status, ok := findComponentStatus(componentStatuses, componentID)
	if !ok {
		http.Error(w, "component not found", http.StatusNotFound)
		return
	}
	if status.Manifest.RuntimeShape != model.RuntimeShapeWorker {
		http.Error(w, "component runtime is not restartable", http.StatusConflict)
		return
	}
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}
	if err := a.workerManager.Restart(componentID); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, componentStatuses, err = a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status, _ = findComponentStatus(componentStatuses, componentID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"component": status,
		"status":    "restart_requested",
	})
}

func (a *App) shellSnapshot() (model.ShellStatus, []model.ComponentStatus, error) {
	shell, componentStatuses, err := a.platformStatus()
	if err != nil {
		return model.ShellStatus{}, nil, err
	}
	shell.ComponentCount = len(componentStatuses)
	shell.ServiceStatus = "running"
	shell.RelayStatus = a.relayStatus()
	shell.EventBusStatus = a.eventBusStatus()
	shell.PushStatus = a.pushChannelStatus()
	shell.ComponentStats = componentStats(componentStatuses)
	if latestError, err := a.store.LatestShellDiagnosticError(); err == nil && strings.TrimSpace(latestError.Message) != "" {
		shell.LastError = latestError.Message
	}
	if shell.LastError == "" {
		for _, component := range componentStatuses {
			if strings.TrimSpace(component.LastError) != "" {
				shell.LastError = component.LastError
				break
			}
		}
	}
	return shell, componentStatuses, nil
}

func (a *App) relayStatus() string {
	baseConfigured := strings.TrimSpace(a.cfg.Relay.BaseURL) != ""
	tokenConfigured := strings.TrimSpace(a.cfg.Relay.OwnerToken) != ""
	switch {
	case baseConfigured && tokenConfigured:
		return "configured"
	case baseConfigured || tokenConfigured:
		return "partial"
	default:
		return "disabled"
	}
}

func (a *App) eventBusStatus() string {
	if a.relayStatus() == "configured" {
		return "ready"
	}
	return "local_only"
}

func (a *App) pushChannelStatus() string {
	if a.pushManager == nil {
		return "unavailable"
	}
	status := a.pushManager.Status()
	ready := 0
	for _, ch := range status.Channels {
		if ch.Status == push.StatusReady || ch.Status == push.StatusConnected {
			ready++
		}
	}
	if ready == 0 {
		return "reserved"
	}
	return "ready"
}

func componentStats(statuses []model.ComponentStatus) model.ComponentStats {
	stats := model.ComponentStats{Total: len(statuses)}
	for _, status := range statuses {
		if status.ManifestValid && status.ShellContractCompatible {
			stats.Valid++
		} else {
			stats.Invalid++
		}
		if status.Manifest.RuntimeShape == model.RuntimeShapeWorker {
			stats.Worker++
		}
		switch status.RuntimeStatus {
		case model.RuntimeStatusRunning:
			stats.Running++
		case model.RuntimeStatusBackoff:
			stats.Backoff++
		}
	}
	return stats
}

func findComponentStatus(statuses []model.ComponentStatus, componentID string) (model.ComponentStatus, bool) {
	for _, status := range statuses {
		if status.Manifest.ID == componentID {
			return status, true
		}
	}
	return model.ComponentStatus{}, false
}

func (a *App) recordShellDiagnostic(componentID, kind, severity, message string, payload map[string]any) {
	if a == nil || a.store == nil {
		return
	}
	if _, err := a.store.SaveShellDiagnostic(model.ShellDiagnosticEvent{
		ComponentID: componentID,
		Kind:        kind,
		Severity:    severity,
		Message:     message,
		OccurredAt:  model.NowString(),
		Payload:     mustJSON(payload),
	}); err != nil {
		// The shell must remain available even when diagnostics cannot be persisted.
	}
}
