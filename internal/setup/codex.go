package setup

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

const codexStopCommand = "notifytun emit-hook --tool codex --event Stop"

var codexStripPrefixes = []string{"notifytun emit ", "notifytun emit-hook "}

// CodexConfigurator manages ~/.codex/config.toml + ~/.codex/hooks.json.
type CodexConfigurator struct{}

func (*CodexConfigurator) Name() string       { return "Codex CLI" }
func (*CodexConfigurator) Binaries() []string { return []string{"codex"} }
func (*CodexConfigurator) ConfigPath(home string) string {
	return codexHooksPath(home)
}
func (*CodexConfigurator) IsConfigured(home string) bool {
	return IsCodexConfigured(home)
}
func (*CodexConfigurator) PreviewAction(home string) string {
	return "will enable codex_hooks in ~/.codex/config.toml and add Stop hook to ~/.codex/hooks.json"
}
func (c *CodexConfigurator) Apply(home string) error {
	return ApplyCodexConfig(codexConfigPath(home), c.ConfigPath(home))
}

func codexConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

func codexHooksPath(home string) string {
	return filepath.Join(home, ".codex", "hooks.json")
}

func codexHookEvents() []JSONHookEvent {
	return []JSONHookEvent{{Event: "Stop", Command: codexStopCommand}}
}

// GenerateCodexConfig returns the config.toml fragment needed for Codex hooks.
func GenerateCodexConfig() string {
	return "[features]\ncodex_hooks = true\n"
}

// GenerateCodexHookConfig returns the hooks.json snippet notifytun writes.
func GenerateCodexHookConfig() string {
	return `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit-hook --tool codex --event Stop"
          }
        ]
      }
    ]
  }
}`
}

// IsCodexConfigured reports whether Codex hooks are enabled, the canonical
// Stop hook is present, and no legacy root notify entry remains.
func IsCodexConfigured(home string) bool {
	cfg, err := readCodexConfig(codexConfigPath(home))
	if err != nil {
		return false
	}
	if !codexHooksFeatureEnabled(cfg) {
		return false
	}
	if codexHasRootNotify(cfg) {
		return false
	}
	return IsCodexHookConfigured(codexHooksPath(home))
}

// IsCodexHookConfigured reports whether the canonical Codex Stop hook exists.
func IsCodexHookConfigured(hooksPath string) bool {
	return JSONHooksConfigured(hooksPath, codexHookEvents())
}

// ApplyCodexConfig enables Codex hooks, removes the legacy root notify
// integration, and installs the canonical Stop hook.
func ApplyCodexConfig(configPath, hooksPath string) error {
	cfg, err := readCodexConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = map[string]any{}
		} else {
			return err
		}
	}

	features, err := mapValue(cfg["features"], "features")
	if err != nil {
		return fmt.Errorf("parse Codex config: %w", err)
	}
	features["codex_hooks"] = true
	cfg["features"] = features
	delete(cfg, "notify")

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

	if IsCodexHookConfigured(hooksPath) {
		return nil
	}
	return ApplyJSONHooks(hooksPath, codexHookEvents(), codexStripPrefixes)
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

func codexHooksFeatureEnabled(cfg map[string]any) bool {
	raw, ok := cfg["features"]
	if !ok {
		return false
	}
	features, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := features["codex_hooks"].(bool)
	return ok && enabled
}

func codexHasRootNotify(cfg map[string]any) bool {
	_, ok := cfg["notify"]
	return ok
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
