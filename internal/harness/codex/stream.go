package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/redblacktree/fastflow/internal/output"
)

// codexEvent represents a single NDJSON event from codex --json output.
// The Codex CLI emits various event types; we capture the ones we care about
// and ignore unknown types gracefully.
type codexEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Name      string          `json:"name"`      // function_call tool name
	Arguments json.RawMessage `json:"arguments"` // function_call args
	Message   string          `json:"message"`   // error message
}

// parseCodexStream reads NDJSON events from r, printing tool activity when
// verbose is true. Returns the collected session ID and the raw combined line
// output for auth classification.
func parseCodexStream(r io.Reader, verbose, debug bool) (sessionID string, rawOutput string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var lines []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)

		var ev codexEvent
		if jsonErr := json.Unmarshal([]byte(line), &ev); jsonErr != nil {
			if debug {
				fmt.Printf("[DEBUG codex] failed to parse event: %v\n", jsonErr)
			}
			continue
		}

		if ev.SessionID != "" && sessionID == "" {
			sessionID = ev.SessionID
		}

		if verbose && ev.Type == "function_call" && ev.Name != "" {
			summary := summarizeCodexTool(ev.Name, ev.Arguments)
			output.Printf("    [codex] %s %s\n", ev.Name, summary)
		}
	}

	rawOutput = strings.Join(lines, "\n")
	return sessionID, rawOutput
}

// summarizeCodexTool returns a short display summary of a Codex tool call.
func summarizeCodexTool(name string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, v := range m {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	return ""
}
