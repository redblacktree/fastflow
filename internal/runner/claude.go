// Package runner provides stage execution logic for fastflow.
package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/redblacktree/fastflow/internal/output"
)

// ClaudeInvoker handles invocation of the Claude CLI.
type ClaudeInvoker struct {
	// WorkDir is the working directory for Claude commands.
	WorkDir string
	// MaxTurns is the maximum number of turns for the Claude CLI.
	MaxTurns int
	// MaxBudgetUsd enables --max-budget-usd and --output-format json when > 0.
	MaxBudgetUsd float64
	// SkipPermissions enables --dangerously-skip-permissions flag.
	SkipPermissions bool
	// Debug enables verbose output logging.
	Debug bool
	// Verbose enables stream-json output to show tool activity.
	Verbose bool
}

// NewClaudeInvoker creates a new Claude invoker with default settings.
func NewClaudeInvoker(workDir string) *ClaudeInvoker {
	return &ClaudeInvoker{
		WorkDir:         workDir,
		MaxTurns:        50,
		SkipPermissions: true,
	}
}

// InvokeResult contains the result of a Claude invocation.
type InvokeResult struct {
	Output       string
	RawOutput    string  // Full output for auth detection (includes stderr, non-JSON lines)
	ExitCode     int
	HitMaxTurns  bool
	HitBudgetCap bool    // True when budget limit was reached
	SessionID    string  // Session ID from JSON output (for --continue/--resume)
	TotalCostUsd float64 // Actual spend from this invocation
}

