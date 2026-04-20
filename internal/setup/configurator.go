package setup

// Configurator knows how to detect, describe, and install a single tool's
// notifytun integration.
type Configurator interface {
	// Name is the human-readable tool name shown in previews ("Claude Code").
	Name() string
	// Binaries lists names to probe on PATH. First hit wins.
	Binaries() []string
	// ConfigPath returns the absolute path of the file this configurator
	// reads/writes, derived from the user's home directory.
	ConfigPath(home string) string
	// IsConfigured reports whether the canonical notifytun integration is
	// already present at ConfigPath(home).
	IsConfigured(home string) bool
	// PreviewAction returns a one-line description of what Apply would do.
	// Used for the dry-run preview and the pre-apply prompt.
	PreviewAction(home string) string
	// Apply writes the canonical notifytun integration, merging with any
	// existing unrelated configuration. Idempotent.
	Apply(home string) error
}

// Registered lists all configurators in preview order.
var Registered = []Configurator{
	&ClaudeConfigurator{},
	&CodexConfigurator{},
	&GeminiConfigurator{},
}
