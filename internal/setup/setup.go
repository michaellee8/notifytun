package setup

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tool represents a detected AI coding tool and whether notifytun can configure it.
type Tool struct {
	Name       string
	Binary     string
	Detected   bool
	Configured bool
	Supported  bool
	Cfg        Configurator
}

// knownTools is the detection list. It includes tools for which we do not yet
// have a Configurator registered — those return with Supported=false and Cfg=nil.
// Later tasks register more Configurators and eventually this list goes away.
var knownTools = []struct {
	Name     string
	Binaries []string
}{
	{Name: "Claude Code", Binaries: []string{"claude", "claude-code"}},
	{Name: "Codex CLI", Binaries: []string{"codex"}},
	{Name: "Gemini CLI", Binaries: []string{"gemini"}},
	{Name: "OpenCode", Binaries: []string{"opencode"}},
}

// DetectTools scans the provided path list or the current PATH when pathEnv is empty.
func DetectTools(pathEnv string) []Tool {
	configurators := map[string]Configurator{}
	for _, cfg := range Registered {
		configurators[cfg.Name()] = cfg
	}

	var tools []Tool
	for _, known := range knownTools {
		tool := Tool{Name: known.Name}
		for _, binary := range known.Binaries {
			if path := lookPath(binary, pathEnv); path != "" {
				tool.Binary = path
				tool.Detected = true
				break
			}
		}
		if !tool.Detected {
			continue
		}
		if cfg, ok := configurators[known.Name]; ok {
			tool.Cfg = cfg
			tool.Supported = true
		}
		tools = append(tools, tool)
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

// Preview summarizes what remote-setup would do for detected tools.
func Preview(tools []Tool) string {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		switch {
		case tool.Configured:
			sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
		case tool.Cfg != nil:
			sb.WriteString(fmt.Sprintf("  * %s -- %s\n", tool.Name, tool.Cfg.PreviewAction("")))
		default:
			sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
		}
	}
	return sb.String()
}
