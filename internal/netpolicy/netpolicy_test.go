package netpolicy

import "testing"

func TestStripProxyEnv(t *testing.T) {
	input := []string{
		"HTTP_PROXY=http://127.0.0.1:8118",
		"https_proxy=http://127.0.0.1:8118",
		"NO_PROXY=localhost",
		"PATH=/usr/bin",
		"KEEP=value",
	}
	got := StripProxyEnv(input)
	for _, item := range got {
		if item == "PATH=/usr/bin" || item == "KEEP=value" {
			continue
		}
		t.Fatalf("unexpected env retained: %q", item)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 env vars after stripping, got %d", len(got))
	}
}
