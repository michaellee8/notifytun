package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

// hookDispatch describes how to turn a payload into a notification for one (tool, event) pair.
type hookDispatch struct {
	toolDisplayName string
	titleSuffix     string
	// shouldRecord may suppress a notification entirely. Returning an error
	// does not block delivery; the caller logs it and proceeds.
	shouldRecord func(map[string]any) (bool, error)
	// extractBody receives the unmarshaled payload and returns the body string.
	// Returning "" means "no body" — title-only notification. Not an error.
	extractBody func(map[string]any) string
}

var hookTable = map[string]map[string]hookDispatch{
	"claude-code": {
		"Stop": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("last_assistant_message"),
		},
		"Notification": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"gemini": {
		"AfterAgent": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("prompt_response"),
		},
		"Notification": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"codex": {
		"Stop": {
			toolDisplayName: "Codex",
			titleSuffix:     "Task complete",
			shouldRecord:    shouldRecordCodexStop,
			extractBody:     extractStringField("last_assistant_message"),
		},
		"notify": {
			toolDisplayName: "Codex",
			titleSuffix:     "Task complete",
			extractBody:     extractCodexBody,
		},
	},
	"opencode": {
		"session.idle": {
			toolDisplayName: "OpenCode",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("body"),
		},
	},
}

func extractStringField(field string) func(map[string]any) string {
	return func(payload map[string]any) string {
		if payload == nil {
			return ""
		}
		v, _ := payload[field].(string)
		return strings.TrimSpace(v)
	}
}

func extractCodexBody(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if s, _ := payload["last-assistant-message"].(string); strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	raw, ok := payload["input-messages"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func shouldRecordCodexStop(payload map[string]any) (bool, error) {
	if payload == nil {
		return true, nil
	}
	transcriptPath, _ := payload["transcript_path"].(string)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if transcriptPath == "" {
		return true, nil
	}
	isSubagent, err := transcriptShowsCodexSubagentSpawn(transcriptPath)
	if err != nil {
		return true, fmt.Errorf("classify Codex transcript %q: %w", transcriptPath, err)
	}
	return !isSubagent, nil
}

type codexTranscriptLine struct {
	Type    string                   `json:"type"`
	Payload codexTranscriptLineInner `json:"payload"`
}

type codexTranscriptLineInner struct {
	Source json.RawMessage `json:"source"`
}

type codexTranscriptSubagentSource struct {
	Subagent *struct {
		ThreadSpawn *struct {
			ParentThreadID string `json:"parent_thread_id"`
		} `json:"thread_spawn"`
	} `json:"subagent"`
}

func transcriptShowsCodexSubagentSpawn(transcriptPath string) (bool, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	line = trimASCIIWhitespace(line)
	if len(line) == 0 {
		return false, fmt.Errorf("empty transcript")
	}

	var first codexTranscriptLine
	if err := json.Unmarshal(line, &first); err != nil {
		return false, err
	}
	if first.Type != "session_meta" {
		return false, fmt.Errorf("first transcript line is %q, want session_meta", first.Type)
	}
	source := strings.TrimSpace(string(first.Payload.Source))
	if source == "" || source == "null" {
		return false, nil
	}
	if strings.HasPrefix(source, `"`) {
		return false, nil
	}

	var parsed codexTranscriptSubagentSource
	if err := json.Unmarshal(first.Payload.Source, &parsed); err != nil {
		return false, err
	}
	return parsed.Subagent != nil && parsed.Subagent.ThreadSpawn != nil, nil
}

// NewEmitHookCmd records a notification derived from an agent hook payload.
// Always exits 0; errors go to notifytun-errors.log next to the DB.
func NewEmitHookCmd() *cobra.Command {
	var (
		tool       string
		event      string
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "emit-hook [payload-json]",
		Short:         "Record a notification derived from an agent hook payload",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				LogHookError(dbPath, "emit-hook", "parse",
					fmt.Errorf("unexpected positional argument count: got %d, want 0 or 1", len(args)))
				args = args[:1]
			}

			dispatch, ok := lookupDispatch(tool, event)
			if !ok {
				LogHookError(dbPath, "emit-hook", "dispatch",
					fmt.Errorf("unknown tool/event: %s/%s", tool, event))
				return nil
			}

			payloadBytes, err := readPayload(args, cmd.InOrStdin())
			if err != nil {
				LogHookError(dbPath, "emit-hook", "parse", err)
			}

			var payload map[string]any
			if len(payloadBytes) > 0 {
				if err := json.Unmarshal(payloadBytes, &payload); err != nil {
					LogHookError(dbPath, "emit-hook", "parse", err)
					payload = nil
				}
			}

			if dispatch.shouldRecord != nil {
				shouldRecord, err := dispatch.shouldRecord(payload)
				if err != nil {
					LogHookError(dbPath, "emit-hook", "parse", err)
				}
				if !shouldRecord {
					return nil
				}
			}

			title := dispatch.toolDisplayName + ": " + dispatch.titleSuffix
			body := truncateRunes(dispatch.extractBody(payload), 180)

			d, err := db.Open(dbPath)
			if err != nil {
				LogHookError(dbPath, "emit-hook", "db-open", err)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				LogHookError(dbPath, "emit-hook", "db-insert", err)
				return nil
			}

			_ = socket.SendWakeup(socketPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name (claude-code|gemini|codex|opencode)")
	cmd.Flags().StringVar(&event, "event", "", "Hook event name (Stop|Notification|AfterAgent|notify|session.idle)")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}

func lookupDispatch(tool, event string) (hookDispatch, bool) {
	byEvent, ok := hookTable[tool]
	if !ok {
		return hookDispatch{}, false
	}
	d, ok := byEvent[event]
	return d, ok
}

func readPayload(args []string, stdin io.Reader) ([]byte, error) {
	if len(args) == 1 {
		return []byte(args[0]), nil
	}
	if stdin == nil {
		return nil, nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func trimASCIIWhitespace(b []byte) []byte {
	start := 0
	for start < len(b) && isASCIIWhitespace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isASCIIWhitespace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isASCIIWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}
