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
