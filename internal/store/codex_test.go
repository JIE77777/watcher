package store

import (
	"path/filepath"
	"testing"

	"watcher/internal/model"
)

func TestBatchGetCodexThreadOverlays(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	for _, threadID := range []string{"t1", "t2", "t3"} {
		if _, err := store.UpsertCodexThreadOverlay(model.CodexThreadOverlay{
			ThreadID:   threadID,
			AppManaged: true,
		}); err != nil {
			t.Fatalf("UpsertCodexThreadOverlay %s: %v", threadID, err)
		}
	}

	result, err := store.BatchGetCodexThreadOverlays([]string{"t1", "t2", "t4"})
	if err != nil {
		t.Fatalf("BatchGetCodexThreadOverlays: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	if !result["t1"].AppManaged {
		t.Fatal("expected t1 overlay to be app managed")
	}
	if _, ok := result["t4"]; ok {
		t.Fatal("expected t4 to not be in result")
	}
}

func TestBatchGetLatestCodexOperationsByThread(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	for _, op := range []model.CodexOperation{
		{OperationID: "op1", Kind: "turn_start", ThreadID: "t1", Status: "completed"},
		{OperationID: "op2", Kind: "turn_start", ThreadID: "t1", Status: "running"},
		{OperationID: "op3", Kind: "turn_start", ThreadID: "t2", Status: "accepted"},
	} {
		if _, err := store.SaveCodexOperation(op); err != nil {
			t.Fatalf("SaveCodexOperation: %v", err)
		}
	}

	result, err := store.BatchGetLatestCodexOperationsByThread([]string{"t1", "t2", "t_missing"})
	if err != nil {
		t.Fatalf("BatchGetLatestCodexOperationsByThread: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	if _, ok := result["t_missing"]; ok {
		t.Fatal("expected t_missing to not be in result")
	}
	if result["t2"].OperationID != "op3" {
		t.Fatalf("t2 latest operation = %q, want op3", result["t2"].OperationID)
	}
}

func TestListCodexOperationTurnIDsByThread(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	for _, op := range []model.CodexOperation{
		{OperationID: "op1", Kind: "turn_start", ThreadID: "t1", TurnID: "turn_a", Status: "completed"},
		{OperationID: "op2", Kind: "turn_start", ThreadID: "t1", TurnID: "turn_a", Status: "completed"},
		{OperationID: "op3", Kind: "turn_start", ThreadID: "t1", TurnID: "turn_b", Status: "running"},
		{OperationID: "op4", Kind: "turn_start", ThreadID: "t1", TurnID: "", Status: "accepted"},
	} {
		if _, err := store.SaveCodexOperation(op); err != nil {
			t.Fatalf("SaveCodexOperation: %v", err)
		}
	}

	turnIDs, err := store.ListCodexOperationTurnIDsByThread("t1", 500)
	if err != nil {
		t.Fatalf("ListCodexOperationTurnIDsByThread: %v", err)
	}
	if len(turnIDs) != 2 {
		t.Fatalf("len(turnIDs) = %d, want 2 (got %v)", len(turnIDs), turnIDs)
	}
}

func TestLocalStoreCodexOperationRoundTrip(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	saved, err := store.SaveCodexOperation(model.CodexOperation{
		OperationID: "codop_test",
		Kind:        "turn_start",
		ThreadID:    "thread_123",
		Status:      "accepted",
		AcceptedAt:  model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveCodexOperation: %v", err)
	}
	got, err := store.GetCodexOperation(saved.OperationID)
	if err != nil {
		t.Fatalf("GetCodexOperation: %v", err)
	}
	if got.OperationID != saved.OperationID {
		t.Fatalf("OperationID = %q, want %q", got.OperationID, saved.OperationID)
	}
	if got.ThreadID != "thread_123" {
		t.Fatalf("ThreadID = %q, want thread_123", got.ThreadID)
	}

	list, err := store.ListCodexOperationsByThread("thread_123", 10)
	if err != nil {
		t.Fatalf("ListCodexOperationsByThread: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
}

func TestLocalStoreCodexPendingServerRequestRoundTrip(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	saved, err := store.SaveCodexPendingServerRequest(model.CodexPendingServerRequest{
		RequestID:      "req_test",
		ThreadID:       "thread_123",
		TurnID:         "turn_123",
		Method:         "item/tool/requestUserInput",
		Status:         "created",
		Supported:      true,
		ResolutionKind: "request_user_input",
		UIKind:         "request_user_input",
		ParamsJSON:     []byte(`{"questions":[]}`),
	})
	if err != nil {
		t.Fatalf("SaveCodexPendingServerRequest: %v", err)
	}
	got, err := store.GetCodexPendingServerRequest(saved.RequestID)
	if err != nil {
		t.Fatalf("GetCodexPendingServerRequest: %v", err)
	}
	if !got.Supported {
		t.Fatalf("Supported = false, want true")
	}
	if got.ResolutionKind != "request_user_input" {
		t.Fatalf("ResolutionKind = %q, want request_user_input", got.ResolutionKind)
	}
	if got.UIKind != "request_user_input" {
		t.Fatalf("UIKind = %q, want request_user_input", got.UIKind)
	}
}
