package ssh_test

import (
	"testing"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
)

func TestBackoffSequence(t *testing.T) {
	b := tunnelssh.NewBackoff()
	expected := []int{1, 2, 4, 8, 16, 30, 30, 30}

	for i, want := range expected {
		if got := int(b.Next().Seconds()); got != want {
			t.Fatalf("attempt %d: expected %ds, got %ds", i+1, want, got)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := tunnelssh.NewBackoff()
	_ = b.Next()
	_ = b.Next()
	_ = b.Next()
	b.Reset()

	if got := int(b.Next().Seconds()); got != 1 {
		t.Fatalf("expected reset backoff to 1s, got %ds", got)
	}
}
