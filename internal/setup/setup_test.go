package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
	toml "github.com/pelletier/go-toml/v2"
)

func TestDetectToolsFullMatrix(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex", "gemini", "opencode"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	tools := setup.DetectTools(dir)
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d: %+v", len(tools), tools)
	}

	found := map[string]setup.Tool{}
	for _, tool := range tools {
		found[tool.Name] = tool
	}
	if tool, ok := found["Claude Code"]; !ok {
		t.Fatal("expected Claude Code to be detected")
	} else if filepath.Base(tool.Binary) != "claude" {
		t.Fatalf("expected Claude Code to be detected via claude, got %q", tool.Binary)
	}
	if _, ok := found["Codex CLI"]; !ok {
		t.Fatal("expected Codex CLI to be detected")
	}
	if tool, ok := found["Gemini CLI"]; !ok {
		t.Fatal("expected Gemini CLI to be detected")
	} else if tool.Supported {
		t.Fatal("expected Gemini CLI to be detected as unsupported in v1")
	}
	if tool, ok := found["OpenCode"]; !ok {
		t.Fatal("expected OpenCode to be detected")
	} else if tool.Supported {
		t.Fatal("expected OpenCode to be detected as unsupported in v1")
	}
}

func TestDetectToolsClaudeCodeAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-code")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(claude-code): %v", err)
	}

	tools := setup.DetectTools(dir)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d: %+v", len(tools), tools)
	}

	tool := tools[0]
	if tool.Name != "Claude Code" {
		t.Fatalf("expected Claude Code, got %q", tool.Name)
	}
	if filepath.Base(tool.Binary) != "claude-code" {
		t.Fatalf("expected Claude Code to be detected via claude-code, got %q", tool.Binary)
	}
}

func TestDetectToolsInjectedPathRequiresExecutable(t *testing.T) {
	dir := t.TempDir()

	claudePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(claude): %v", err)
	}

	codexPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(codex): %v", err)
	}

	tools := setup.DetectTools(dir)
	if len(tools) != 1 {
		t.Fatalf("expected only executable tools to be detected, got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != "Claude Code" {
		t.Fatalf("expected Claude Code only, got %+v", tools)
	}
}

func TestClaudeHookGeneration(t *testing.T) {
	hook := setup.GenerateClaudeHook()
	if !strings.Contains(hook, `"Stop"`) {
		t.Fatal("expected Stop hook in generated config")
	}
	if !strings.Contains(hook, `"Notification"`) {
		t.Fatal("expected Notification hook in generated config")
	}
	if !strings.Contains(hook, "Task complete") {
		t.Fatal("expected generated hook to emit Task complete notifications")
	}
	if !strings.Contains(hook, "Needs attention") {
		t.Fatal("expected generated hook to emit Claude attention notifications")
	}
}

func TestCodexNotifyGeneration(t *testing.T) {
	cfg := setup.GenerateCodexNotifyConfig()
	if !strings.Contains(cfg, `notify = ["notifytun", "emit", "--tool", "codex"]`) {
		t.Fatal("expected codex notify config to call notifytun emit")
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
            "command": "notifytun emit --tool claude-code --title 'Task complete'"
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
	if strings.Count(content, "notifytun emit --tool claude-code --title 'Task complete'") != 1 {
		t.Fatal("expected exactly one notifytun Stop hook after apply")
	}
}

func TestCodexNotifyIdempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}

	if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(second): %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second codex apply changed the file")
	}
}

func TestIsCodexConfiguredIgnoresTableScopedNotify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[profiles.default]
notify = ["notifytun", "emit", "--tool", "codex"]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if setup.IsCodexConfigured(configPath) {
		t.Fatal("expected table-scoped notify to not count as configured")
	}
}

func TestApplyCodexNotifyConfigInsertsRootNotifyBeforeFirstTable(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
		t.Fatalf("ApplyCodexNotifyConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	cfg := decodeTOML(t, data)
	if got := rootNotifyValue(t, cfg); !equalStrings(got, []string{"notifytun", "emit", "--tool", "codex"}) {
		t.Fatalf("expected root notify command, got %#v", got)
	}
	if profiles, ok := cfg["profiles"].(map[string]any); !ok || profiles == nil {
		t.Fatalf("expected profiles table to remain, got %#v", cfg["profiles"])
	}
}

func TestApplyCodexNotifyConfigReplacesExistingRootNotify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`notify = ["other", "command"]

[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
		t.Fatalf("ApplyCodexNotifyConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	cfg := decodeTOML(t, data)
	if got := rootNotifyValue(t, cfg); !equalStrings(got, []string{"notifytun", "emit", "--tool", "codex"}) {
		t.Fatalf("expected root notify command, got %#v", got)
	}
}

func TestIsCodexConfiguredAcceptsMultilineRootNotify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`notify = [
  "notifytun",
  "emit",
  "--tool",
  "codex",
]

[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !setup.IsCodexConfigured(configPath) {
		t.Fatal("expected multiline root notify to count as configured")
	}
}

func TestApplyCodexNotifyConfigReplacesMultilineRootNotify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(`notify = [
  "old",
  "command",
]

[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
		t.Fatalf("ApplyCodexNotifyConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), `"old"`) {
		t.Fatalf("expected old multiline notify to be removed, got %q", string(data))
	}
	cfg := decodeTOML(t, data)
	if got := rootNotifyValue(t, cfg); !equalStrings(got, []string{"notifytun", "emit", "--tool", "codex"}) {
		t.Fatalf("expected root notify command, got %#v", got)
	}
}

func decodeTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("toml.Unmarshal: %v\ncontent:\n%s", err, string(data))
	}
	return cfg
}

func rootNotifyValue(t *testing.T, cfg map[string]any) []string {
	t.Helper()

	raw, ok := cfg["notify"]
	if !ok {
		t.Fatal("expected root notify key")
	}
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected root notify array, got %#v", raw)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		s, ok := value.(string)
		if !ok {
			t.Fatalf("expected notify string element, got %#v", value)
		}
		out = append(out, s)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
