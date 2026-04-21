package notifier_test

import (
	"testing"

	"github.com/michaellee8/notifytun/internal/notifier"
)

func TestNewAutoReturnsBeeepNotifier(t *testing.T) {
	n, err := notifier.New("auto", "")
	if err != nil {
		t.Fatalf("New auto: %v", err)
	}

	if _, ok := n.(*notifier.Beeep); !ok {
		t.Fatalf("expected *notifier.Beeep, got %T", n)
	}
}

func TestNewGenericRequiresCmd(t *testing.T) {
	if _, err := notifier.New("generic", ""); err == nil {
		t.Fatal("expected error when generic backend has no notify-cmd")
	}
}

func TestNewGenericReturnsGenericNotifier(t *testing.T) {
	n, err := notifier.New("generic", "echo")
	if err != nil {
		t.Fatalf("New generic: %v", err)
	}

	if _, ok := n.(*notifier.Generic); !ok {
		t.Fatalf("expected *notifier.Generic, got %T", n)
	}
}

func TestNewRejectsRemovedAndUnknownBackends(t *testing.T) {
	t.Parallel()

	for _, backend := range []string{"macos", "linux", "bogus"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()

			_, err := notifier.New(backend, "")
			if err == nil {
				t.Fatalf("expected error for backend %q", backend)
			}

			want := "unknown backend: " + backend
			if err.Error() != want {
				t.Fatalf("expected %q, got %q", want, err.Error())
			}
		})
	}
}
