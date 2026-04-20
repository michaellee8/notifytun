package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestOpenCodePluginIdempotent(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second apply changed content")
	}
}

func TestOpenCodePluginContentContainsKeyBits(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(pluginPath)
	content := string(data)

	for _, want := range []string{
		`export const NotifytunPlugin`,
		`"session.idle"`,
		`notifytun emit-hook --tool opencode --event session.idle`,
		`client.session.messages`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected plugin to contain %q, got %q", want, content)
		}
	}
}

func TestIsOpenCodeConfiguredCanonical(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("absent file should not be configured")
	}
	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected configured after Apply")
	}
}

func TestApplyOpenCodePluginOverwritesModifiedContent(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")
	if err := os.WriteFile(pluginPath, []byte("// user edits\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(pluginPath)
	if strings.Contains(string(data), "user edits") {
		t.Fatal("expected user edits to be overwritten with canonical content")
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected configured after Apply-over-existing")
	}
}

func TestApplyOpenCodePluginCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "deep", "nested", "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}
