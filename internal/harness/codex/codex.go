// Package codex implements the fastflow harness interface for OpenAI Codex CLI.
package codex

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
	"github.com/redblacktree/fastflow/internal/output"
)

// Harness implements harness.Harness for the Codex CLI.
type Harness struct {
	binary       string
	defaultModel string
	config       harness.CodexConfig
}

// New creates a Codex harness from configuration.
func New(cfg harness.Config) *Harness {
	bin := cfg.Binary
	if bin == "" {
		bin = "codex"
	}
	model := cfg.DefaultModel
	if model == "" {
		model = "o4-mini" // current Codex CLI default; see docs/backends.md
	}
	return &Harness{binary: bin, defaultModel: model, config: cfg.Codex}
}

func (b *Harness) Name() string         { return "codex" }
func (b *Harness) DefaultModel() string { return b.defaultModel }

func (b *Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		SupportsBudget:        false, // codex has no --max-budget-usd
		SupportsMaxTurns:      false, // codex has no --max-turns
		SupportsResume:        true,
		SupportsStreaming:     true,  // --json NDJSON event stream
		SupportsSlashCommands: false, // skill body is inlined into prompt
	}
}

// Invoke runs the Codex CLI with the given options.
func (b *Harness) Invoke(opts harness.InvokeOptions) (*harness.InvokeResult, error) {
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
func (b *Harness) buildArgs(opts harness.InvokeOptions, lastMsgPath, prompt string) []string {
	if opts.Continue {
		// codex exec resume --last replaces "exec" and feeds a follow-up prompt
		args := []string{
			"exec", "resume", "--last",
		}
		args = append(args, b.configArgs()...)
		args = append(args,
			"--enable", "multi_agent",
			"--model", opts.Model,
			"--cd", opts.WorkDir,
			"--json",
			"--output-last-message", lastMsgPath,
		)
		args = append(args, b.executionModeArgs()...)
		return append(args, prompt)
	}
	args := []string{
		"exec",
	}
	args = append(args, b.configArgs()...)
	args = append(args,
		"--enable", "multi_agent",
		"--model", opts.Model,
		"--cd", opts.WorkDir,
		"--json",
		"--output-last-message", lastMsgPath,
	)
	args = append(args, b.executionModeArgs()...)
	return append(args, prompt)
}

func (b *Harness) configArgs() []string {
	var args []string
	add := func(key string, value int) {
		if value <= 0 {
			return
		}
		args = append(args, "-c", key+"="+strconv.Itoa(value))
	}

	add("model_context_window", b.config.ModelContextWindow)
	add("model_auto_compact_token_limit", b.config.ModelAutoCompactTokenLimit)
	add("tool_output_token_limit", b.config.ToolOutputTokenLimit)
	return args
}

func (b *Harness) executionModeArgs() []string {
	if b.config.BypassApprovalsAndSandbox {
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	}
	args := []string{"--full-auto"}
	if sandbox := strings.TrimSpace(b.config.Sandbox); sandbox != "" {
		args = append(args, "--sandbox", sandbox)
	}
	return args
}

// run executes the codex command, parses NDJSON events, reads the last message file.
func (b *Harness) run(cmd *exec.Cmd, opts harness.InvokeOptions, lastMsgPath string) (*harness.InvokeResult, error) {
	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(output.Writer, &stderrBuf)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", b.binary, err)
	}

	sessionID, rawEvents, usage := parseCodexStream(stdoutR, opts.Verbose, opts.Debug)

	cmdErr := cmd.Wait()

	rawOutput := rawEvents + "\n" + stderrBuf.String()

	result := &harness.InvokeResult{
		RawOutput:      rawOutput,
		SessionID:      sessionID,
		ContextTokens:  usage.InputTokens + usage.OutputTokens,
		ContextWindow:  b.config.ModelContextWindow,
		ContextPercent: b.contextPercent(usage),
	}
	result.HitContextHandoff = b.hitContextHandoffThreshold(result.ContextPercent)

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

	// Classify auth errors from Codex CLI failure channels, not successful
	// assistant/tool output. Review context often mentions prior "rate limit"
	// blockers, and that should not poison a successful current run.
	stderrOutput := stderrBuf.String()
	if authErr := classifyAuthError(stderrOutput); authErr != nil {
		return nil, authErr
	}
	if result.ExitCode != 0 && result.Output == "" {
		if authErr := classifyAuthError(rawEvents); authErr != nil {
			return nil, authErr
		}
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

func (b *Harness) contextPercent(usage codexUsage) float64 {
	if b.config.ModelContextWindow <= 0 {
		return 0
	}
	tokens := usage.InputTokens + usage.OutputTokens
	if tokens <= 0 {
		return 0
	}
	return float64(tokens) * 100 / float64(b.config.ModelContextWindow)
}

func (b *Harness) hitContextHandoffThreshold(percent float64) bool {
	return b.config.ContextHandoffThresholdPct > 0 && percent >= b.config.ContextHandoffThresholdPct
}
