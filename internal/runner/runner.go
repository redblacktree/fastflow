package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/redblacktree/fastflow/internal/config"
	"github.com/redblacktree/fastflow/internal/judge"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/state"
	"github.com/redblacktree/fastflow/internal/worktree"
	"github.com/fatih/color"
)

const (
	maxInteractionAttempts = 3
	maxEvalRetries         = 2
)

// Runner orchestrates the execution of pipeline stages.
type Runner struct {
	Config         *config.Config
	ConfigPath     string
	NoReview       bool
	DryRun         bool
	Debug          bool
	Verbose        bool
	Resume         string // "auto", "true", "false", or "force"
	// MaxResumptions is the maximum number of handoff/resume cycles per stage (default: 3).
	MaxResumptions int
	// Interactive controls whether to prompt human for input (true) or auto-answer (false).
	Interactive bool
}

// RunContext contains the context for a pipeline run.
type RunContext struct {
	Goal       string
	Ticket     string
	Workflow   string
	WorkDir    string
	RunDir     string
	RepoPath   string
	BranchName string
}

// NewRunner creates a new pipeline runner.
func NewRunner(cfg *config.Config, configPath string) *Runner {
	return &Runner{
		Config:         cfg,
		ConfigPath:     configPath,
		MaxResumptions: 3,
	}
}

