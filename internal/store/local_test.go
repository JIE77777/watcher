package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"watcher/internal/model"
)

func TestLocalStoreWatcherTaskEventsRoundTrip(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	first := model.WatcherTaskEvent{
		EventID:    "evt_box_one",
		TaskID:     "task_alpha",
		ToolID:     "example_tool",
		TaskName:   "Alpha",
		ResourceID: "thread_alpha",
		ItemKey:    "item_alpha",
		ThreadKey:  "thread_alpha",
		SnapshotID: "snap_one",
		ItemTitle:  "FFT",
		Summary:    "alpha summary",
		Body:       "alpha body",
		Severity:   "info",
		Labels:     []string{"example"},
		ChangeType: model.WatcherTaskChangeChanged,
		OccurredAt: model.NowString(),
	}
	second := model.WatcherTaskEvent{
		EventID:    "evt_box_two",
		TaskID:     "task_beta",
		ToolID:     "example_tool",
		TaskName:   "Beta",
		ResourceID: "task_beta:item_beta",
		ItemKey:    "item_beta",
		SnapshotID: "snap_two",
		ItemTitle:  "Tensor",
		Summary:    "beta summary",
		Body:       "beta body",
		Severity:   "warn",
		ChangeType: model.WatcherTaskChangeAppeared,
		OccurredAt: model.NowString(),
	}

	if err := store.SaveWatcherTaskEvent(first); err != nil {
		t.Fatalf("SaveWatcherTaskEvent(first): %v", err)
	}
	if err := store.SaveWatcherTaskEvent(second); err != nil {
		t.Fatalf("SaveWatcherTaskEvent(second): %v", err)
	}

	got, err := store.GetWatcherTaskEvent(first.EventID)
	if err != nil {
		t.Fatalf("GetWatcherTaskEvent: %v", err)
	}
	if got.ResourceID != "thread_alpha" {
		t.Fatalf("ResourceID = %q, want thread_alpha", got.ResourceID)
	}
	if got.EnvelopeKind() != "item.changed" {
		t.Fatalf("EnvelopeKind = %q, want item.changed", got.EnvelopeKind())
	}

	eventsByTask, err := store.ListWatcherTaskEvents(10, "task_alpha", "")
	if err != nil {
		t.Fatalf("ListWatcherTaskEvents(task): %v", err)
	}
	if len(eventsByTask) != 1 || eventsByTask[0].EventID != first.EventID {
		t.Fatalf("eventsByTask = %#v, want only %q", eventsByTask, first.EventID)
	}

	eventsByResource, err := store.ListWatcherTaskEvents(10, "", "task_beta:item_beta")
	if err != nil {
		t.Fatalf("ListWatcherTaskEvents(resource): %v", err)
	}
	if len(eventsByResource) != 1 || eventsByResource[0].EventID != second.EventID {
		t.Fatalf("eventsByResource = %#v, want only %q", eventsByResource, second.EventID)
	}
}

func TestLocalStoreShellDiagnosticsRoundTrip(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	first, err := store.SaveShellDiagnostic(model.ShellDiagnosticEvent{
		ComponentID: "probe",
		Kind:        "worker.started",
		Severity:    "info",
		Message:     "probe worker started",
		OccurredAt:  model.NowString(),
		Payload:     testJSON(t, map[string]any{"pid": 1234}),
	})
	if err != nil {
		t.Fatalf("SaveShellDiagnostic(first): %v", err)
	}
	if _, err := store.SaveShellDiagnostic(model.ShellDiagnosticEvent{
		ComponentID: "probe",
		Kind:        "worker.crashed",
		Severity:    "error",
		Message:     "probe worker crashed",
		OccurredAt:  model.NowString(),
		Payload:     testJSON(t, map[string]any{"exit_code": 1}),
	}); err != nil {
		t.Fatalf("SaveShellDiagnostic(second): %v", err)
	}

	diagnostics, err := store.ListShellDiagnostics(10, "probe")
	if err != nil {
		t.Fatalf("ListShellDiagnostics: %v", err)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("len(diagnostics) = %d, want 2", len(diagnostics))
	}
	latest, err := store.LatestShellDiagnosticError()
	if err != nil {
		t.Fatalf("LatestShellDiagnosticError: %v", err)
	}
	if latest.Kind != "worker.crashed" {
		t.Fatalf("latest.Kind = %q, want worker.crashed", latest.Kind)
	}
	if first.DiagnosticID == "" {
		t.Fatalf("expected generated diagnostic id")
	}
}

func testJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}
