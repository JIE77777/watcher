package serverguard

import (
	"net/http"
	"sync"
	"time"
)

type MemoryRateLimiter struct {
	limit   int
	window  time.Duration
	mu      sync.Mutex
	entries map[string]*rateEntry
}

type rateEntry struct {
	count int
	reset time.Time
}

func NewMemoryRateLimiter(limit int, window time.Duration) *MemoryRateLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}
	return &MemoryRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*rateEntry),
	}
}

func (l *MemoryRateLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()

	for entryKey, entry := range l.entries {
		if now.After(entry.reset) {
			delete(l.entries, entryKey)
		}
	}

	entry, ok := l.entries[key]
	if !ok || now.After(entry.reset) {
		l.entries[key] = &rateEntry{
			count: 1,
			reset: now.Add(l.window),
		}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}

func (l *MemoryRateLimiter) Middleware(keyFunc func(*http.Request) string) Middleware {
	if l == nil {
		return nil
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "global"
			if keyFunc != nil {
				key = keyFunc(r)
			}
			if !l.Allow(key) {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
