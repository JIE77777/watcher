package codexbridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionLockerLockWaitsForUnlock(t *testing.T) {
	locker := NewSessionLocker()
	if !locker.TryLock("session-a") {
		t.Fatalf("expected initial TryLock to succeed")
	}

	acquired := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		acquired <- locker.Lock(ctx, "session-a")
	}()

	select {
	case err := <-acquired:
		t.Fatalf("lock acquired before unlock: %v", err)
	case <-time.After(120 * time.Millisecond):
	}

	locker.Unlock("session-a")

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("expected waiter to acquire after unlock, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked locker to acquire")
	}

	locker.Unlock("session-a")
}

func TestSessionLockerLockReturnsContextError(t *testing.T) {
	locker := NewSessionLocker()
	if !locker.TryLock("session-a") {
		t.Fatalf("expected initial TryLock to succeed")
	}
	defer locker.Unlock("session-a")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	err := locker.Lock(ctx, "session-a")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline error, got %v", err)
	}
}
