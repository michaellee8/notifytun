package setup

import (
	"os/exec"
	"path/filepath"
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

// DetectTools scans the provided path list or the current PATH when pathEnv is empty.
func DetectTools(pathEnv string) []Tool {
	var tools []Tool
	for _, cfg := range Registered {
		tool := Tool{
			Name:      cfg.Name(),
			Supported: true,
			Cfg:       cfg,
		}
		for _, binary := range cfg.Binaries() {
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
