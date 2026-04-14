package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	claudeStopCommand         = "notifytun emit --tool claude-code --title 'Task complete'"
	claudeNotificationCommand = "notifytun emit --tool claude-code --title 'Needs attention'"
	codexNotifyConfigLine     = `notify = ["notifytun", "emit", "--tool", "codex"]`
)

// Tool represents a detected AI coding tool and whether notifytun can configure it.
type Tool struct {
	Name       string
	Binary     string
	Detected   bool
	Configured bool
	Supported  bool
}

var knownTools = []struct {
	Name      string
	Binaries  []string
	Supported bool
}{
	{Name: "Claude Code", Binaries: []string{"claude", "claude-code"}, Supported: true},
	{Name: "Codex CLI", Binaries: []string{"codex"}, Supported: true},
	{Name: "Gemini CLI", Binaries: []string{"gemini"}, Supported: false},
	{Name: "OpenCode", Binaries: []string{"opencode"}, Supported: false},
}

// DetectTools scans the provided path list or the current PATH when pathEnv is empty.
func DetectTools(pathEnv string) []Tool {
	var tools []Tool

	for _, known := range knownTools {
		tool := Tool{
			Name:      known.Name,
			Supported: known.Supported,
		}

		for _, binary := range known.Binaries {
			if path := lookPath(binary, pathEnv); path != "" {
				tool.Binary = path
				tool.Detected = true
				break
			}
		}

		if tool.Detected {
			tools = append(tools, tool)
		}
	}

	return tools
}

func lookPath(binary, pathEnv string) string {
	if pathEnv == "" {
		path, _ := exec.LookPath(binary)
		return path
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, binary)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate
	}

	return ""
}

// GenerateClaudeHook returns the JSON snippet notifytun writes into Claude settings.
func GenerateClaudeHook() string {
	return `{
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
}`
}

// GenerateCodexNotifyConfig returns the notify config line for Codex CLI.
func GenerateCodexNotifyConfig() string {
	return codexNotifyConfigLine + "\n"
}

// IsClaudeConfigured reports whether both notifytun Claude hooks are already present.
func IsClaudeConfigured(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}

	stopEntries, ok := hooks["Stop"].([]any)
	if !ok {
		return false
	}
	notificationEntries, ok := hooks["Notification"].([]any)
	if !ok {
		return false
	}

	return hasHookCommand(stopEntries, claudeStopCommand) &&
		hasHookCommand(notificationEntries, claudeNotificationCommand)
}

// ApplyClaudeHook merges notifytun Claude hooks into the given settings file.
func ApplyClaudeHook(settingsPath string) error {
	if IsClaudeConfigured(settingsPath) {
		return nil
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	hooks, err := mapValue(settings["hooks"], "hooks")
	if err != nil {
		return err
	}

	stopEntries, err := sliceValue(hooks["Stop"], "hooks.Stop")
	if err != nil {
		return err
	}
	notificationEntries, err := sliceValue(hooks["Notification"], "hooks.Notification")
	if err != nil {
		return err
	}

	if !hasHookCommand(stopEntries, claudeStopCommand) {
		stopEntries = append(stopEntries, newClaudeEntry(claudeStopCommand))
	}
	if !hasHookCommand(notificationEntries, claudeNotificationCommand) {
		notificationEntries = append(notificationEntries, newClaudeEntry(claudeNotificationCommand))
	}

	hooks["Stop"] = stopEntries
	hooks["Notification"] = notificationEntries
	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create Claude settings dir: %w", err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Claude settings: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write Claude settings: %w", err)
	}
	return nil
}

func readSettings(settingsPath string) (map[string]any, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read Claude settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse Claude settings: %w", err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func mapValue(value any, field string) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected %s format: want object", field)
	}
	return m, nil
}

func sliceValue(value any, field string) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected %s format: want array", field)
	}
	return s, nil
}

func newClaudeEntry(command string) map[string]any {
	return map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	}
}

func hasHookCommand(entries []any, want string) bool {
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hook := range hooks {
			hookMap, ok := hook.(map[string]any)
			if !ok {
				continue
			}
			if command, _ := hookMap["command"].(string); command == want {
				return true
			}
		}
	}
	return false
}

// IsCodexConfigured reports whether the notifytun notify hook is already present.
func IsCodexConfigured(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), codexNotifyConfigLine)
}

// ApplyCodexNotifyConfig appends the notifytun notify line when absent.
func ApplyCodexNotifyConfig(configPath string) error {
	if IsCodexConfigured(configPath) {
		return nil
	}

	data, err := os.ReadFile(configPath)
	existing := ""
	if err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Codex config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create Codex config dir: %w", err)
	}

	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	existing += GenerateCodexNotifyConfig()

	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

// Preview summarizes what remote-setup would do for detected tools.
func Preview(tools []Tool) string {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		switch {
		case tool.Configured:
			sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
		case tool.Supported && tool.Name == "Claude Code":
			sb.WriteString("  * Claude Code -- will add Stop + Notification hooks to ~/.claude/settings.json\n")
		case tool.Supported && tool.Name == "Codex CLI":
			sb.WriteString("  * Codex CLI -- will set notify in ~/.codex/config.toml\n")
		default:
			sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
		}
	}
	return sb.String()
}
