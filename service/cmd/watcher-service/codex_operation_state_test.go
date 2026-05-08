package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"watcher/internal/codexbridge"
	"watcher/internal/model"
	"watcher/internal/store"
)

func TestMarkThreadOperationWaitingAttachesTurnID(t *testing.T) {
	app := newCodexOperationStateTestApp(t)
	saved, err := app.store.SaveCodexOperation(model.CodexOperation{
		OperationID: "codop_wait",
		Kind:        "turn_start",
		ThreadID:    "thread_1",
		Status:      "running",
		AcceptedAt:  model.NowString(),
		StartedAt:   model.NowString(),
		CreatedAt:   model.NowString(),
		UpdatedAt:   model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveCodexOperation: %v", err)
	}

	app.markThreadOperationWaiting("thread_1", "turn_1")

	got, err := app.store.GetCodexOperation(saved.OperationID)
	if err != nil {
		t.Fatalf("GetCodexOperation: %v", err)
	}
	if got.Status != "waiting_user_input" {
		t.Fatalf("Status = %q, want waiting_user_input", got.Status)
	}
	if got.TurnID != "turn_1" {
		t.Fatalf("TurnID = %q, want turn_1", got.TurnID)
	}
}

func TestAttachCodexOperationTurnIDPreservesWaitingState(t *testing.T) {
	app := newCodexOperationStateTestApp(t)
	now := model.NowString()
	stale := model.CodexOperation{
		OperationID: "codop_race",
		Kind:        "turn_start",
		ThreadID:    "thread_1",
		Status:      "running",
		AcceptedAt:  now,
		StartedAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := app.store.SaveCodexOperation(stale); err != nil {
		t.Fatalf("SaveCodexOperation running: %v", err)
	}
	waiting := stale
	waiting.Status = "waiting_user_input"
	waiting.TurnID = "turn_1"
	waiting.UpdatedAt = model.NowString()
	if _, err := app.store.SaveCodexOperation(waiting); err != nil {
		t.Fatalf("SaveCodexOperation waiting: %v", err)
	}

	got := app.attachCodexOperationTurnID(context.Background(), stale, "turn_1")
	if got.Status != "waiting_user_input" {
		t.Fatalf("Status = %q, want waiting_user_input", got.Status)
	}
	if got.TurnID != "turn_1" {
		t.Fatalf("TurnID = %q, want turn_1", got.TurnID)
	}
	stored, err := app.store.GetCodexOperation(stale.OperationID)
	if err != nil {
		t.Fatalf("GetCodexOperation: %v", err)
	}
	if stored.Status != "waiting_user_input" {
		t.Fatalf("stored Status = %q, want waiting_user_input", stored.Status)
	}
}

func TestStartCodexThreadOperationQueuesBehindActiveThread(t *testing.T) {
	app := newCodexOperationStateTestApp(t)
	app.codexLocks = codexbridge.NewSessionLocker()
	now := model.NowString()
	operation, err := app.store.SaveCodexOperation(model.CodexOperation{
		OperationID: "codop_queue",
		Kind:        "turn_start",
		ThreadID:    "thread_1",
		Status:      "accepted",
		AcceptedAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("SaveCodexOperation: %v", err)
	}
	if err := app.codexLocks.Lock(context.Background(), "thread_1"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	started := make(chan *codexOperationContext, 1)
	go func() {
		opCtx, ok := app.startCodexThreadOperation(operation.OperationID, "thread_1")
		if !ok {
			started <- nil
			return
		}
		started <- opCtx
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := app.store.GetCodexOperation(operation.OperationID)
		if err != nil {
			t.Fatalf("GetCodexOperation: %v", err)
		}
		if got.Status == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation status = %q, want queued", got.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	app.codexLocks.Unlock("thread_1")
	select {
	case opCtx := <-started:
		if opCtx == nil {
			t.Fatal("startCodexThreadOperation returned !ok")
		}
		defer opCtx.close()
		if opCtx.operation.Status != "running" {
			t.Fatalf("operation status after lock release = %q, want running", opCtx.operation.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued operation did not start after lock release")
	}
}

func TestRepairLateCodexThreadStartCompletesTimedOutOperation(t *testing.T) {
	app := newCodexOperationStateTestApp(t)
	now := time.Now().UTC()
	operation, err := app.store.SaveCodexOperation(model.CodexOperation{
		OperationID: "codop_thread_timeout",
		Kind:        "thread_start",
		Status:      "failed",
		LastError:   "formal codex app-server unavailable: thread/start timed out",
		AcceptedAt:  now.Add(-20 * time.Second).Format(time.RFC3339),
		StartedAt:   now.Add(-20 * time.Second).Format(time.RFC3339),
		CompletedAt: now.Add(-10 * time.Second).Format(time.RFC3339),
		CreatedAt:   now.Add(-20 * time.Second).Format(time.RFC3339),
		UpdatedAt:   now.Add(-10 * time.Second).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("SaveCodexOperation: %v", err)
	}

	app.repairLateCodexThreadStart(context.Background(), "thread_late", now.Format(time.RFC3339))

	got, err := app.store.GetCodexOperation(operation.OperationID)
	if err != nil {
		t.Fatalf("GetCodexOperation: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
	if got.ThreadID != "thread_late" {
		t.Fatalf("ThreadID = %q, want thread_late", got.ThreadID)
	}
	if got.LastError != "" {
		t.Fatalf("LastError = %q, want empty", got.LastError)
	}
	overlay, err := app.store.GetCodexThreadOverlay("thread_late")
	if err != nil {
		t.Fatalf("GetCodexThreadOverlay: %v", err)
	}
	if !overlay.AppManaged || overlay.LastActiveEndpoint != "mobile" {
		t.Fatalf("overlay = %+v, want app-managed mobile overlay", overlay)
	}
}

func TestRepairLateCodexThreadStartIgnoresOldTimeout(t *testing.T) {
	app := newCodexOperationStateTestApp(t)
	now := time.Now().UTC()
	operation, err := app.store.SaveCodexOperation(model.CodexOperation{
		OperationID: "codop_old_thread_timeout",
		Kind:        "thread_start",
		Status:      "failed",
		LastError:   "formal codex app-server unavailable: thread/start timed out",
		CompletedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
		CreatedAt:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		UpdatedAt:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("SaveCodexOperation: %v", err)
	}

	app.repairLateCodexThreadStart(context.Background(), "thread_late", now.Format(time.RFC3339))

	got, err := app.store.GetCodexOperation(operation.OperationID)
	if err != nil {
		t.Fatalf("GetCodexOperation: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("Status = %q, want failed", got.Status)
	}
	if got.ThreadID != "" {
		t.Fatalf("ThreadID = %q, want empty", got.ThreadID)
	}
}

func newCodexOperationStateTestApp(t *testing.T) *App {
	t.Helper()
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = localStore.Close() })
	return &App{store: localStore, shutdownCtx: context.Background()}
}
