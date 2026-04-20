package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JSONHookEvent describes one hook entry to install at hooks.<Event>.
// Matcher is always "" for notifytun — we hook every occurrence.
type JSONHookEvent struct {
	Event   string
	Command string
}

// JSONHooksConfigured reports whether every event's canonical command is
// already present at settingsPath (and no legacy entries would be removed).
func JSONHooksConfigured(settingsPath string, events []JSONHookEvent) bool {
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
	for _, ev := range events {
		entries, ok := hooks[ev.Event].([]any)
		if !ok {
			return false
		}
		if !hasHookCommand(entries, ev.Command) {
			return false
		}
	}
	return true
}

// ApplyJSONHooks installs every event's canonical command and removes any
// existing entry whose command (after trimming leading whitespace) has one
// of stripPrefixes as a prefix. Non-matching entries are preserved.
func ApplyJSONHooks(settingsPath string, events []JSONHookEvent, stripPrefixes []string) error {
	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}
	hooks, err := mapValue(settings["hooks"], "hooks")
	if err != nil {
		return err
	}

	for _, ev := range events {
		entries, err := sliceValue(hooks[ev.Event], "hooks."+ev.Event)
		if err != nil {
			return err
		}
		entries = removeEntriesByCommandPrefix(entries, stripPrefixes)
		if !hasHookCommand(entries, ev.Command) {
			entries = append(entries, newClaudeEntry(ev.Command))
		}
		hooks[ev.Event] = entries
	}
	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func removeEntriesByCommandPrefix(entries []any, prefixes []string) []any {
	if len(prefixes) == 0 {
		return entries
	}
	out := entries[:0:0]
	for _, entry := range entries {
		if entryHasCommandWithPrefix(entry, prefixes) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func entryHasCommandWithPrefix(entry any, prefixes []string) bool {
	entryMap, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entryMap["hooks"].([]any)
	if !ok || len(hooks) == 0 {
		return false
	}
	for _, hook := range hooks {
		hookMap, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hookMap["command"].(string)
		cmd = strings.TrimLeft(cmd, " \t")
		for _, p := range prefixes {
			if strings.HasPrefix(cmd, p) {
				return true
			}
		}
	}
	return false
}