// Run executes the pipeline for the given context.
func (r *Runner) Run(ctx *RunContext) error {
	// Get the workflow
	workflow, err := r.Config.GetWorkflow(ctx.Workflow)
	if err != nil {
		return err
	}

	info := color.New(color.FgCyan).SprintFunc()
	success := color.New(color.FgGreen).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()
	warning := color.New(color.FgYellow).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	output.Printf("\n%s Running workflow: %s\n", info(">>>"), bold(ctx.Workflow))
	output.Printf("    Goal: %s\n", ctx.Goal)
	output.Printf("    Ticket: %s\n", ctx.Ticket)
	output.Printf("    Working directory: %s\n", ctx.WorkDir)
	output.Printf("    Run directory: %s\n", ctx.RunDir)

	// Determine resume behavior
	shouldResume, pipelineState, err := r.determineResumeState(ctx, workflow)
	if err != nil {
		return err
	}

	if shouldResume && pipelineState != nil {
		output.Printf("    Resume mode: %s (skipping %d completed stages)\n\n",
			success("ENABLED"), len(pipelineState.CompletedStages))
	} else {
		output.Printf("    Resume mode: %s\n\n", warning("DISABLED"))
	}

	// Create the run directory
	if err := os.MkdirAll(ctx.RunDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	// Initialize or use existing state
	if pipelineState == nil {
		pipelineState = state.NewState(ctx.Workflow, workflow.Stages, ctx.Ticket, ctx.WorkDir)
		if err := pipelineState.Save(ctx.RunDir); err != nil {
			return fmt.Errorf("failed to save initial state: %w", err)
		}
	}

	// Write the goal file (always, to ensure it's current)
	if err := r.writeGoalFile(ctx); err != nil {
		return fmt.Errorf("failed to write goal file: %w", err)
	}

	// Check if all stages are already complete
	if shouldResume && len(pipelineState.CompletedStages) == len(workflow.Stages) {
		output.Printf("%s All stages already complete!\n", success("SUCCESS"))
		output.Printf("    To re-run from scratch, use: --resume=false\n")
		return nil
	}

	// Execute each stage
	for i, stageName := range workflow.Stages {
		stage, err := r.Config.GetStage(stageName)
		if err != nil {
			return err
		}

		// Skip completed stages if resuming
		if shouldResume && pipelineState.IsStageComplete(stageName) {
			output.Printf("%s Stage %d/%d: %s %s\n", info(">>>"), i+1, len(workflow.Stages),
				bold(stageName), success("[SKIPPED - already complete]"))
			continue
		}

		output.Printf("%s Stage %d/%d: %s\n", info(">>>"), i+1, len(workflow.Stages), bold(stageName))

		if err := pipelineState.SetStage(ctx.RunDir, stageName); err != nil {
			output.Printf("    %s Failed to update stage: %v\n", warning("WARN"), err)
		}

		if r.DryRun {
			output.Printf("    [DRY RUN] Would execute stage: %s\n", stageName)
			continue
		}

		// Execute the stage
		result, err := r.executeStage(ctx, stageName, stage)
		if err != nil {
			output.Printf("    %s Stage failed: %v\n", errColor("ERROR"), err)
			pipelineState.SetStatus(ctx.RunDir, state.StatusFailed) //nolint:errcheck
			return err
		}

		// Run judge evaluation
		judgeResult, err := r.evaluateStage(ctx, stageName, stage, result)
		if err != nil {
			output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
			pipelineState.SetStatus(ctx.RunDir, state.StatusFailed) //nolint:errcheck
			return err
		}

		// Handle interactive prompts
		interactionAttempts := 0
		for judgeResult.Interactive && interactionAttempts < maxInteractionAttempts {
			interactionAttempts++

			if r.Debug {
				output.Printf("[DEBUG] Question detected: %s\n", judgeResult.Reasoning)
			}

			var answer string
			if r.Interactive {
				// Human-in-the-loop mode: Prompt human for input
				output.Printf("    %s Interactive prompt detected, waiting for human input...\n",
					warning("INTERACTIVE"))
				answer, err = r.promptHumanForAnswer(judgeResult.Reasoning)
				if err != nil {
					output.Printf("    %s Failed to get human input: %v\n", errColor("ERROR"), err)
					return err
				}
				result, err = r.continueWithAnswer(ctx, stageName, stage, result, judgeResult.Reasoning, answer)
			} else {
				// Auto-answer mode: Claude answers its own question
				output.Printf("    %s Interactive prompt detected, auto-answering (%d/%d)...\n",
					warning("INTERACTIVE"), interactionAttempts, maxInteractionAttempts)
				result, err = r.selfAnswer(ctx, stageName, stage, result, judgeResult.Reasoning)
			}

			if err != nil {
				output.Printf("    %s Continuation failed: %v\n", errColor("ERROR"), err)
				return err
			}

			// Re-evaluate
			judgeResult, err = r.evaluateStage(ctx, stageName, stage, result)
			if err != nil {
				output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
				return err
			}
		}

		if judgeResult.Interactive && interactionAttempts >= maxInteractionAttempts {
			output.Printf("    %s Max interaction attempts (%d) reached\n",
				warning("WARN"), maxInteractionAttempts)
			pipelineState.SetStatus(ctx.RunDir, state.StatusFailed) //nolint:errcheck
			return fmt.Errorf("stage %s still requires input after %d attempts: %s",
				stageName, maxInteractionAttempts, judgeResult.Reasoning)
		}

		// Handle evaluation failure with automatic retry
		evalRetries := 0
		for !judgeResult.Success && evalRetries < maxEvalRetries {
			evalRetries++
			output.Printf("    %s Stage did not pass evaluation\n", errColor("FAILED"))
			output.Printf("    Reason: %s\n", judgeResult.Reasoning)
			output.Printf("    %s Retrying stage with feedback (%d/%d)...\n",
				warning("RETRY"), evalRetries, maxEvalRetries)

			result, err = r.retryWithFeedback(ctx, stageName, stage, result, judgeResult.Reasoning)
			if err != nil {
				output.Printf("    %s Retry failed: %v\n", errColor("ERROR"), err)
				return err
			}

			judgeResult, err = r.evaluateStage(ctx, stageName, stage, result)
			if err != nil {
				output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
				return err
			}
		}

		if !judgeResult.Success {
			output.Printf("    %s Stage did not pass evaluation after %d retries\n", errColor("FAILED"), maxEvalRetries)
			output.Printf("    Reason: %s\n", judgeResult.Reasoning)
			pipelineState.SetStatus(ctx.RunDir, state.StatusFailed) //nolint:errcheck
			return fmt.Errorf("stage %s failed evaluation: %s", stageName, judgeResult.Reasoning)
		}

		// Mark stage as complete
		if err := pipelineState.MarkStageComplete(ctx.RunDir, stageName); err != nil {
			output.Printf("    %s Failed to save state: %v\n", warning("WARN"), err)
		}

		output.Printf("    %s Stage completed successfully\n", success("PASS"))
		output.Printf("    %s\n\n", judgeResult.Reasoning)

		// Handle checkpoint
		if stage.Checkpoint && !r.NoReview {
			if err := pipelineState.SetStatus(ctx.RunDir, state.StatusCheckpoint); err != nil {
				output.Printf("    %s Failed to update status: %v\n", warning("WARN"), err)
			}
			if err := r.handleCheckpoint(ctx, stageName); err != nil {
				return err
			}
			// Restore running status after checkpoint resumes
			if err := pipelineState.SetStatus(ctx.RunDir, state.StatusRunning); err != nil {
				output.Printf("    %s Failed to update status: %v\n", warning("WARN"), err)
			}
		}
	}

	if err := pipelineState.SetStatus(ctx.RunDir, state.StatusComplete); err != nil {
		output.Printf("    %s Failed to update status: %v\n", warning("WARN"), err)
	}
	output.Printf("\n%s Pipeline completed successfully!\n", success("SUCCESS"))
	return nil
}

// determineResumeState decides whether to resume and loads existing state.
func (r *Runner) determineResumeState(ctx *RunContext, workflow *config.Workflow) (bool, *state.PipelineState, error) {
	errColor := color.New(color.FgRed).SprintFunc()

	// Load existing state if present
	existingState, err := state.Load(ctx.RunDir)
	if err != nil {
		return false, nil, fmt.Errorf("failed to load state: %w", err)
	}

	// Determine if we should resume based on flag
	shouldResume := false
	switch r.Resume {
	case "false":
		// Explicit fresh start - clear run directory
		if existingState != nil {
			if err := r.clearRunDirectory(ctx.RunDir); err != nil {
				return false, nil, fmt.Errorf("failed to clear run directory: %w", err)
			}
		}
		return false, nil, nil

	case "true":
		// Explicit resume - must have existing state
		if existingState == nil {
			return false, nil, fmt.Errorf("--resume=true specified but no existing state found")
		}
		shouldResume = true

	case "force":
		// Force resume - skip config validation
		if existingState == nil {
			return false, nil, fmt.Errorf("--resume=force specified but no existing state found")
		}
		return true, existingState, nil

	default: // "auto"
		// Auto-detect based on existing state
		shouldResume = existingState != nil && len(existingState.CompletedStages) > 0
	}

	// Validate config hasn't changed (unless force)
	if shouldResume && existingState != nil {
		currentHash := state.ComputeConfigHash(ctx.Workflow, workflow.Stages)
		configChanged := existingState.ConfigHash != currentHash
		workflowChanged := existingState.Workflow != ctx.Workflow

		if configChanged || workflowChanged {
			if workflow.IgnoreConfigChange {
				// Workflow configured to start fresh on config change instead of erroring
				if err := r.clearRunDirectory(ctx.RunDir); err != nil {
					return false, nil, fmt.Errorf("failed to clear run directory: %w", err)
				}
				return false, nil, nil
			}
			// Error with guidance
			if workflowChanged {
				return false, nil, fmt.Errorf(
					"%s Workflow changed from %q to %q.\n"+
						"    Use --resume=false to start fresh, or --resume=force to continue anyway",
					errColor("ERROR"), existingState.Workflow, ctx.Workflow)
			}
			return false, nil, fmt.Errorf(
				"%s Config has changed since last run (workflow or stages modified).\n"+
					"    Use --resume=false to start fresh, or --resume=force to continue anyway",
				errColor("ERROR"))
		}
	}

	return shouldResume, existingState, nil
}

// clearRunDirectory removes all files in the run directory.
func (r *Runner) clearRunDirectory(runDir string) error {
	entries, err := os.ReadDir(runDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(runDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

// executeStage runs a single stage.
func (r *Runner) executeStage(ctx *RunContext, stageName string, stage *config.Stage) (*InvokeResult, error) {
	invoker := NewClaudeInvoker(ctx.WorkDir)
	invoker.Debug = r.Debug
	invoker.Verbose = r.Verbose

	model := stage.Model
	if model == "" {
		model = "sonnet"
	}

	// Build the prompt
	prompt, err := r.buildStagePrompt(ctx, stageName, stage)
	if err != nil {
		return nil, err
	}

	// Execute based on whether it's a skill or prompt file
	var result *InvokeResult
	if stage.Skill != "" {
		result, err = invoker.InvokeWithSkill(stage.Skill, model, prompt)
	} else {
		result, err = invoker.Invoke(prompt, model)
	}
	if err != nil {
		return nil, err
	}

	// Handle max-turns with automatic handoff/resume
	resumptions := 0
	for result.HitMaxTurns && resumptions < r.MaxResumptions {
		resumptions++
		warning := color.New(color.FgYellow).SprintFunc()
		output.Printf("    %s Max turns reached, attempting handoff/resume (%d/%d)...\n",
			warning("RESUME"), resumptions, r.MaxResumptions)

		// Run create_handoff to save state
		handoffResult, handoffErr := r.runHandoffCycle(ctx, stageName, model, invoker)
		if handoffErr != nil {
			if r.Debug {
				output.Printf("[DEBUG] Handoff/resume failed: %v\n", handoffErr)
			}
			// Continue with partial result rather than failing completely
			break
		}

		// Combine outputs
		result.Output += "\n\n--- [RESUMED AFTER MAX TURNS] ---\n\n" + handoffResult.Output
		result.HitMaxTurns = handoffResult.HitMaxTurns
		result.ExitCode = handoffResult.ExitCode
	}

	if result.HitMaxTurns && resumptions >= r.MaxResumptions {
		warning := color.New(color.FgYellow).SprintFunc()
		output.Printf("    %s Max resumptions (%d) reached, proceeding with partial result\n",
			warning("WARN"), r.MaxResumptions)
	}

	return result, nil
}

// runHandoffCycle runs create_handoff followed by resume_handoff.
func (r *Runner) runHandoffCycle(ctx *RunContext, stageName string, model string, invoker *ClaudeInvoker) (*InvokeResult, error) {
	// Step 1: Create handoff document
	handoffContext := fmt.Sprintf("Stage: %s\nTicket: %s\nGoal: %s\nRun directory: %s",
		stageName, ctx.Ticket, ctx.Goal, ctx.RunDir)

	if r.Debug {
		output.Printf("[DEBUG] Running create_handoff skill...\n")
	}

	_, createErr := invoker.InvokeWithSkill("create_handoff", model, handoffContext)
	if createErr != nil {
		return nil, fmt.Errorf("create_handoff failed: %w", createErr)
	}

	// Step 2: Resume from handoff
	if r.Debug {
		output.Printf("[DEBUG] Running resume_handoff skill...\n")
	}

	resumeResult, resumeErr := invoker.InvokeWithSkill("resume_handoff", model, handoffContext)
	if resumeErr != nil {
		return nil, fmt.Errorf("resume_handoff failed: %w", resumeErr)
	}

	return resumeResult, nil
}

// buildStagePrompt constructs the prompt for a stage.
func (r *Runner) buildStagePrompt(ctx *RunContext, stageName string, stage *config.Stage) (string, error) {
	var basePrompt string

	if stage.PromptFile != "" {
		// Read the prompt file
		promptPath := config.ResolvePromptFile(r.ConfigPath, stage.PromptFile)
		content, err := os.ReadFile(promptPath)
		if err != nil {
			return "", fmt.Errorf("failed to read prompt file %s: %w", promptPath, err)
		}
		basePrompt = string(content)
	} else {
		// For skills, provide context
		basePrompt = ""
	}

	// Inject placeholders
	prompt := r.injectPlaceholders(basePrompt, ctx)

	// Prepend goal file reference
	goalPath := filepath.Join(ctx.RunDir, "goal.md")
	prompt = fmt.Sprintf("First, read the goal file at: %s\n\n%s", goalPath, prompt)

	return prompt, nil
}

// injectPlaceholders replaces template placeholders in the prompt.
func (r *Runner) injectPlaceholders(prompt string, ctx *RunContext) string {
	replacements := map[string]string{
		"{ticket}":  ctx.Ticket,
		"{goal}":    ctx.Goal,
		"{run_dir}": ctx.RunDir,
	}

	result := prompt
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// evaluateStage runs the judge evaluation for a completed stage.
func (r *Runner) evaluateStage(ctx *RunContext, stageName string, stage *config.Stage, result *InvokeResult) (*judge.Result, error) {
	j := judge.NewJudge(r.Config.JudgeModel)
	j.Debug = r.Debug

	judgePrompt := stage.JudgePrompt
	if judgePrompt == "" {
		judgePrompt = r.Config.DefaultJudgePrompt
	}

	evalCtx := &judge.EvaluationContext{
		Goal:           ctx.Goal,
		Ticket:         ctx.Ticket,
		StageName:      stageName,
		JudgePrompt:    judgePrompt,
		CLIOutput:      result.Output,
		OutputFilePath: filepath.Join(ctx.RunDir, stageName+".md"),
	}

	return j.Evaluate(evalCtx)
}

// handleCheckpoint prompts the user to review and continue.
func (r *Runner) handleCheckpoint(ctx *RunContext, stageName string) error {
	warning := color.New(color.FgYellow).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	output.Printf("\n%s Checkpoint reached after stage: %s\n", warning("PAUSE"), bold(stageName))
	output.Printf("    Please review the output in: %s\n", ctx.RunDir)
	output.Printf("    Press Enter to continue, or Ctrl+C to abort...\n")

	reader := bufio.NewReader(os.Stdin)
	_, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("checkpoint aborted: %w", err)
	}

	return nil
}

// writeGoalFile creates the initial goal.md file in the run directory.
func (r *Runner) writeGoalFile(ctx *RunContext) error {
	goalPath := filepath.Join(ctx.RunDir, "goal.md")

	content := fmt.Sprintf(`---
ticket: %s
goal: %s
created: %s
---

# Goal

%s

# Context

Repository: %s
Branch: %s
Working Directory: %s
Run Directory: %s
`, ctx.Ticket, ctx.Goal, time.Now().Format(time.RFC3339),
		ctx.Goal, filepath.Base(ctx.RepoPath), ctx.BranchName, ctx.WorkDir, ctx.RunDir)

	return os.WriteFile(goalPath, []byte(content), 0644)
}

// SetupWorktreeOpts configures worktree creation behavior.
type SetupWorktreeOpts struct {
	// NoFetch disables fetching the main branch from origin before branching.
	NoFetch bool
	// AfterCreate hooks run after a new worktree is created. Each receives the worktree path.
	AfterCreate []func(wtPath string) error
	// BaseDir overrides the default worktree base directory (for testing).
	BaseDir string
}

// SetupWorktree creates a worktree for the run if needed.
// Returns the worktree path and whether it already existed.
func SetupWorktree(ticket string, opts SetupWorktreeOpts) (string, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}

	mgr, err := worktree.NewManager(cwd)
	if err != nil {
		return "", false, err
	}

	if opts.BaseDir != "" {
		mgr.BaseDir = opts.BaseDir
	}
	mgr.FetchBeforeCreate = !opts.NoFetch
	existed := mgr.Exists(ticket)

	wtPath, err := mgr.Create(ticket)
	if err != nil {
		return "", false, err
	}

	// Run post-create hooks only on fresh creation
	if !existed {
		for _, hook := range opts.AfterCreate {
			if err := hook(wtPath); err != nil {
				return wtPath, false, fmt.Errorf("post-create hook failed: %w", err)
			}
		}
	}

	return wtPath, existed, nil
}

// DefaultPostCreateHooks returns the standard hooks to run after worktree creation.
func DefaultPostCreateHooks(repoName string) []func(string) error {
	return []func(string) error{
		thoughtsInitHook(repoName),
	}
}

// thoughtsInitHook returns a hook that runs `humanlayer thoughts init` in the worktree.
func thoughtsInitHook(repoName string) func(string) error {
	return func(wtPath string) error {
		hlPath, err := exec.LookPath("humanlayer")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: humanlayer not found in PATH, skipping thoughts init\n")
			return nil
		}

		cmd := exec.Command(hlPath, "thoughts", "init", "--directory", repoName)
		cmd.Dir = wtPath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: thoughts init failed: %v\n", err)
		}
		return nil
	}
}

// GetRunDir returns the run directory path for a ticket.
func GetRunDir(workDir, ticket string) string {
	return filepath.Join(workDir, "thoughts", "shared", "runs", ticket)
}

// selfAnswer re-invokes Claude with context to answer its own question.
func (r *Runner) selfAnswer(ctx *RunContext, stageName string, stage *config.Stage, previousResult *InvokeResult, question string) (*InvokeResult, error) {
	invoker := NewClaudeInvoker(ctx.WorkDir)
	invoker.Debug = r.Debug
	invoker.Verbose = r.Verbose

	model := stage.Model
	if model == "" {
		model = "sonnet"
	}

	prompt := fmt.Sprintf(`You previously asked a question or presented options while working on a task.

**Original Goal**: %s
**Ticket**: %s

**Question/Prompt You Asked**:
%s

**Your Previous Output** (for context):
%s

---

Based on the original goal and context above, answer your own question and continue with the task. Make reasonable decisions based on:
1. The stated goal and requirements
2. Best practices and common patterns
3. The simplest approach that achieves the goal

Do NOT ask for further clarification. Make a decision and proceed with the implementation.
`, ctx.Goal, ctx.Ticket, question, truncateForContext(previousResult.Output, 5000))

	return invoker.Invoke(prompt, model)
}

// retryWithFeedback re-invokes a stage with evaluation feedback so it can fix its output.
func (r *Runner) retryWithFeedback(ctx *RunContext, stageName string, stage *config.Stage, previousResult *InvokeResult, evalFeedback string) (*InvokeResult, error) {
	invoker := NewClaudeInvoker(ctx.WorkDir)
	invoker.Debug = r.Debug
	invoker.Verbose = r.Verbose

	model := stage.Model
	if model == "" {
		model = "sonnet"
	}

	judgePrompt := stage.JudgePrompt
	if judgePrompt == "" {
		judgePrompt = r.Config.DefaultJudgePrompt
	}

	prompt := fmt.Sprintf(`Your previous attempt at the "%s" stage did not pass evaluation. You need to fix the issues and try again.

**Original Goal**: %s
**Ticket**: %s

**Evaluation Criteria**:
%s

**Why Your Previous Attempt Failed**:
%s

**Your Previous Output** (for context):
%s

---

Fix the issues identified in the evaluation feedback above. Make sure your output fully satisfies all the evaluation criteria. Do NOT ask for clarification — just fix the issues and complete the stage.
`, stageName, ctx.Goal, ctx.Ticket, judgePrompt, evalFeedback, truncateForContext(previousResult.Output, 5000))

	if stage.Skill != "" {
		return invoker.InvokeWithSkill(stage.Skill, model, prompt)
	}
	return invoker.Invoke(prompt, model)
}

// promptHumanForAnswer displays the question and reads human input from stdin.
func (r *Runner) promptHumanForAnswer(question string) (string, error) {
	info := color.New(color.FgCyan).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	output.Printf("\n%s %s\n", info("QUESTION:"), bold(question))
	output.Printf("%s Enter your answer (two blank lines to submit):\n", info(">>>"))

	reader := bufio.NewReader(os.Stdin)
	var lines []string
	blankCount := 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("error reading input: %w", err)
		}

		line = strings.TrimRight(line, "\n\r")

		if line == "" {
			blankCount++
			if blankCount >= 2 {
				break
			}
			lines = append(lines, line)
		} else {
			blankCount = 0
			lines = append(lines, line)
		}
	}

	// Remove trailing blank lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	answer := strings.Join(lines, "\n")
	if answer == "" {
		return "", fmt.Errorf("no input provided")
	}

	return answer, nil
}

// continueWithAnswer re-invokes Claude with the human's answer to continue the task.
func (r *Runner) continueWithAnswer(ctx *RunContext, stageName string, stage *config.Stage, previousResult *InvokeResult, question string, answer string) (*InvokeResult, error) {
	invoker := NewClaudeInvoker(ctx.WorkDir)
	invoker.Debug = r.Debug
	invoker.Verbose = r.Verbose

	model := stage.Model
	if model == "" {
		model = "sonnet"
	}

	prompt := fmt.Sprintf(`You previously asked a question while working on a task. The human has provided their answer.

**Original Goal**: %s
**Ticket**: %s

**Question You Asked**:
%s

**Human's Answer**:
%s

**Your Previous Output** (for context):
%s

---

Continue with the task based on the human's answer. Proceed with the implementation.
`, ctx.Goal, ctx.Ticket, question, answer, truncateForContext(previousResult.Output, 5000))

	return invoker.Invoke(prompt, model)
}

// truncateForContext truncates output to fit within context limits.
func truncateForContext(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}
	half := maxLen / 2
	return output[:half] + "\n\n... [truncated] ...\n\n" + output[len(output)-half:]
}
