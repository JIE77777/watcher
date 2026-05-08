package codexbridge

import (
	"context"
	"sync"
)

type SessionLocker struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func NewSessionLocker() *SessionLocker {
	return &SessionLocker{
		locks: make(map[string]chan struct{}),
	}
}

func (l *SessionLocker) TryLock(sessionID string) bool {
	select {
	case <-l.channel(sessionID):
		return true
	default:
		return false
	}
}

func (l *SessionLocker) Lock(ctx context.Context, sessionID string) error {
	ch := l.channel(sessionID)
	if ctx == nil {
		<-ch
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *SessionLocker) Unlock(sessionID string) {
	select {
	case l.channel(sessionID) <- struct{}{}:
	default:
	}
}

func (l *SessionLocker) channel(sessionID string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ch, ok := l.locks[sessionID]; ok {
		return ch
	}

	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	l.locks[sessionID] = ch
	return ch
}
