package setup

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

const codexNotifyConfigLine = `notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`

var codexNotifyCommand = []string{"notifytun", "emit-hook", "--tool", "codex", "--event", "notify"}

// CodexConfigurator manages ~/.codex/config.toml notify.
type CodexConfigurator struct{}

func (*CodexConfigurator) Name() string       { return "Codex CLI" }
func (*CodexConfigurator) Binaries() []string { return []string{"codex"} }
func (*CodexConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}
func (*CodexConfigurator) IsConfigured(home string) bool {
	return IsCodexConfigured((&CodexConfigurator{}).ConfigPath(home))
}
func (*CodexConfigurator) PreviewAction(home string) string {
	return "will set notify in ~/.codex/config.toml"
}
func (c *CodexConfigurator) Apply(home string) error {
	return ApplyCodexNotifyConfig(c.ConfigPath(home))
}

// GenerateCodexNotifyConfig returns the notify config line for Codex CLI.
func GenerateCodexNotifyConfig() string {
	return codexNotifyConfigLine + "\n"
}

// IsCodexConfigured reports whether the notifytun notify hook is already present.
func IsCodexConfigured(configPath string) bool {
	cfg, err := readCodexConfig(configPath)
	if err != nil {
		return false
	}
	return codexNotifyConfigured(cfg)
}

// ApplyCodexNotifyConfig writes the notifytun notify config at the TOML root.
func ApplyCodexNotifyConfig(configPath string) error {
	cfg, err := readCodexConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = map[string]any{}
		} else {
			return err
		}
	}

	if codexNotifyConfigured(cfg) {
		return nil
	}
	cfg["notify"] = append([]string(nil), codexNotifyCommand...)

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create Codex config dir: %w", err)
	}

	updated, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal Codex config: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

func readCodexConfig(configPath string) (map[string]any, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse Codex config: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

func codexNotifyConfigured(cfg map[string]any) bool {
	raw, ok := cfg["notify"]
	if !ok {
		return false
	}

	notifyArgs, ok := stringSlice(raw)
	if !ok {
		return false
	}
	return equalStringSlices(notifyArgs, codexNotifyCommand)
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
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
