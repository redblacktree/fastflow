package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/redblacktree/fastflow/internal/harness"
	"github.com/redblacktree/fastflow/internal/output"
)

// streamEvent represents a single NDJSON line from claude --output-format stream-json.
type streamEvent struct {
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype"`
	Message   *streamMessage `json:"message"`
	Result    string         `json:"result"`
	SessionID string         `json:"session_id"`
	IsError   bool           `json:"is_error"`
}

// streamMessage is the message field within assistant/user events.
type streamMessage struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a single content item in a message.
type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Text  string          `json:"text"`
}

var (
	toolColor = color.New(color.FgCyan, color.Bold).SprintFunc()
	dimColor  = color.New(color.Faint).SprintFunc()
)

// runWithStreamParsing reads stream-json output from Claude, displays tool activity,
// and returns the final text result.
func (b *Harness) runWithStreamParsing(cmd *exec.Cmd, debug bool) (*harness.InvokeResult, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", b.binary, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var finalOutput string
	var nonJSONLines []string
	var resultSubtype string
	var resultSessionID string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Failed to parse stream event: %v\n", err)
			}
			nonJSONLines = append(nonJSONLines, string(line))
			continue
		}

		switch event.Type {
		case "assistant":
			if event.Message == nil {
				continue
			}
			for _, block := range event.Message.Content {
				switch block.Type {
				case "tool_use":
					printToolActivity(block.Name, block.Input)
				case "text":
					if block.Text != "" {
						printTextActivity(block.Text)
					}
				}
			}

		case "result":
			finalOutput = event.Result
			resultSubtype = event.Subtype
			resultSessionID = event.SessionID
		}
	}

	if err := scanner.Err(); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Scanner error: %v\n", err)
		}
	}

	cmdErr := cmd.Wait()

	allOutput := strings.Join(nonJSONLines, "\n") + "\n" + stderrBuf.String()

	result := &harness.InvokeResult{
		Output:       finalOutput,
		RawOutput:    allOutput,
		HitBudgetCap: resultSubtype == subtypeBudgetExhausted,
		HitMaxTurns:  resultSubtype == subtypeMaxTurns,
		SessionID:    resultSessionID,
	}

	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] %s exited with code: %d\n", b.binary, result.ExitCode)
			}
		} else {
			return nil, fmt.Errorf("%s process failed: %w", b.binary, cmdErr)
		}
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] %s output length: %d chars\n", b.binary, len(result.Output))
	}

	return result, nil
}

// printToolActivity prints a formatted tool invocation line.
func printToolActivity(toolName string, rawInput json.RawMessage) {
	summary := summarizeToolInput(toolName, rawInput)
	output.Printf("    [%s] %s\n", toolColor(toolName), summary)
}

// printTextActivity prints a summary of text output from Claude.
func printTextActivity(text string) {
	firstLine := text
	if idx := strings.IndexByte(text, '\n'); idx != -1 {
		firstLine = text[:idx]
	}
	if len(firstLine) > 120 {
		firstLine = firstLine[:117] + "..."
	}
	output.Printf("    %s %s\n", dimColor("[text]"), dimColor(firstLine))
}

// summarizeToolInput returns a human-readable summary of a tool's input.
func summarizeToolInput(toolName string, rawInput json.RawMessage) string {
	if len(rawInput) == 0 {
		return ""
	}

	var input map[string]interface{}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return ""
	}

	switch toolName {
	case "Read":
		return getStr(input, "file_path")
	case "Write":
		return getStr(input, "file_path")
	case "Edit":
		return getStr(input, "file_path")
	case "Grep":
		pattern := getStr(input, "pattern")
		path := getStr(input, "path")
		if path != "" {
			return fmt.Sprintf("pattern=%q path=%s", pattern, path)
		}
		return fmt.Sprintf("pattern=%q", pattern)
	case "Glob":
		return getStr(input, "pattern")
	case "Bash":
		cmd := getStr(input, "command")
		if len(cmd) > 80 {
			cmd = cmd[:77] + "..."
		}
		return cmd
	case "WebFetch":
		return getStr(input, "url")
	case "WebSearch":
		return getStr(input, "query")
	case "Task":
		return getStr(input, "description")
	default:
		for _, v := range input {
			if s, ok := v.(string); ok && len(s) > 0 {
				if len(s) > 60 {
					s = s[:57] + "..."
				}
				return s
			}
		}
		return ""
	}
}

// getStr safely extracts a string value from a map.
func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
