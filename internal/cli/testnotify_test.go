package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/cli"
)

func TestTestNotifyPrintsActualGenericCommandOutputAndSuccess(t *testing.T) {
	cmd := cli.NewTestNotifyCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--backend", "generic",
		"--notify-cmd", "printf 'actual notify output'",
	})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := "actual notify output\n" +
		"Test notification sent successfully.\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestTestNotifyReturnsErrorAndSurfacesGenericCommandStderr(t *testing.T) {
	cmd := cli.NewTestNotifyCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--backend", "generic",
		"--notify-cmd", "printf 'generic failed\\n' >&2; exit 7",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "notification failed:") {
		t.Fatalf("expected wrapped notification error, got %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("expected exit status in error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if stderr.String() != "generic failed\n" {
		t.Fatalf("stderr = %q, want %q", stderr.String(), "generic failed\n")
	}
}