// claudeJSONResult represents the JSON output from claude --output-format json.
type claudeJSONResult struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMs   int     `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUsd float64 `json:"total_cost_usd"`
}

// Budget/turn exhaustion subtypes from Claude CLI.
const (
	subtypeSuccess         = "success"
	subtypeBudgetExhausted = "error_max_budget_usd"
	subtypeMaxTurns        = "error_max_turns"
)

// Invoke runs Claude with the given prompt and model.
// If ANTHROPIC_API_KEY is set but rejected, it retries without the key.
func (c *ClaudeInvoker) Invoke(prompt string, model string) (*InvokeResult, error) {
	result, err := c.invokeWithEnv(prompt, model, nil)
	if err != nil {
		return nil, err
	}

	// If API key was rejected and is set in env, retry without it
	if isInvalidAPIKey(result.RawOutput) && os.Getenv("ANTHROPIC_API_KEY") != "" {
		output.Printf("WARNING: ANTHROPIC_API_KEY is set but was rejected. Falling back to claude.ai session.\n")
		retryResult, retryErr := c.invokeWithEnv(prompt, model, envWithoutAPIKey())
		if retryErr != nil {
			return nil, fmt.Errorf("fallback also failed: %w (original: ANTHROPIC_API_KEY rejected)", retryErr)
		}
		return retryResult, nil
	}

	return result, nil
}

// invokeWithEnv is the internal implementation that runs Claude with an optional env override.
func (c *ClaudeInvoker) invokeWithEnv(prompt string, model string, env []string) (*InvokeResult, error) {
	args := []string{
		"--model", model,
		"--max-turns", fmt.Sprintf("%d", c.MaxTurns),
	}

	if c.Verbose {
		args = append(args, "--output-format", "stream-json", "--verbose")
	} else if c.MaxBudgetUsd > 0 {
		args = append(args, "--output-format", "json")
	} else {
		args = append(args, "--print")
	}

	if c.MaxBudgetUsd > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", c.MaxBudgetUsd))
	}

	if c.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	args = append(args, prompt)

	if c.Debug {
		output.Printf("[DEBUG] Claude command: claude %v\n", args[:len(args)-1]) // Don't print full prompt
		output.Printf("[DEBUG] Prompt length: %d chars\n", len(prompt))
		output.Printf("[DEBUG] Working directory: %s\n", c.WorkDir)
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = c.WorkDir
	if env != nil {
		cmd.Env = env
	}

	if c.Verbose {
		result, err := c.runWithStreamParsing(cmd)
		if err != nil {
			return nil, err
		}
		if isNotLoggedIn(result.RawOutput) {
			return nil, ErrNotLoggedIn
		}
		if isRateLimited(result.RawOutput) {
			return nil, ErrRateLimited
		}
		if result.ExitCode != 0 && result.Output == "" {
			snippet := strings.TrimSpace(result.RawOutput)
			if len(snippet) > 300 {
				snippet = snippet[len(snippet)-300:]
			}
			return nil, fmt.Errorf("claude exited with code %d: %s", result.ExitCode, snippet)
		}
		result.HitMaxTurns = result.HitMaxTurns || isMaxTurnsError(result.Output)
		if c.Debug && result.HitMaxTurns {
			output.Printf("[DEBUG] Max turns limit detected in output\n")
		}
		return result, nil
	}

	if c.MaxBudgetUsd > 0 {
		// JSON output path: capture and parse structured result
		result, err := c.runWithJSONParsing(cmd)
		if err != nil {
			return nil, err
		}
		if isNotLoggedIn(result.RawOutput) {
			return nil, ErrNotLoggedIn
		}
		return result, nil
	}

	// --print path: capture output while also streaming to stdout
	var outputBuf bytes.Buffer
	multiWriter := io.MultiWriter(output.Writer, &outputBuf)

	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	err := cmd.Run()

	result := &InvokeResult{
		Output: outputBuf.String(),
	}
	result.RawOutput = result.Output

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			if c.Debug {
				output.Printf("[DEBUG] Claude exited with code: %d\n", result.ExitCode)
			}
		} else {
			return nil, fmt.Errorf("failed to run claude: %w", err)
		}
	}

	if c.Debug {
		output.Printf("[DEBUG] Claude output length: %d chars\n", len(result.Output))
	}

	// Detect authentication failure before anything else
	if isNotLoggedIn(result.RawOutput) {
		return nil, ErrNotLoggedIn
	}

	// Detect rate limit
	if isRateLimited(result.RawOutput) {
		return nil, ErrRateLimited
	}

	// Treat non-zero exit with no usable output as a failure
	if result.ExitCode != 0 && result.Output == "" {
		snippet := strings.TrimSpace(result.RawOutput)
		if len(snippet) > 300 {
			snippet = snippet[len(snippet)-300:]
		}
		return nil, fmt.Errorf("claude exited with code %d: %s", result.ExitCode, snippet)
	}

	// Detect if max-turns was hit
	result.HitMaxTurns = isMaxTurnsError(result.Output)
	if c.Debug && result.HitMaxTurns {
		output.Printf("[DEBUG] Max turns limit detected in output\n")
	}

	return result, nil
}

// runWithJSONParsing runs Claude with --output-format json and parses the result.
// stdout is captured silently; only the extracted result text is printed.
func (c *ClaudeInvoker) runWithJSONParsing(cmd *exec.Cmd) (*InvokeResult, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = io.MultiWriter(output.Writer, &stderrBuf)

	err := cmd.Run()

	result := &InvokeResult{
		RawOutput: stdoutBuf.String() + stderrBuf.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to run claude: %w", err)
		}
	}

	// Parse JSON output
	jsonOutput := strings.TrimSpace(stdoutBuf.String())
	if jsonOutput != "" {
		var jsonResult claudeJSONResult
		if parseErr := json.Unmarshal([]byte(jsonOutput), &jsonResult); parseErr == nil {
			result.Output = jsonResult.Result
			result.SessionID = jsonResult.SessionID
			result.TotalCostUsd = jsonResult.TotalCostUsd
			result.HitBudgetCap = jsonResult.Subtype == subtypeBudgetExhausted
			result.HitMaxTurns = jsonResult.Subtype == subtypeMaxTurns

			if c.Debug {
				output.Printf("[DEBUG] JSON result: subtype=%s, cost=$%.4f, turns=%d\n",
					jsonResult.Subtype, jsonResult.TotalCostUsd, jsonResult.NumTurns)
			}

			// Print the text result for user visibility
			if result.Output != "" {
				fmt.Fprint(output.Writer, result.Output)
			}
		} else {
			if c.Debug {
				output.Printf("[DEBUG] Failed to parse JSON output: %v\n", parseErr)
			}
			// Fall back to raw output with text-based detection
			result.Output = jsonOutput
			result.HitMaxTurns = isMaxTurnsError(jsonOutput)
		}
	}

	return result, nil
}

// InvokeContinue runs Claude with --continue to resume the most recent session.
// Used for budget-based handoff: asks the exhausted session to write a handoff doc.
func (c *ClaudeInvoker) InvokeContinue(prompt string, model string) (*InvokeResult, error) {
	args := []string{
		"--continue",
		"--model", model,
		"--max-turns", fmt.Sprintf("%d", c.MaxTurns),
		"--output-format", "json",
		"--max-budget-usd", "1.00",
	}

	if c.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	args = append(args, prompt)

	if c.Debug {
		output.Printf("[DEBUG] Claude continue command: claude %v\n", args[:len(args)-1])
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = c.WorkDir

	return c.runWithJSONParsing(cmd)
}

// isMaxTurnsError checks if the output indicates max turns was reached.
func isMaxTurnsError(output string) bool {
	return strings.Contains(output, "Reached max turns") ||
		strings.Contains(output, "Error: Reached max turns")
}

// ErrNotLoggedIn is returned when Claude CLI reports the user is not authenticated.
var ErrNotLoggedIn = fmt.Errorf("claude is not logged in: please run 'claude' interactively and use /login to authenticate")

// isNotLoggedIn checks if the output indicates an authentication failure.
func isNotLoggedIn(output string) bool {
	return strings.Contains(output, "Not logged in") ||
		strings.Contains(output, "Please run /login")
}

// ErrRateLimited is returned when Claude CLI reports a rate limit was hit.
var ErrRateLimited = fmt.Errorf("claude rate limit reached: please wait before retrying")

// isRateLimited checks if the output indicates a rate limit was hit.
func isRateLimited(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "you've hit your limit") ||
		strings.Contains(lower, "rate limit")
}

// ErrInvalidAPIKey is returned when the API key is set but rejected by the API.
var ErrInvalidAPIKey = fmt.Errorf("ANTHROPIC_API_KEY is set but was rejected by the API")

// isInvalidAPIKey checks if the output indicates an API key was rejected.
func isInvalidAPIKey(output string) bool {
	return strings.Contains(output, "invalid x-api-key") ||
		strings.Contains(output, "authentication_error") ||
		strings.Contains(output, "Invalid API key")
}

// envWithoutAPIKey returns a copy of the current environment with ANTHROPIC_API_KEY removed.
func envWithoutAPIKey() []string {
	var filtered []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "ANTHROPIC_API_KEY=") {
			filtered = append(filtered, env)
		}
	}
	return filtered
}

// InvokeWithSkill runs Claude with a skill command.
func (c *ClaudeInvoker) InvokeWithSkill(skill string, model string, context string) (*InvokeResult, error) {
	// Build a prompt that invokes the skill
	prompt := fmt.Sprintf("/%s\n\n%s", skill, context)
	return c.Invoke(prompt, model)
}
