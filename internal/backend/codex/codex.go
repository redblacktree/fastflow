// Package codex implements the fastflow Backend interface for OpenAI Codex CLI.
package codex

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/redblacktree/fastflow/internal/backend"
	"github.com/redblacktree/fastflow/internal/output"
)

// Backend implements backend.Backend for the Codex CLI.
type Backend struct {
	binary       string
	defaultModel string
}

// New creates a Codex backend from configuration.
func New(cfg backend.Config) *Backend {
	bin := cfg.Binary
	if bin == "" {
		bin = "codex"
	}
	model := cfg.DefaultModel
	if model == "" {
		model = "o4-mini" // current Codex CLI default; see docs/backends.md
	}
	return &Backend{binary: bin, defaultModel: model}
}

func (b *Backend) Name() string         { return "codex" }
func (b *Backend) DefaultModel() string { return b.defaultModel }

func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		SupportsBudget:        false, // codex has no --max-budget-usd
		SupportsMaxTurns:      false, // codex has no --max-turns
		SupportsResume:        true,
		SupportsStreaming:     true,  // --json NDJSON event stream
		SupportsSlashCommands: false, // skill body is inlined into prompt
	}
}

// Invoke runs the Codex CLI with the given options.
func (b *Backend) Invoke(opts backend.InvokeOptions) (*backend.InvokeResult, error) {
	// Build prompt: if Skill, resolve and prepend skill content to SkillContext.
	prompt := opts.Prompt
	if opts.Skill != "" {
		skillBody, err := resolveSkill(opts.WorkDir, opts.Skill)
		if err != nil {
			return nil, fmt.Errorf("resolve skill %q: %w", opts.Skill, err)
		}
		prompt = skillBody + "\n\n---\n\n" + opts.SkillContext
	}

	// Use --output-last-message to capture final text reliably.
	lastMsgFile, err := os.CreateTemp("", "codex-last-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	lastMsgPath := lastMsgFile.Name()
	lastMsgFile.Close()
	defer os.Remove(lastMsgPath)

	args := b.buildArgs(opts, lastMsgPath, prompt)

	if opts.Debug {
		output.Printf("[DEBUG] %s command: %s %v\n", b.binary, b.binary, args[:len(args)-1])
		output.Printf("[DEBUG] Prompt length: %d chars\n", len(prompt))
		output.Printf("[DEBUG] Working directory: %s\n", opts.WorkDir)
	}

	cmd := exec.Command(b.binary, args...)
	cmd.Dir = opts.WorkDir

	return b.run(cmd, opts, lastMsgPath)
}

// buildArgs constructs the codex exec argument list.
func (b *Backend) buildArgs(opts backend.InvokeOptions, lastMsgPath, prompt string) []string {
	if opts.Continue {
		// codex exec resume --last replaces "exec" and feeds a follow-up prompt
		return []string{
			"exec", "resume", "--last",
			"--model", opts.Model,
			"--cd", opts.WorkDir,
			"--json",
			"--output-last-message", lastMsgPath,
			"--full-auto",
			prompt,
		}
	}
	return []string{
		"exec",
		"--model", opts.Model,
		"--cd", opts.WorkDir,
		"--json",
		"--output-last-message", lastMsgPath,
		"--full-auto",
		prompt,
	}
}

// run executes the codex command, parses NDJSON events, reads the last message file.
func (b *Backend) run(cmd *exec.Cmd, opts backend.InvokeOptions, lastMsgPath string) (*backend.InvokeResult, error) {
	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(output.Writer, &stderrBuf)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", b.binary, err)
	}

	sessionID, rawEvents := parseCodexStream(stdoutR, opts.Verbose, opts.Debug)

	cmdErr := cmd.Wait()

	rawOutput := rawEvents + "\n" + stderrBuf.String()

	result := &backend.InvokeResult{
		RawOutput: rawOutput,
		SessionID: sessionID,
	}

	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("%s process failed: %w", b.binary, cmdErr)
		}
	}

	// Read final assistant message from temp file
	if data, readErr := os.ReadFile(lastMsgPath); readErr == nil {
		result.Output = string(data)
	} else if result.ExitCode == 0 {
		// If exit was clean but no output file, fall back to raw events
		result.Output = rawEvents
	}

	if opts.Debug {
		output.Printf("[DEBUG] %s output length: %d chars\n", b.binary, len(result.Output))
	}

	// Classify auth errors
	if authErr := classifyAuthError(rawOutput); authErr != nil {
		return nil, authErr
	}

	if result.ExitCode != 0 && result.Output == "" {
		snippet := rawOutput
		if len(snippet) > 300 {
			snippet = snippet[len(snippet)-300:]
		}
		return nil, fmt.Errorf("%s exited with code %d: %s", b.binary, result.ExitCode, snippet)
	}

	return result, nil
}
