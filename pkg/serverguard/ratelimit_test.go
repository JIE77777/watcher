package serverguard

import (
	"testing"
	"time"
)

func TestMemoryRateLimiter(t *testing.T) {
	limiter := NewMemoryRateLimiter(2, time.Minute)
	if !limiter.Allow("ip") {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("ip") {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow("ip") {
		t.Fatal("third request should be limited")
	}
}
