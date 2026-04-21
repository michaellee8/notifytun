package notifier

import (
	"context"
	"errors"
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
	var gotIcon any

	beeepNotify = func(title, body string, icon any) error {
		gotTitle = title
		gotBody = body
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

	if gotTitle != "Task Complete" {
		t.Fatalf("expected title %q, got %q", "Task Complete", gotTitle)
	}
	if gotBody != "notifytun finished" {
		t.Fatalf("expected body %q, got %q", "notifytun finished", gotBody)
	}
	if gotIcon != "" {
		t.Fatalf("expected empty icon, got %#v", gotIcon)
	}
	if beeep.AppName != "notifytun" {
		t.Fatalf("expected AppName %q, got %q", "notifytun", beeep.AppName)
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
