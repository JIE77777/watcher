package securityplane

import (
	"testing"

	"watcher/internal/model"
)

func TestBuildFlagsPublicCleartext(t *testing.T) {
	posture := Build("relay", []EndpointConfig{
		{
			EndpointID:           "relay",
			Role:                 "relay",
			BindAddr:             "0.0.0.0:8780",
			PublicURL:            "http://example.com",
			OwnerTokenConfigured: true,
			AllowedHosts:         []string{"example.com"},
		},
	})
	if got := posture.Endpoints[0].Exposure; got != "public_bind" {
		t.Fatalf("exposure = %q, want public_bind", got)
	}
	assertChecklist(t, posture.Checklist, "public_https", "fail")
	assertChecklist(t, posture.Checklist, "direct_public_bind", "warn")
}

func TestBuildAllowsLocalDevelopment(t *testing.T) {
	posture := Build("service", []EndpointConfig{
		{
			EndpointID:              "service",
			Role:                    "service",
			BindAddr:                "127.0.0.1:8765",
			OwnerTokenConfigured:    true,
			SessionSecretConfigured: boolPtr(true),
			RateLimitPerMinute:      240,
			ServiceProxyConfigured:  true,
			DeviceAuthSupported:     false,
			SecureCookies:           boolPtr(false),
		},
	})
	if got := posture.Endpoints[0].Exposure; got != "local_dev" {
		t.Fatalf("exposure = %q, want local_dev", got)
	}
	assertChecklist(t, posture.Checklist, "public_https", "pass")
}

func TestBuildPassesPublicHTTPSWithHSTS(t *testing.T) {
	posture := Build("relay", []EndpointConfig{
		{
			EndpointID:           "relay",
			Role:                 "relay",
			BindAddr:             "127.0.0.1:8780",
			PublicURL:            "https://watcher.example.com",
			OwnerTokenConfigured: true,
			HSTSEnabled:          true,
			AllowedHosts:         []string{"watcher.example.com"},
			TrustedProxies:       []string{"127.0.0.1/32"},
		},
	})
	assertChecklist(t, posture.Checklist, "public_https", "pass")
	assertChecklist(t, posture.Checklist, "hsts", "pass")
}

func assertChecklist(t *testing.T, items []model.SecurityChecklistItem, id string, status string) {
	t.Helper()
	for _, item := range items {
		if item.ItemID == id {
			if item.Status != status {
				t.Fatalf("%s status = %q, want %q", id, item.Status, status)
			}
			return
		}
	}
	t.Fatalf("checklist item %q not found", id)
}

func boolPtr(value bool) *bool {
	return &value
}
