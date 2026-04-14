package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteSetupDryRunPrintsPreview(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini"))
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
	if !strings.Contains(out, "Detected tools:\n") {
		t.Fatalf("expected preview header, got %q", out)
	}
	if !strings.Contains(out, "Claude Code -- will add Stop + Notification hooks to ~/.claude/settings.json") {
		t.Fatalf("expected Claude preview, got %q", out)
	}
	if !strings.Contains(out, "Codex CLI -- will set notify in ~/.codex/config.toml") {
		t.Fatalf("expected Codex preview, got %q", out)
	}
	if !strings.Contains(out, "Gemini CLI -- detected but hook setup not supported in v1") {
		t.Fatalf("expected Gemini unsupported preview, got %q", out)
	}
	if !strings.Contains(out, "(dry run - no changes applied)") && !strings.Contains(out, "(dry run — no changes applied)") {
		t.Fatalf("expected dry-run note, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRemoteSetupApplyConfiguresSupportedTools(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex"))
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
	if !strings.Contains(out, "Apply? [Y/n] ") {
		t.Fatalf("expected confirmation prompt, got %q", out)
	}
	if !strings.Contains(out, "Configured Claude Code hooks in") {
		t.Fatalf("expected Claude success message, got %q", out)
	}
	if !strings.Contains(out, "Configured Codex CLI notify in") {
		t.Fatalf("expected Codex success message, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	claudeSettings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(claude): %v", err)
	}
	if !strings.Contains(string(claudeSettings), "notifytun emit --tool claude-code --title 'Task complete'") {
		t.Fatalf("expected Claude settings to contain notifytun hook, got %q", string(claudeSettings))
	}

	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(codex): %v", err)
	}
	if !strings.Contains(string(codexConfig), `notify = ["notifytun", "emit", "--tool", "codex"]`) {
		t.Fatalf("expected Codex config to contain notify line, got %q", string(codexConfig))
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
		t.Fatalf("WriteFile(claude settings): %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.codex): %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`notify = ["notifytun", "emit", "--tool", "codex"]`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(codex config): %v", err)
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
	if !strings.Contains(out, "Codex CLI -- already configured") {
		t.Fatalf("expected Codex already-configured preview, got %q", out)
	}
	if !strings.Contains(out, "OpenCode -- detected but hook setup not supported in v1") {
		t.Fatalf("expected unsupported preview, got %q", out)
	}
	if !strings.Contains(out, "Nothing to configure — all supported tools already set up.") {
		t.Fatalf("expected nothing-to-configure message, got %q", out)
	}
	if strings.Contains(out, "Apply? [Y/n] ") {
		t.Fatalf("did not expect prompt when nothing needs configuration, got %q", out)
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
	if !strings.Contains(stdout.String(), "Configured Codex CLI notify in") {
		t.Fatalf("expected Codex success despite Claude failure, got %q", stdout.String())
	}

	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(codex): %v", err)
	}
	if !strings.Contains(string(codexConfig), `notify = ["notifytun", "emit", "--tool", "codex"]`) {
		t.Fatalf("expected Codex config to contain notify line, got %q", string(codexConfig))
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
