package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	claudeStopCommand         = "notifytun emit --tool claude-code --title 'Task complete'"
	claudeNotificationCommand = "notifytun emit --tool claude-code --title 'Needs attention'"
)

// ClaudeConfigurator manages ~/.claude/settings.json hooks.
type ClaudeConfigurator struct{}

func (*ClaudeConfigurator) Name() string       { return "Claude Code" }
func (*ClaudeConfigurator) Binaries() []string { return []string{"claude", "claude-code"} }
func (*ClaudeConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}
func (*ClaudeConfigurator) IsConfigured(home string) bool {
	return IsClaudeConfigured((&ClaudeConfigurator{}).ConfigPath(home))
}
func (*ClaudeConfigurator) PreviewAction(home string) string {
	return "will add Stop + Notification hooks to ~/.claude/settings.json"
}
func (c *ClaudeConfigurator) Apply(home string) error {
	return ApplyClaudeHook(c.ConfigPath(home))
}

// IsClaudeConfigured reports whether both notifytun Claude hooks are already present.
func IsClaudeConfigured(settingsPath string) bool {
	return JSONHooksConfigured(settingsPath, claudeLegacyHookEvents())
}

// ApplyClaudeHook merges notifytun Claude hooks into the given settings file.
func ApplyClaudeHook(settingsPath string) error {
	if IsClaudeConfigured(settingsPath) {
		return nil
	}
	return ApplyJSONHooks(settingsPath, claudeLegacyHookEvents(), nil)
}

func claudeLegacyHookEvents() []JSONHookEvent {
	return []JSONHookEvent{
		{Event: "Stop", Command: claudeStopCommand},
		{Event: "Notification", Command: claudeNotificationCommand},
	}
}

// GenerateClaudeHook returns the JSON snippet notifytun writes into Claude settings.
// Retained for test compatibility.
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
