package cli_test

import (
	"bytes"
	"testing"

	"github.com/michaellee8/notifytun/internal/cli"
)

func TestTestNotifySendsGenericNotificationAndPrintsSuccess(t *testing.T) {
	cmd := cli.NewTestNotifyCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--backend", "generic",
		"--notify-cmd", "cat",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := "{\"body\":\"Test notification - if you see this, your backend is working!\",\"title\":\"notifytun\",\"tool\":\"test\"}\n" +
		"Test notification sent successfully.\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}
