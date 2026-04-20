package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
	toml "github.com/pelletier/go-toml/v2"
)

func TestCodexNotifyGeneration(t *testing.T) {
	cfg := setup.GenerateCodexNotifyConfig()
	if !strings.Contains(cfg, `notify = ["notifytun", "emit", "--tool", "codex"]`) {
		t.Fatal("expected codex notify config to call notifytun emit")
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
