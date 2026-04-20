package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// openCodePluginContent is the verbatim content notifytun writes.
// Any byte-level divergence causes IsOpenCodeConfigured to report false
// and Apply to overwrite.
const openCodePluginContent = `// Managed by ` + "`notifytun remote-setup`" + `. Edits will be overwritten.
export const NotifytunPlugin = async ({ client, $ }) => {
  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;
      let body = "";
      try {
        const sessionID =
          event.properties?.sessionID ?? event.properties?.session_id;
        if (sessionID) {
          const msgs = await client.session.messages({ path: { id: sessionID } });
          const last = Array.isArray(msgs) ? msgs[msgs.length - 1] : null;
          body = extractText(last);
        }
      } catch (_) {
        // Never let a notification failure block the session.
      }
      const payload = JSON.stringify({ body });
      try {
        await $` + "`echo ${payload} | notifytun emit-hook --tool opencode --event session.idle`" + `;
      } catch (_) {
        // notifytun missing or failing must not block the session.
      }
    },
  };
};

function extractText(msg) {
  if (!msg) return "";
  const parts = msg.parts ?? msg.content ?? [];
  if (typeof parts === "string") return parts;
  if (!Array.isArray(parts)) return "";
  return parts
    .map((p) => (typeof p === "string" ? p : p?.text ?? ""))
    .filter(Boolean)
    .join("\n");
}
`

// OpenCodeConfigurator writes a verbatim JS plugin.
type OpenCodeConfigurator struct{}

func (*OpenCodeConfigurator) Name() string       { return "OpenCode" }
func (*OpenCodeConfigurator) Binaries() []string { return []string{"opencode"} }
func (*OpenCodeConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js")
}
func (*OpenCodeConfigurator) IsConfigured(home string) bool {
	return IsOpenCodeConfigured((&OpenCodeConfigurator{}).ConfigPath(home))
}
func (*OpenCodeConfigurator) PreviewAction(home string) string {
	return "will write ~/.config/opencode/plugins/notifytun.js"
}
func (c *OpenCodeConfigurator) Apply(home string) error {
	return ApplyOpenCodePlugin(c.ConfigPath(home))
}

// GenerateOpenCodePlugin returns the verbatim plugin content.
func GenerateOpenCodePlugin() string {
	return openCodePluginContent
}

// IsOpenCodeConfigured reports whether the plugin file matches the canonical content.
func IsOpenCodeConfigured(pluginPath string) bool {
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		return false
	}
	return bytes.Equal(data, []byte(openCodePluginContent))
}

// ApplyOpenCodePlugin writes the canonical plugin file, creating parents.
func ApplyOpenCodePlugin(pluginPath string) error {
	if IsOpenCodeConfigured(pluginPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		return fmt.Errorf("create OpenCode plugin dir: %w", err)
	}
	if err := os.WriteFile(pluginPath, []byte(openCodePluginContent), 0o644); err != nil {
		return fmt.Errorf("write OpenCode plugin: %w", err)
	}
	return nil
}
