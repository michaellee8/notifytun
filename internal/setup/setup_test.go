package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
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
	} else if !tool.Supported {
		t.Fatal("expected Gemini CLI to be detected as supported")
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
