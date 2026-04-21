package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestRemoteSetupDryRunPrintsPreview(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini", "opencode"))
	t.Setenv("HOME", t.TempDir())

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Detected tools:\n",
		"Claude Code -- will add Stop + Notification hooks to ~/.claude/settings.json",
		"Codex CLI -- will enable codex_hooks in ~/.codex/config.toml and add Stop hook to ~/.codex/hooks.json",
		"Gemini CLI -- will add AfterAgent + Notification hooks to ~/.gemini/settings.json",
		"OpenCode -- will write ~/.config/opencode/plugins/notifytun.js",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected preview to contain %q, got %q", want, out)
		}
	}
	if !strings.Contains(out, "(dry run — no changes applied)") {
		t.Fatalf("expected dry-run note, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRemoteSetupApplyConfiguresAllFourTools(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini", "opencode"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("y\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, name := range []string{"Claude Code", "Codex CLI", "Gemini CLI", "OpenCode"} {
		if !strings.Contains(out, "Configured "+name+" at ") {
			t.Fatalf("expected 'Configured %s at ' in output, got %q", name, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	claude, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(claude): %v", err)
	}
	for _, want := range []string{
		"notifytun emit-hook --tool claude-code --event Stop",
		"notifytun emit-hook --tool claude-code --event Notification",
	} {
		if !strings.Contains(string(claude), want) {
			t.Fatalf("expected Claude settings to contain %q, got %q", want, string(claude))
		}
	}

	if !setup.IsCodexConfigured(home) {
		t.Fatal("expected Codex config to be structurally configured")
	}

	gemini, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(gemini): %v", err)
	}
	for _, want := range []string{
		"notifytun emit-hook --tool gemini --event AfterAgent",
		"notifytun emit-hook --tool gemini --event Notification",
	} {
		if !strings.Contains(string(gemini), want) {
			t.Fatalf("expected Gemini settings to contain %q, got %q", want, string(gemini))
		}
	}

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("Stat(opencode plugin): %v", err)
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected OpenCode plugin to match canonical content")
	}
}

func TestRemoteSetupNothingToConfigureWhenAlreadySetUp(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "opencode"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{
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
		t.Fatalf("WriteFile(claude settings): %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.codex): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(codex legacy config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte(setup.GenerateCodexHookConfig()), 0o644); err != nil {
		t.Fatalf("WriteFile(codex hooks): %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode", "plugins"), 0o755); err != nil {
		t.Fatalf("MkdirAll(opencode plugins): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js"),
		[]byte(setup.GenerateOpenCodePlugin()), 0o644); err != nil {
		t.Fatalf("WriteFile(opencode plugin): %v", err)
	}

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Claude Code -- already configured") {
		t.Fatalf("expected Claude already-configured preview, got %q", out)
	}
	if !strings.Contains(out, "Codex CLI -- will enable codex_hooks in ~/.codex/config.toml and add Stop hook to ~/.codex/hooks.json") {
		t.Fatalf("expected Codex migration preview, got %q", out)
	}
	if !strings.Contains(out, "OpenCode -- already configured") {
		t.Fatalf("expected OpenCode already-configured preview, got %q", out)
	}
	if strings.Contains(out, "Nothing to configure — all supported tools already set up.") {
		t.Fatalf("did not expect nothing-to-configure message during Codex migration, got %q", out)
	}
	if !strings.Contains(out, "Apply? [Y/n] ") {
		t.Fatalf("expected prompt when Codex still needs migration, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRemoteSetupContinuesAfterPerToolFailure(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"hooks":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(claude settings): %v", err)
	}

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("y\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(stderr.String(), "warning: failed to configure Claude Code:") {
		t.Fatalf("expected Claude warning, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Configured Codex CLI at ") {
		t.Fatalf("expected Codex success despite Claude failure, got %q", stdout.String())
	}

	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(codex): %v", err)
	}
	if !setup.IsCodexConfigured(home) {
		t.Fatalf("expected Codex config to be structurally configured, got %q", string(codexConfig))
	}
}

func TestRemoteSetupAbortsOnNo(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("n\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected abort message, got %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("expected Claude settings to remain absent, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected Codex config to remain absent, stat err=%v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRemoteSetupAbortsOnEOF(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(""))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected abort message on EOF, got %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("expected Claude settings to remain absent, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected Codex config to remain absent, stat err=%v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func makeFakePath(t *testing.T, names ...string) string {
	t.Helper()

	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}
	return dir
}
