package notifier_test

import (
	"context"
	"os/exec"
	"reflect"
	"runtime"
	"testing"

	"github.com/michaellee8/notifytun/internal/notifier"
)

func TestMacOSCommandArgs(t *testing.T) {
	n := notifier.NewMacOS()
	cmd := n.BuildCommand(context.Background(), "Test Title", "Test Body")

	if cmd.Path == "" {
		t.Fatal("expected osascript path")
	}

	found := false
	for _, arg := range cmd.Args {
		if arg == "-e" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected -e flag in args: %v", cmd.Args)
	}
}

func TestLinuxCommandArgs(t *testing.T) {
	n := notifier.NewLinux()
	cmd := n.BuildCommand(context.Background(), "Test Title", "Test Body")

	if len(cmd.Args) == 0 || cmd.Args[0] != "notify-send" {
		t.Fatalf("expected notify-send command, got %v", cmd.Args)
	}

	foundApp := false
	for i, arg := range cmd.Args {
		if arg == "-a" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "notifytun" {
			foundApp = true
			break
		}
	}
	if !foundApp {
		t.Fatalf("expected -a notifytun in args: %v", cmd.Args)
	}
}

func TestGenericNotifier(t *testing.T) {
	n, err := notifier.NewGeneric("echo")
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}

	err = n.Notify(context.Background(), notifier.Notification{
		Title: "Test",
		Body:  "Body",
		Tool:  "test",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
}

func TestNewAutoDetectsOS(t *testing.T) {
	notifyCmd := ""
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("notify-send"); err != nil {
			notifyCmd = "printf ok"
		}
	}

	n, err := notifier.New("auto", notifyCmd)
	if err != nil {
		t.Fatalf("New auto: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil notifier from auto")
	}
}

func TestNewGenericRequiresCmd(t *testing.T) {
	if _, err := notifier.New("generic", ""); err == nil {
		t.Fatal("expected error when generic backend has no notify-cmd")
	}
}

func TestNewAutoOnLinuxFallsBackToGenericWhenNotifySendIsMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only behavior")
	}

	t.Setenv("PATH", t.TempDir())

	if _, err := notifier.New("auto", ""); err == nil {
		t.Fatal("expected error when auto fallback has no notify-cmd")
	}

	n, err := notifier.New("auto", "printf ok")
	if err != nil {
		t.Fatalf("New auto with notify-cmd: %v", err)
	}
	if got := reflect.TypeOf(n).String(); got != "*notifier.Generic" {
		t.Fatalf("expected generic fallback, got %s", got)
	}
}
