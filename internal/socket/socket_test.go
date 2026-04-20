package socket_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/socket"
)

type delayedDeadlineContext struct {
	deadline time.Time
	done     chan struct{}
}

func newDelayedDeadlineContext(deadline time.Time, doneDelay time.Duration) *delayedDeadlineContext {
	ctx := &delayedDeadlineContext{
		deadline: deadline,
		done:     make(chan struct{}),
	}

	time.AfterFunc(time.Until(deadline)+doneDelay, func() {
		close(ctx.done)
	})

	return ctx
}

func (c *delayedDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func (c *delayedDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *delayedDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *delayedDeadlineContext) Value(any) any {
	return nil
}

func TestListenAndWakeup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := socket.SendWakeup(path); err != nil {
			t.Errorf("SendWakeup: %v", err)
		}
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := listener.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	<-done
}

func TestWaitTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = listener.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestWaitDeadlineExceededBeforeContextErrorVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	ctx := newDelayedDeadlineContext(time.Now().Add(20*time.Millisecond), 200*time.Millisecond)

	err = listener.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestSendWakeupNoListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.sock")

	if err := socket.SendWakeup(path); err != nil {
		t.Fatalf("SendWakeup should ignore missing listener, got %v", err)
	}
}

func TestCloseRemovesSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := socket.SendWakeup(path); err != nil {
		t.Fatalf("SendWakeup after Close should still be best-effort, got %v", err)
	}
}

func TestWaitReturnsPromptlyOnParentCancelEvenWithDeadline(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "wait.sock")
	listener, err := socket.Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Caller-style: wrap parent with a deadline far in the future.
	ctx, ctxCancel := context.WithTimeout(parent, 10*time.Second)
	defer ctxCancel()

	done := make(chan error, 1)
	go func() { done <- listener.Wait(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel() // cancel parent, not the deadline'd child

	start := time.Now()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Wait took %s after parent cancel, want <1s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return within 2s of parent cancel")
	}
}

func TestStaleSocketRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen 1: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	reopened, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen 2: %v", err)
	}
	defer reopened.Close()
}
