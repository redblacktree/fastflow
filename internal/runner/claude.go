// Package runner provides stage execution logic for fastflow.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ClaudeInvoker handles invocation of the Claude CLI.
type ClaudeInvoker struct {
	// WorkDir is the working directory for Claude commands.
	WorkDir string
	// MaxTurns is the maximum number of turns for the Claude CLI.
	MaxTurns int
	// SkipPermissions enables --dangerously-skip-permissions flag.
	SkipPermissions bool
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
	Output   string
	ExitCode int
}

// Invoke runs Claude with the given prompt and model.
func (c *ClaudeInvoker) Invoke(prompt string, model string) (*InvokeResult, error) {
	args := []string{
		"--model", model,
		"--print",
		"--max-turns", fmt.Sprintf("%d", c.MaxTurns),
	}

	if c.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	args = append(args, prompt)

	cmd := exec.Command("claude", args...)
	cmd.Dir = c.WorkDir

	// Capture output while also streaming to stdout
	var outputBuf bytes.Buffer
	multiWriter := io.MultiWriter(os.Stdout, &outputBuf)

	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	err := cmd.Run()

	result := &InvokeResult{
		Output: outputBuf.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to run claude: %w", err)
		}
	}

	return result, nil
}

// InvokeWithSkill runs Claude with a skill command.
func (c *ClaudeInvoker) InvokeWithSkill(skill string, model string, context string) (*InvokeResult, error) {
	// Build a prompt that invokes the skill
	prompt := fmt.Sprintf("/%s\n\n%s", skill, context)
	return c.Invoke(prompt, model)
}
