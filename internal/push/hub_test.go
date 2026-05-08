package push

import "testing"

func TestWSHubUnregisterIgnoresStaleConnection(t *testing.T) {
	cancelled := false
	hub := &WSHub{
		connections: map[string]*wsConn{
			"device-1": {
				id:     2,
				cancel: func() { cancelled = true },
			},
		},
	}

	hub.Unregister("device-1", 1)
	if _, ok := hub.connections["device-1"]; !ok {
		t.Fatalf("stale unregister removed active connection")
	}
	if cancelled {
		t.Fatalf("stale unregister cancelled active connection")
	}

	hub.Unregister("device-1", 2)
	if _, ok := hub.connections["device-1"]; ok {
		t.Fatalf("active unregister did not remove connection")
	}
	if !cancelled {
		t.Fatalf("active unregister did not cancel connection")
	}
}
