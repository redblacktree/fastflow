// Package claude implements the fastflow harness interface for Claude Code CLI.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
	"github.com/redblacktree/fastflow/internal/output"
)

// Harness implements harness.Harness for the Claude Code CLI.
type Harness struct {
	binary       string
	defaultModel string
}

// New creates a Claude harness from configuration.
func New(cfg harness.Config) *Harness {
	bin := cfg.Binary
	if bin == "" {
		bin = "claude"
	}
	model := cfg.DefaultModel
	if model == "" {
		model = "sonnet"
	}
	return &Harness{binary: bin, defaultModel: model}
}

func (b *Harness) Name() string         { return "claude" }
func (b *Harness) DefaultModel() string { return b.defaultModel }

func (b *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SupportsBudget:        true,
		SupportsMaxTurns:      true,
		SupportsResume:        true,
		SupportsStreaming:     true,
		SupportsSlashCommands: true,
	}
}

// Invoke runs the Claude CLI with the given options.
// If opts.Continue is true, it resumes the most recent session with --continue.
// If opts.Skill is non-empty, it builds a slash-command prompt.
// If ANTHROPIC_API_KEY is set but rejected, it retries without the key.
func (b *Harness) Invoke(opts harness.InvokeOptions) (*harness.InvokeResult, error) {
	if opts.Continue {
		return b.invokeContinue(opts)
	}

	prompt := opts.Prompt
	if opts.Skill != "" {
		prompt = fmt.Sprintf("/%s\n\n%s", opts.Skill, opts.SkillContext)
	}

	result, err := b.invokeWithEnv(prompt, opts, nil)
	if err != nil {
		return nil, err
	}

	// If API key was rejected and is set in env, retry without it
	if isInvalidAPIKey(result.RawOutput) && os.Getenv("ANTHROPIC_API_KEY") != "" {
		output.Printf("WARNING: ANTHROPIC_API_KEY is set but was rejected. Falling back to claude.ai session.\n")
		retryResult, retryErr := b.invokeWithEnv(prompt, opts, envWithoutAPIKey())
		if retryErr != nil {
			return nil, fmt.Errorf("fallback also failed: %w (original: ANTHROPIC_API_KEY rejected)", retryErr)
		}
		return retryResult, nil
	}

	return result, nil
}

// invokeContinue runs claude with --continue to resume the most recent session.
func (b *Harness) invokeContinue(opts harness.InvokeOptions) (*harness.InvokeResult, error) {
	args := []string{
		"--continue",
		"--model", opts.Model,
		"--max-turns", "50",
		"--output-format", "json",
		"--max-budget-usd", "1.00",
		"--dangerously-skip-permissions",
	}
	args = append(args, opts.Prompt)

	if opts.Debug {
		output.Printf("[DEBUG] %s continue command: %s %v\n", b.binary, b.binary, args[:len(args)-1])
	}

	cmd := exec.Command(b.binary, args...)
	cmd.Dir = opts.WorkDir

	return b.runWithJSONParsing(cmd, opts.Debug)
}

