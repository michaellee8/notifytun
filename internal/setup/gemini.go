package setup

import (
	"path/filepath"
)

const (
	geminiAfterAgentCommand   = "notifytun emit-hook --tool gemini --event AfterAgent"
	geminiNotificationCommand = "notifytun emit-hook --tool gemini --event Notification"
)

var geminiStripPrefixes = []string{"notifytun emit ", "notifytun emit-hook "}

// GeminiConfigurator manages ~/.gemini/settings.json hooks.
type GeminiConfigurator struct{}

func (*GeminiConfigurator) Name() string       { return "Gemini CLI" }
func (*GeminiConfigurator) Binaries() []string { return []string{"gemini"} }
func (*GeminiConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".gemini", "settings.json")
}
func (*GeminiConfigurator) IsConfigured(home string) bool {
	return IsGeminiConfigured((&GeminiConfigurator{}).ConfigPath(home))
}
func (*GeminiConfigurator) PreviewAction(home string) string {
	return "will add AfterAgent + Notification hooks to ~/.gemini/settings.json"
}
func (c *GeminiConfigurator) Apply(home string) error {
	return ApplyGeminiHook(c.ConfigPath(home))
}

func geminiHookEvents() []JSONHookEvent {
	return []JSONHookEvent{
		{Event: "AfterAgent", Command: geminiAfterAgentCommand},
		{Event: "Notification", Command: geminiNotificationCommand},
	}
}

// IsGeminiConfigured reports whether notifytun Gemini hooks are already present.
func IsGeminiConfigured(settingsPath string) bool {
	return JSONHooksConfigured(settingsPath, geminiHookEvents())
}

// ApplyGeminiHook merges notifytun Gemini hooks into the given settings file.
func ApplyGeminiHook(settingsPath string) error {
	if IsGeminiConfigured(settingsPath) {
		return nil
	}
	return ApplyJSONHooks(settingsPath, geminiHookEvents(), geminiStripPrefixes)
}
