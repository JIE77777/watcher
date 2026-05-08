package store

import (
	"path/filepath"
	"testing"

	"watcher/internal/model"
)

func TestLocalStoreOpencodeSessionTurnEventRoundTrip(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	session, err := store.SaveOpencodeSession(model.OpencodeSession{
		SessionID:  "ocsess_test",
		Title:      "Watcher",
		RepoRoot:   "/workspace/watcher",
		Status:     "idle",
		Driver:     "pending",
		ConfigJSON: []byte(`{"dirty_policy":"clean"}`),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	gotSession, err := store.GetOpencodeSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetOpencodeSession: %v", err)
	}
	if gotSession.Title != "Watcher" {
		t.Fatalf("Title = %q, want Watcher", gotSession.Title)
	}

	turn, err := store.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_test",
		SessionID:   session.SessionID,
		OperationID: "op_test",
		Prompt:      "inspect",
		Status:      "accepted",
		DirtyPolicy: "clean",
		Driver:      "pending",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	gotTurn, err := store.GetOpencodeTurn(turn.TurnID)
	if err != nil {
		t.Fatalf("GetOpencodeTurn: %v", err)
	}
	if gotTurn.OperationID != "op_test" {
		t.Fatalf("OperationID = %q, want op_test", gotTurn.OperationID)
	}

	if _, err := store.InsertOpencodeEvent(model.OpencodeEvent{
		TurnID:      turn.TurnID,
		Seq:         1,
		Kind:        "turn.started",
		Source:      "watcher",
		PayloadJSON: []byte(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("InsertOpencodeEvent seq 1: %v", err)
	}
	if _, err := store.InsertOpencodeEvent(model.OpencodeEvent{
		TurnID:      turn.TurnID,
		Seq:         2,
		Kind:        "turn.event",
		Source:      "driver",
		PayloadJSON: []byte(`{"message":"hello"}`),
	}); err != nil {
		t.Fatalf("InsertOpencodeEvent seq 2: %v", err)
	}

	events, err := store.ListOpencodeEventsAfter(turn.TurnID, 1, 20)
	if err != nil {
		t.Fatalf("ListOpencodeEventsAfter: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Seq != 2 {
		t.Fatalf("Seq = %d, want 2", events[0].Seq)
	}
	tail, err := store.ListOpencodeEventsTail(turn.TurnID, 1)
	if err != nil {
		t.Fatalf("ListOpencodeEventsTail: %v", err)
	}
	if len(tail) != 1 || tail[0].Seq != 2 {
		t.Fatalf("tail = %+v, want only seq 2", tail)
	}

	maxSeq, err := store.MaxOpencodeEventSeq(turn.TurnID)
	if err != nil {
		t.Fatalf("MaxOpencodeEventSeq: %v", err)
	}
	if maxSeq != 2 {
		t.Fatalf("MaxOpencodeEventSeq = %d, want 2", maxSeq)
	}

	running, err := store.ListOpencodeTurnsByStatuses([]string{"accepted"}, 20)
	if err != nil {
		t.Fatalf("ListOpencodeTurnsByStatuses: %v", err)
	}
	if len(running) != 1 || running[0].TurnID != turn.TurnID {
		t.Fatalf("ListOpencodeTurnsByStatuses = %#v, want turn %s", running, turn.TurnID)
	}
}

func TestLocalStoreOpencodeMirrorRecentEvents(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	if _, err := store.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_mirror_events",
		Title:           "Mirror Events",
		Status:          "busy",
		StatusJSON:      []byte(`{"type":"busy"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	for seq := int64(1); seq <= 5; seq++ {
		if _, err := store.InsertOpencodeMirrorEvent(model.OpencodeMirrorEvent{
			NativeSessionID: "ses_mirror_events",
			Seq:             seq,
			Kind:            "message.part.updated",
			UIKind:          "tool_call",
			MessageID:       "msg_recent",
			PayloadJSON:     []byte(`{"ok":true}`),
		}); err != nil {
			t.Fatalf("InsertOpencodeMirrorEvent(%d): %v", seq, err)
		}
	}

	recent, err := store.ListOpencodeMirrorRecentEvents("ses_mirror_events", 3)
	if err != nil {
		t.Fatalf("ListOpencodeMirrorRecentEvents: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("len(recent) = %d, want 3", len(recent))
	}
	for i, want := range []int64{3, 4, 5} {
		if recent[i].Seq != want {
			t.Fatalf("recent[%d].Seq = %d, want %d", i, recent[i].Seq, want)
		}
	}
}

func TestLocalStoreOpencodePermissionRequestsByStatus(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	session, err := store.SaveOpencodeSession(model.OpencodeSession{
		Title:    "Watcher",
		RepoRoot: "/workspace/watcher",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := store.SaveOpencodeTurn(model.OpencodeTurn{
		SessionID:   session.SessionID,
		OperationID: "op_perm",
		Prompt:      "edit",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	if _, err := store.SaveOpencodePermissionRequest(model.OpencodePermissionRequest{
		RequestID:    "ocperm_test",
		TurnID:       turn.TurnID,
		OperationID:  turn.OperationID,
		Kind:         "file_write",
		ResourceJSON: []byte(`{"path":"README.md"}`),
		Status:       "pending",
		ExpiresAt:    model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodePermissionRequest: %v", err)
	}

	requests, err := store.ListOpencodePermissionRequestsByStatus("pending", 10)
	if err != nil {
		t.Fatalf("ListOpencodePermissionRequestsByStatus: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].Kind != "file_write" {
		t.Fatalf("Kind = %q, want file_write", requests[0].Kind)
	}

	got, err := store.GetOpencodePermissionRequest("ocperm_test")
	if err != nil {
		t.Fatalf("GetOpencodePermissionRequest: %v", err)
	}
	if got.TurnID != turn.TurnID {
		t.Fatalf("TurnID = %q, want %q", got.TurnID, turn.TurnID)
	}

	byTurn, err := store.ListOpencodePermissionRequestsByTurn(turn.TurnID, "pending", 10)
	if err != nil {
		t.Fatalf("ListOpencodePermissionRequestsByTurn: %v", err)
	}
	if len(byTurn) != 1 || byTurn[0].RequestID != "ocperm_test" {
		t.Fatalf("ListOpencodePermissionRequestsByTurn = %#v, want ocperm_test", byTurn)
	}
}

func TestLocalStoreOpencodeQuestionRequestsByStatus(t *testing.T) {
	store, err := OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer store.Close()

	session, err := store.SaveOpencodeSession(model.OpencodeSession{
		Title:    "Watcher",
		RepoRoot: "/workspace/watcher",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := store.SaveOpencodeTurn(model.OpencodeTurn{
		SessionID:   session.SessionID,
		OperationID: "op_question",
		Prompt:      "edit",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	if _, err := store.SaveOpencodeQuestionRequest(model.OpencodeQuestionRequest{
		RequestID:       "ocque_test",
		TurnID:          turn.TurnID,
		OperationID:     turn.OperationID,
		NativeSessionID: "ses_question",
		QuestionsJSON:   []byte(`[{"question":"Pick one","options":[{"label":"A","value":"a"}]}]`),
		Status:          "pending",
		ExpiresAt:       model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodeQuestionRequest: %v", err)
	}

	requests, err := store.ListOpencodeQuestionRequestsByStatus("pending", 10)
	if err != nil {
		t.Fatalf("ListOpencodeQuestionRequestsByStatus: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].NativeSessionID != "ses_question" {
		t.Fatalf("NativeSessionID = %q, want ses_question", requests[0].NativeSessionID)
	}

	got, err := store.GetOpencodeQuestionRequest("ocque_test")
	if err != nil {
		t.Fatalf("GetOpencodeQuestionRequest: %v", err)
	}
	if got.TurnID != turn.TurnID {
		t.Fatalf("TurnID = %q, want %q", got.TurnID, turn.TurnID)
	}

	byTurn, err := store.ListOpencodeQuestionRequestsByTurn(turn.TurnID, "pending", 10)
	if err != nil {
		t.Fatalf("ListOpencodeQuestionRequestsByTurn: %v", err)
	}
	if len(byTurn) != 1 || byTurn[0].RequestID != "ocque_test" {
		t.Fatalf("ListOpencodeQuestionRequestsByTurn = %#v, want ocque_test", byTurn)
	}
}
