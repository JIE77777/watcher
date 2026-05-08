package serverguard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginAllowsMatchingOrigin(t *testing.T) {
	handler := SameOrigin(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://watcher.example.com/login", nil)
	req.Host = "watcher.example.com"
	req.Header.Set("Origin", "https://watcher.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}

func TestSameOriginBlocksCrossSite(t *testing.T) {
	handler := SameOrigin(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://watcher.example.com/login", nil)
	req.Host = "watcher.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
}

func TestSameOriginAllowsLoopbackAliases(t *testing.T) {
	handler := SameOrigin(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/login", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Origin", "http://localhost:8765")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}

func TestSameOriginAllowsTrustedForwardedOrigin(t *testing.T) {
	handler := SameOriginWithTrustedOrigins(false, []string{"*.app.github.dev"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/login", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Origin", "https://watcher-8765.app.github.dev")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}
