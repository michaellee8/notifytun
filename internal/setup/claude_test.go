package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestClaudeHookGeneration(t *testing.T) {
	hook := setup.GenerateClaudeHook()
	if !strings.Contains(hook, `"Stop"`) {
		t.Fatal("expected Stop hook in generated config")
	}
	if !strings.Contains(hook, `"Notification"`) {
		t.Fatal("expected Notification hook in generated config")
	}
	if !strings.Contains(hook, "notifytun emit-hook --tool claude-code --event Stop") {
		t.Fatal("expected emit-hook Stop command")
	}
	if !strings.Contains(hook, "notifytun emit-hook --tool claude-code --event Notification") {
		t.Fatal("expected emit-hook Notification command")
	}
}

func TestClaudeHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(second): %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second apply changed the file - not idempotent")
	}
}

func TestDetectAlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit-hook --tool claude-code --event Stop"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit-hook --tool claude-code --event Notification"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !setup.IsClaudeConfigured(settingsPath) {
		t.Fatal("expected Claude to be detected as already configured")
	}
}

func TestApplyClaudeHookPreservesExistingStopHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "existing",
        "hooks": [
          {
            "type": "command",
            "command": "echo existing"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("ApplyClaudeHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "echo existing") {
		t.Fatal("expected existing Stop hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatal("expected exactly one notifytun Stop hook after apply")
	}
}

func TestApplyClaudeHookMigratesLegacyEmitEntries(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit --tool claude-code --title 'Task complete'"
          }
        ]
      },
      {
        "matcher": "preserve",
        "hooks": [
          {
            "type": "command",
            "command": "echo keep-me"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit --tool claude-code --title 'Needs attention'"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("ApplyClaudeHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "'Task complete'") || strings.Contains(content, "'Needs attention'") {
		t.Fatalf("expected legacy notifytun emit entries to be removed, got %q", content)
	}
	if !strings.Contains(content, "echo keep-me") {
		t.Fatal("expected unrelated hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatalf("expected exactly one emit-hook Stop entry, got %q", content)
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Notification") != 1 {
		t.Fatalf("expected exactly one emit-hook Notification entry, got %q", content)
	}
}