// invokeWithEnv is the internal implementation that runs Claude with an optional env override.
func (b *Harness) invokeWithEnv(prompt string, opts harness.InvokeOptions, env []string) (*harness.InvokeResult, error) {
	maxTurns := 50
	if opts.MaxTurns > 0 {
		maxTurns = opts.MaxTurns
	}

	args := []string{
		"--model", opts.Model,
		"--max-turns", fmt.Sprintf("%d", maxTurns),
	}

	if opts.Verbose {
		args = append(args, "--output-format", "stream-json", "--verbose")
	} else if opts.MaxBudgetUsd > 0 {
		args = append(args, "--output-format", "json")
	} else {
		args = append(args, "--print")
	}

	if opts.MaxBudgetUsd > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.MaxBudgetUsd))
	}

	// Always skip permissions for headless runs
	args = append(args, "--dangerously-skip-permissions")

	args = append(args, prompt)

	if opts.Debug {
		output.Printf("[DEBUG] %s command: %s %v\n", b.binary, b.binary, args[:len(args)-1])
		output.Printf("[DEBUG] Prompt length: %d chars\n", len(prompt))
		output.Printf("[DEBUG] Working directory: %s\n", opts.WorkDir)
	}

	cmd := exec.Command(b.binary, args...)
	cmd.Dir = opts.WorkDir
	if env != nil {
		cmd.Env = env
	}

	if opts.Verbose {
		result, err := b.runWithStreamParsing(cmd, opts.Debug)
		if err != nil {
			return nil, err
		}
		if isNotLoggedIn(result.RawOutput) {
			return nil, harness.ErrNotLoggedIn
		}
		if isRateLimited(result.RawOutput) {
			return nil, harness.ErrRateLimited
		}
		if result.ExitCode != 0 && result.Output == "" {
			snippet := strings.TrimSpace(result.RawOutput)
			if len(snippet) > 300 {
				snippet = snippet[len(snippet)-300:]
			}
			return nil, fmt.Errorf("%s exited with code %d: %s", b.binary, result.ExitCode, snippet)
		}
		result.HitMaxTurns = result.HitMaxTurns || isMaxTurnsError(result.Output)
		if opts.Debug && result.HitMaxTurns {
			output.Printf("[DEBUG] Max turns limit detected in output\n")
		}
		return result, nil
	}

	if opts.MaxBudgetUsd > 0 {
		result, err := b.runWithJSONParsing(cmd, opts.Debug)
		if err != nil {
			return nil, err
		}
		if isNotLoggedIn(result.RawOutput) {
			return nil, harness.ErrNotLoggedIn
		}
		return result, nil
	}

	// --print path: capture output while also streaming to stdout
	var outputBuf bytes.Buffer
	multiWriter := io.MultiWriter(output.Writer, &outputBuf)

	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	err := cmd.Run()

	result := &harness.InvokeResult{
		Output: outputBuf.String(),
	}
	result.RawOutput = result.Output

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			if opts.Debug {
				output.Printf("[DEBUG] %s exited with code: %d\n", b.binary, result.ExitCode)
			}
		} else {
			return nil, fmt.Errorf("failed to run %s: %w", b.binary, err)
		}
	}

	if opts.Debug {
		output.Printf("[DEBUG] %s output length: %d chars\n", b.binary, len(result.Output))
	}

	if isNotLoggedIn(result.RawOutput) {
		return nil, harness.ErrNotLoggedIn
	}

	if isRateLimited(result.RawOutput) {
		return nil, harness.ErrRateLimited
	}

	if result.ExitCode != 0 && result.Output == "" {
		snippet := strings.TrimSpace(result.RawOutput)
		if len(snippet) > 300 {
			snippet = snippet[len(snippet)-300:]
		}
		return nil, fmt.Errorf("%s exited with code %d: %s", b.binary, result.ExitCode, snippet)
	}

	result.HitMaxTurns = isMaxTurnsError(result.Output)
	if opts.Debug && result.HitMaxTurns {
		output.Printf("[DEBUG] Max turns limit detected in output\n")
	}

	return result, nil
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

// runWithJSONParsing runs Claude with --output-format json and parses the result.
func (b *Harness) runWithJSONParsing(cmd *exec.Cmd, debug bool) (*harness.InvokeResult, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = io.MultiWriter(output.Writer, &stderrBuf)

	err := cmd.Run()

	result := &harness.InvokeResult{
		RawOutput: stdoutBuf.String() + stderrBuf.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to run %s: %w", b.binary, err)
		}
	}

	jsonOutput := strings.TrimSpace(stdoutBuf.String())
	if jsonOutput != "" {
		var jsonResult claudeJSONResult
		if parseErr := json.Unmarshal([]byte(jsonOutput), &jsonResult); parseErr == nil {
			result.Output = jsonResult.Result
			result.SessionID = jsonResult.SessionID
			result.TotalCostUsd = jsonResult.TotalCostUsd
			result.HitBudgetCap = jsonResult.Subtype == subtypeBudgetExhausted
			result.HitMaxTurns = jsonResult.Subtype == subtypeMaxTurns

			if debug {
				output.Printf("[DEBUG] JSON result: subtype=%s, cost=$%.4f, turns=%d\n",
					jsonResult.Subtype, jsonResult.TotalCostUsd, jsonResult.NumTurns)
			}

			if result.Output != "" {
				fmt.Fprint(output.Writer, result.Output)
			}
		} else {
			if debug {
				output.Printf("[DEBUG] Failed to parse JSON output: %v\n", parseErr)
			}
			result.Output = jsonOutput
			result.HitMaxTurns = isMaxTurnsError(jsonOutput)
		}
	}

	return result, nil
}

// isMaxTurnsError checks if the output indicates max turns was reached.
func isMaxTurnsError(s string) bool {
	return strings.Contains(s, "Reached max turns") ||
		strings.Contains(s, "Error: Reached max turns")
}

// isNotLoggedIn checks if the output indicates an authentication failure.
func isNotLoggedIn(s string) bool {
	return strings.Contains(s, "Not logged in") ||
		strings.Contains(s, "Please run /login")
}

// isRateLimited checks if the output indicates a rate limit was hit.
func isRateLimited(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "you've hit your limit") ||
		strings.Contains(lower, "rate limit")
}

// isInvalidAPIKey checks if the output indicates an API key was rejected.
func isInvalidAPIKey(s string) bool {
	return strings.Contains(s, "invalid x-api-key") ||
		strings.Contains(s, "authentication_error") ||
		strings.Contains(s, "Invalid API key")
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
