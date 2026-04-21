package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
	toml "github.com/pelletier/go-toml/v2"
)

func TestCodexHookGeneration(t *testing.T) {
	hooks := setup.GenerateCodexHookConfig()
	if !strings.Contains(hooks, `"Stop"`) {
		t.Fatalf("expected Codex hooks to define Stop, got %q", hooks)
	}
	if !strings.Contains(hooks, `notifytun emit-hook --tool codex --event Stop`) {
		t.Fatalf("expected Codex hooks to call emit-hook Stop, got %q", hooks)
	}
}

func TestCodexConfigGenerationEnablesHooks(t *testing.T) {
	cfg := setup.GenerateCodexConfig()
	if !strings.Contains(cfg, "[features]") {
		t.Fatalf("expected features table, got %q", cfg)
	}
	if !strings.Contains(cfg, `codex_hooks = true`) {
		t.Fatalf("expected codex_hooks enabled, got %q", cfg)
	}
	if strings.Contains(cfg, "notify =") {
		t.Fatalf("did not expect root notify config, got %q", cfg)
	}
}

func TestApplyCodexConfigIdempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	hooksPath := filepath.Join(dir, "hooks.json")

	if err := setup.ApplyCodexConfig(configPath, hooksPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}
	firstHooks, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("ReadFile(first hooks): %v", err)
	}

	if err := setup.ApplyCodexConfig(configPath, hooksPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(second): %v", err)
	}
	secondHooks, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("ReadFile(second hooks): %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second codex apply changed the file")
	}
	if string(firstHooks) != string(secondHooks) {
		t.Fatal("second codex apply changed the hooks file")
	}
}

func TestApplyCodexConfigEnablesHooksAndPreservesTables(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	hooksPath := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(configPath, []byte(`[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyCodexConfig(configPath, hooksPath); err != nil {
		t.Fatalf("ApplyCodexConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	cfg := decodeTOML(t, data)
	features, ok := cfg["features"].(map[string]any)
	if !ok || features == nil {
		t.Fatalf("expected features table, got %#v", cfg["features"])
	}
	if got, ok := features["codex_hooks"].(bool); !ok || !got {
		t.Fatalf("expected codex_hooks=true, got %#v", features["codex_hooks"])
	}
	if _, ok := cfg["notify"]; ok {
		t.Fatalf("did not expect root notify after apply, got %#v", cfg["notify"])
	}
	if profiles, ok := cfg["profiles"].(map[string]any); !ok || profiles == nil {
		t.Fatalf("expected profiles table to remain, got %#v", cfg["profiles"])
	}
	if !setup.IsCodexHookConfigured(hooksPath) {
		t.Fatalf("expected hooks file to be structurally configured")
	}
}

func TestApplyCodexConfigReplacesExistingRootNotify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	hooksPath := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(configPath, []byte(`notify = ["other", "command"]

[profiles.default]
model = "gpt-5"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyCodexConfig(configPath, hooksPath); err != nil {
		t.Fatalf("ApplyCodexConfig: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), `"other"`) {
		t.Fatalf("expected old notify to be removed, got %q", string(data))
	}
	cfg := decodeTOML(t, data)
	features, ok := cfg["features"].(map[string]any)
	if !ok || features == nil {
		t.Fatalf("expected features table, got %#v", cfg["features"])
	}
	if got, ok := features["codex_hooks"].(bool); !ok || !got {
		t.Fatalf("expected codex_hooks=true, got %#v", features["codex_hooks"])
	}
	if _, ok := cfg["notify"]; ok {
		t.Fatalf("did not expect root notify after apply, got %#v", cfg["notify"])
	}
}

func TestIsCodexConfiguredRequiresHooksAndFeature(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	configPath := filepath.Join(home, ".codex", "config.toml")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(setup.GenerateCodexConfig()), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if err := os.WriteFile(hooksPath, []byte(setup.GenerateCodexHookConfig()), 0o644); err != nil {
		t.Fatalf("WriteFile(hooks): %v", err)
	}

	if !setup.IsCodexConfigured(home) {
		t.Fatal("expected Codex hooks integration to count as configured")
	}
}

func TestIsCodexConfiguredRejectsLegacyNotifyOnly(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	if setup.IsCodexConfigured(home) {
		t.Fatal("expected legacy notify-only config to not count as configured")
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
