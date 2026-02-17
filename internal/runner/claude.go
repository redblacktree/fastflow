// Package runner provides stage execution logic for fastflow.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/dustinrasener/fastflow/internal/output"
)

// ClaudeInvoker handles invocation of the Claude CLI.
type ClaudeInvoker struct {
	// WorkDir is the working directory for Claude commands.
	WorkDir string
	// MaxTurns is the maximum number of turns for the Claude CLI.
	MaxTurns int
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
	ExitCode     int
	HitMaxTurns  bool
}

// Invoke runs Claude with the given prompt and model.
func (c *ClaudeInvoker) Invoke(prompt string, model string) (*InvokeResult, error) {
	args := []string{
		"--model", model,
		"--max-turns", fmt.Sprintf("%d", c.MaxTurns),
	}

	if c.Verbose {
		args = append(args, "--output-format", "stream-json", "--verbose")
	} else {
		args = append(args, "--print")
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

	if c.Verbose {
		return c.runWithStreamParsing(cmd)
	}

	// Existing --print path: capture output while also streaming to stdout
	var outputBuf bytes.Buffer
	multiWriter := io.MultiWriter(output.Writer, &outputBuf)

	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	err := cmd.Run()

	result := &InvokeResult{
		Output: outputBuf.String(),
	}

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

	// Detect if max-turns was hit
	result.HitMaxTurns = isMaxTurnsError(result.Output)
	if c.Debug && result.HitMaxTurns {
		output.Printf("[DEBUG] Max turns limit detected in output\n")
	}

	return result, nil
}

// isMaxTurnsError checks if the output indicates max turns was reached.
func isMaxTurnsError(output string) bool {
	return strings.Contains(output, "Reached max turns") ||
		strings.Contains(output, "Error: Reached max turns")
}

// InvokeWithSkill runs Claude with a skill command.
func (c *ClaudeInvoker) InvokeWithSkill(skill string, model string, context string) (*InvokeResult, error) {
	// Build a prompt that invokes the skill
	prompt := fmt.Sprintf("/%s\n\n%s", skill, context)
	return c.Invoke(prompt, model)
}
