package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/redblacktree/fastflow/internal/output"
	"github.com/fatih/color"
)

// streamEvent represents a single NDJSON line from claude --output-format stream-json.
type streamEvent struct {
	Type    string         `json:"type"`    // "system", "assistant", "user", "result"
	Subtype string         `json:"subtype"` // "init" for system events
	Message *streamMessage `json:"message"`
	Result  string         `json:"result"` // final accumulated text (type=="result" only)
}

// streamMessage is the message field within assistant/user events.
type streamMessage struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a single content item in a message.
type contentBlock struct {
	Type  string          `json:"type"`  // "tool_use", "tool_result", "text"
	Name  string          `json:"name"`  // tool name (tool_use only)
	Input json.RawMessage `json:"input"` // raw JSON input (tool_use only)
	Text  string          `json:"text"`  // text content (text blocks only)
}

var (
	toolColor = color.New(color.FgCyan, color.Bold).SprintFunc()
	dimColor  = color.New(color.Faint).SprintFunc()
)

// runWithStreamParsing reads stream-json output from Claude, displays tool activity,
// and returns the final text result for judge evaluation.
func (c *ClaudeInvoker) runWithStreamParsing(cmd *exec.Cmd) (*InvokeResult, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var finalOutput string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			if c.Debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Failed to parse stream event: %v\n", err)
			}
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
		}
	}

	if err := scanner.Err(); err != nil {
		if c.Debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] Scanner error: %v\n", err)
		}
	}

	cmdErr := cmd.Wait()

	result := &InvokeResult{
		Output: finalOutput,
	}

	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			if c.Debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] Claude exited with code: %d\n", result.ExitCode)
			}
		} else {
			return nil, fmt.Errorf("claude process failed: %w", cmdErr)
		}
	}

	if c.Debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Claude output length: %d chars\n", len(result.Output))
	}

	result.HitMaxTurns = isMaxTurnsError(result.Output)
	if c.Debug && result.HitMaxTurns {
		fmt.Fprintf(os.Stderr, "[DEBUG] Max turns limit detected in output\n")
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
