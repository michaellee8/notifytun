package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyJSONHooksCreatesFreshFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
		{Event: "Notification", Command: "notifytun emit-hook --tool claude-code --event Notification"},
	}

	if err := ApplyJSONHooks(settingsPath, events, nil); err != nil {
		t.Fatalf("ApplyJSONHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, ev := range events {
		if !strings.Contains(string(data), ev.Command) {
			t.Fatalf("expected command %q in settings, got %q", ev.Command, string(data))
		}
	}
}

func TestApplyJSONHooksRemovesLegacyByPrefix(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {"matcher":"","hooks":[{"type":"command","command":"notifytun emit --tool claude-code --title 'Task complete'"}]},
      {"matcher":"","hooks":[{"type":"command","command":"echo unrelated"}]}
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
	}
	prefixes := []string{"notifytun emit ", "notifytun emit-hook "}

	if err := ApplyJSONHooks(settingsPath, events, prefixes); err != nil {
		t.Fatalf("ApplyJSONHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "'Task complete'") {
		t.Fatalf("expected legacy notifytun emit entry to be removed, got %q", content)
	}
	if !strings.Contains(content, "echo unrelated") {
		t.Fatalf("expected unrelated hook to be preserved, got %q", content)
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatalf("expected exactly one canonical hook, got %q", content)
	}
}

func TestApplyJSONHooksIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "AfterAgent", Command: "notifytun emit-hook --tool gemini --event AfterAgent"},
	}

	if err := ApplyJSONHooks(settingsPath, events, []string{"notifytun emit-hook "}); err != nil {
		t.Fatalf("first: %v", err)
	}
	first, _ := os.ReadFile(settingsPath)
	if err := ApplyJSONHooks(settingsPath, events, []string{"notifytun emit-hook "}); err != nil {
		t.Fatalf("second: %v", err)
	}
	second, _ := os.ReadFile(settingsPath)
	if string(first) != string(second) {
		t.Fatalf("second apply changed the file:\nfirst=%q\nsecond=%q", first, second)
	}
}

func TestJSONHooksConfigured(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
	}

	if JSONHooksConfigured(settingsPath, events) {
		t.Fatal("empty file should not be configured")
	}
	if err := ApplyJSONHooks(settingsPath, events, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !JSONHooksConfigured(settingsPath, events) {
		t.Fatal("expected configured after apply")
	}
}
