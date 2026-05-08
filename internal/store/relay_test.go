package store

import (
	"path/filepath"
	"testing"

	"watcher/internal/model"
)

func TestRelayStoreEnvelopeRoundTrip(t *testing.T) {
	store, err := OpenRelay(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("OpenRelay: %v", err)
	}
	defer store.Close()

	cursor, created, err := store.SavePublishedEnvelope(model.EventEnvelope{
		EventID:     "evt_test",
		Stream:      model.EventStreamCodexOperation,
		Kind:        "accepted",
		ResourceID:  "resource_alpha",
		ThreadID:    "thread_123",
		OperationID: "codop_test",
		OccurredAt:  model.NowString(),
	})
	if err != nil {
		t.Fatalf("SavePublishedEnvelope: %v", err)
	}
	if !created {
		t.Fatalf("created = false, want true")
	}
	if cursor <= 0 {
		t.Fatalf("cursor = %d, want > 0", cursor)
	}

	items, nextCursor, err := store.ListEnvelopesSince(0, 10, []string{model.EventStreamCodexOperation}, "resource_alpha", "thread_123", "codop_test", "")
	if err != nil {
		t.Fatalf("ListEnvelopesSince: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if nextCursor != cursor {
		t.Fatalf("nextCursor = %d, want %d", nextCursor, cursor)
	}
	if items[0].Envelope.Kind != "accepted" {
		t.Fatalf("Kind = %q, want accepted", items[0].Envelope.Kind)
	}
	if items[0].Envelope.ResourceID != "resource_alpha" {
		t.Fatalf("ResourceID = %q, want resource_alpha", items[0].Envelope.ResourceID)
	}
}
