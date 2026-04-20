package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LogHookError appends a single line to the error log next to dbPath.
// All I/O failures are silently swallowed — a notifytun logging failure
// must never propagate to the caller (which is typically an agent hook).
func LogHookError(dbPath, subcommand, stage string, err error) {
	if err == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(dbPath), "notifytun-errors.log")
	f, ferr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(
		f,
		"%s\t%s\t%s: %s\n",
		time.Now().UTC().Format(time.RFC3339),
		subcommand,
		stage,
		err.Error(),
	)
}
