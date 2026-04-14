package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	claudeStopCommand         = "notifytun emit --tool claude-code --title 'Task complete'"
	claudeNotificationCommand = "notifytun emit --tool claude-code --title 'Needs attention'"
	codexNotifyConfigLine     = `notify = ["notifytun", "emit", "--tool", "codex"]`
)

var codexNotifyCommand = []string{"notifytun", "emit", "--tool", "codex"}

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
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
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

	notifyArgs, count := findRootNotifyAssignments(string(data))
	return count == 1 && equalStringSlices(notifyArgs, codexNotifyCommand)
}

// ApplyCodexNotifyConfig writes the notifytun notify config at the TOML root.
func ApplyCodexNotifyConfig(configPath string) error {
	data, err := os.ReadFile(configPath)
	existing := ""
	if err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Codex config: %w", err)
	}

	updated := upsertRootNotify(existing)
	if existing == updated {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create Codex config dir: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

func findRootNotifyAssignments(content string) ([]string, int) {
	rootLines, _ := splitTOMLRoot(content)

	count := 0
	var notifyArgs []string
	for _, line := range rootLines {
		key, value, ok := parseBareAssignment(line)
		if !ok || key != "notify" {
			continue
		}

		count++
		if count != 1 {
			continue
		}

		args, ok := parseStringArray(value)
		if ok {
			notifyArgs = args
		}
	}

	return notifyArgs, count
}

func upsertRootNotify(content string) string {
	rootLines, bodyLines := splitTOMLRoot(content)
	rootLines = replaceRootNotify(rootLines)
	return joinTOML(rootLines, bodyLines)
}

func replaceRootNotify(rootLines []string) []string {
	rootLines = trimTrailingBlankLines(rootLines)

	var updated []string
	replaced := false
	for _, line := range rootLines {
		key, _, ok := parseBareAssignment(line)
		if ok && key == "notify" {
			if !replaced {
				updated = append(updated, codexNotifyConfigLine)
				replaced = true
			}
			continue
		}
		updated = append(updated, line)
	}

	if !replaced {
		updated = append(updated, codexNotifyConfigLine)
	}
	return updated
}

func splitTOMLRoot(content string) ([]string, []string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	if normalized == "" {
		return nil, nil
	}

	lines := strings.Split(normalized, "\n")
	firstTable := len(lines)
	for i, line := range lines {
		if isTOMLTableHeader(line) {
			firstTable = i
			break
		}
	}

	rootLines := append([]string(nil), lines[:firstTable]...)
	bodyLines := append([]string(nil), lines[firstTable:]...)
	return rootLines, bodyLines
}

func joinTOML(rootLines, bodyLines []string) string {
	rootLines = trimTrailingBlankLines(rootLines)

	var lines []string
	lines = append(lines, rootLines...)
	if len(rootLines) > 0 && len(bodyLines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, bodyLines...)

	if len(lines) == 0 {
		return GenerateCodexNotifyConfig()
	}
	return strings.Join(lines, "\n") + "\n"
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string(nil), lines[:end]...)
}

func isTOMLTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	return strings.HasPrefix(trimmed, "[")
}

func parseBareAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(stripTOMLComment(line))
	if trimmed == "" {
		return "", "", false
	}

	equals := strings.Index(trimmed, "=")
	if equals < 0 {
		return "", "", false
	}

	key := strings.TrimSpace(trimmed[:equals])
	if key == "" || strings.ContainsAny(key, ".\"'[]") {
		return "", "", false
	}

	value := strings.TrimSpace(trimmed[equals+1:])
	return key, value, true
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case r == '#' && !inString:
			return line[:i]
		}
	}
	return line
}

func parseStringArray(value string) ([]string, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return nil, false
	}

	inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if inner == "" {
		return []string{}, true
	}

	var values []string
	for i := 0; i < len(inner); {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t' || inner[i] == '\n' || inner[i] == ',') {
			i++
		}
		if i >= len(inner) {
			break
		}
		if inner[i] != '"' {
			return nil, false
		}

		j := i + 1
		escaped := false
		for j < len(inner) {
			switch {
			case escaped:
				escaped = false
			case inner[j] == '\\':
				escaped = true
			case inner[j] == '"':
				token, err := strconv.Unquote(inner[i : j+1])
				if err != nil {
					return nil, false
				}
				values = append(values, token)
				i = j + 1
				goto nextValue
			}
			j++
		}
		return nil, false

	nextValue:
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t' || inner[i] == '\n') {
			i++
		}
		if i < len(inner) && inner[i] == ',' {
			i++
		}
	}

	return values, true
}

func equalStringSlices(a, b []string) bool {
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
