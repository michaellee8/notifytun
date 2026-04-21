package notifier

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"testing"

	"github.com/gen2brain/beeep"
)

func TestBeeepNotifyForwardsNotification(t *testing.T) {
	originalNotify := beeepNotify
	originalAppName := beeep.AppName
	t.Cleanup(func() {
		beeepNotify = originalNotify
		beeep.AppName = originalAppName
	})

	var gotTitle string
	var gotBody string

	beeepNotify = func(title, body string, icon any) error {
		gotTitle = title
		gotBody = body
		return nil
	}

	err := NewBeeep().Notify(context.Background(), Notification{
		Title: "Task Complete",
		Body:  "notifytun finished",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if gotTitle != "Task Complete" {
		t.Fatalf("expected title %q, got %q", "Task Complete", gotTitle)
	}
	if gotBody != "notifytun finished" {
		t.Fatalf("expected body %q, got %q", "notifytun finished", gotBody)
	}
	if beeep.AppName != "notifytun" {
		t.Fatalf("expected AppName %q, got %q", "notifytun", beeep.AppName)
	}
}

func TestBeeepNotifyUsesEmbeddedPNGIcon(t *testing.T) {
	originalNotify := beeepNotify
	originalAppName := beeep.AppName
	t.Cleanup(func() {
		beeepNotify = originalNotify
		beeep.AppName = originalAppName
	})

	var gotIcon any

	beeepNotify = func(title, body string, icon any) error {
		gotIcon = icon
		return nil
	}

	err := NewBeeep().Notify(context.Background(), Notification{
		Title: "Task Complete",
		Body:  "notifytun finished",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	iconBytes, ok := gotIcon.([]byte)
	if !ok {
		t.Fatalf("expected []byte icon payload, got %T (%#v)", gotIcon, gotIcon)
	}
	if len(iconBytes) == 0 {
		t.Fatal("expected non-empty icon payload")
	}

	wantPNGHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.HasPrefix(iconBytes, wantPNGHeader) {
		t.Fatalf("expected PNG icon payload, got %x", iconBytes)
	}
	if _, err := png.Decode(bytes.NewReader(iconBytes)); err != nil {
		t.Fatalf("expected decodable PNG icon payload, got %v", err)
	}
}

func TestBeeepNotifyReturnsUnderlyingError(t *testing.T) {
	originalNotify := beeepNotify
	originalAppName := beeep.AppName
	t.Cleanup(func() {
		beeepNotify = originalNotify
		beeep.AppName = originalAppName
	})

	wantErr := errors.New("beeep failed")
	beeepNotify = func(title, body string, icon any) error {
		return wantErr
	}

	err := NewBeeep().Notify(context.Background(), Notification{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected errors.Is(err, wantErr), got %v", err)
	}
}

func TestBeeepNotifyCanceledContextSkipsNotify(t *testing.T) {
	originalNotify := beeepNotify
	originalAppName := beeep.AppName
	t.Cleanup(func() {
		beeepNotify = originalNotify
		beeep.AppName = originalAppName
	})

	called := false
	beeepNotify = func(title, body string, icon any) error {
		called = true
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewBeeep().Notify(ctx, Notification{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
	}
	if called {
		t.Fatal("expected beeepNotify not to be called")
	}
}
