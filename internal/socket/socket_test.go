package socket_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/socket"
)

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
