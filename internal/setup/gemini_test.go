package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestGeminiHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("not idempotent")
	}
}

func TestGeminiHookIncludesAfterAgentAndNotification(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("ApplyGeminiHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "notifytun emit-hook --tool gemini --event AfterAgent") {
		t.Fatalf("missing AfterAgent hook: %q", content)
	}
	if !strings.Contains(content, "notifytun emit-hook --tool gemini --event Notification") {
		t.Fatalf("missing Notification hook: %q", content)
	}
}

func TestIsGeminiConfiguredOnCanonicalFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if setup.IsGeminiConfigured(settingsPath) {
		t.Fatal("empty file should not be configured")
	}
	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !setup.IsGeminiConfigured(settingsPath) {
		t.Fatal("expected configured after Apply")
	}
}

func TestApplyGeminiHookPreservesUnrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "AfterAgent": [
      {
        "matcher": "existing",
        "hooks": [
          {"type": "command", "command": "echo other"}
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	content := string(data)
	if !strings.Contains(content, "echo other") {
		t.Fatal("expected unrelated hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool gemini --event AfterAgent") != 1 {
		t.Fatalf("expected exactly one AfterAgent entry, got %q", content)
	}
}
