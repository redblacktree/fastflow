package runner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustinrasener/fastflow/internal/config"
	"github.com/dustinrasener/fastflow/internal/judge"
	"github.com/dustinrasener/fastflow/internal/state"
	"github.com/dustinrasener/fastflow/internal/worktree"
	"github.com/fatih/color"
)

// Runner orchestrates the execution of pipeline stages.
type Runner struct {
	Config         *config.Config
	ConfigPath     string
	NoReview       bool
	DryRun         bool
	Debug          bool
	Resume         string // "auto", "true", "false", or "force"
	// MaxResumptions is the maximum number of handoff/resume cycles per stage (default: 3).
	MaxResumptions int
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

	fmt.Printf("\n%s Running workflow: %s\n", info(">>>"), bold(ctx.Workflow))
	fmt.Printf("    Goal: %s\n", ctx.Goal)
	fmt.Printf("    Ticket: %s\n", ctx.Ticket)
	fmt.Printf("    Working directory: %s\n", ctx.WorkDir)
	fmt.Printf("    Run directory: %s\n", ctx.RunDir)

	// Determine resume behavior
	shouldResume, pipelineState, err := r.determineResumeState(ctx, workflow)
	if err != nil {
		return err
	}

	if shouldResume && pipelineState != nil {
		fmt.Printf("    Resume mode: %s (skipping %d completed stages)\n\n",
			success("ENABLED"), len(pipelineState.CompletedStages))
	} else {
		fmt.Printf("    Resume mode: %s\n\n", warning("DISABLED"))
	}

	// Create the run directory
	if err := os.MkdirAll(ctx.RunDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	// Initialize or use existing state
	if pipelineState == nil {
		pipelineState = state.NewState(ctx.Workflow, workflow.Stages)
	}

	// Write the goal file (always, to ensure it's current)
	if err := r.writeGoalFile(ctx); err != nil {
		return fmt.Errorf("failed to write goal file: %w", err)
	}

	// Check if all stages are already complete
	if shouldResume && len(pipelineState.CompletedStages) == len(workflow.Stages) {
		fmt.Printf("%s All stages already complete!\n", success("SUCCESS"))
		fmt.Printf("    To re-run from scratch, use: --resume=false\n")
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
			fmt.Printf("%s Stage %d/%d: %s %s\n", info(">>>"), i+1, len(workflow.Stages),
				bold(stageName), success("[SKIPPED - already complete]"))
			continue
		}

		fmt.Printf("%s Stage %d/%d: %s\n", info(">>>"), i+1, len(workflow.Stages), bold(stageName))

		if r.DryRun {
			fmt.Printf("    [DRY RUN] Would execute stage: %s\n", stageName)
			continue
		}

		// Execute the stage
		result, err := r.executeStage(ctx, stageName, stage)
		if err != nil {
			fmt.Printf("    %s Stage failed: %v\n", errColor("ERROR"), err)
			return err
		}

		// Run judge evaluation
		judgeResult, err := r.evaluateStage(ctx, stageName, stage, result)
		if err != nil {
			fmt.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
			return err
		}

		if !judgeResult.Success {
			fmt.Printf("    %s Stage did not pass evaluation\n", errColor("FAILED"))
			fmt.Printf("    Reason: %s\n", judgeResult.Reasoning)
			return fmt.Errorf("stage %s failed evaluation: %s", stageName, judgeResult.Reasoning)
		}

		// Mark stage as complete
		if err := pipelineState.MarkStageComplete(ctx.RunDir, stageName); err != nil {
			fmt.Printf("    %s Failed to save state: %v\n", warning("WARN"), err)
		}

		fmt.Printf("    %s Stage completed successfully\n", success("PASS"))
		fmt.Printf("    %s\n\n", judgeResult.Reasoning)

		// Handle checkpoint
		if stage.Checkpoint && !r.NoReview {
			if err := r.handleCheckpoint(ctx, stageName); err != nil {
				return err
			}
		}
	}

	fmt.Printf("\n%s Pipeline completed successfully!\n", success("SUCCESS"))
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
		if existingState.ConfigHash != currentHash {
			return false, nil, fmt.Errorf(
				"%s Config has changed since last run (workflow or stages modified).\n"+
					"    Use --resume=false to start fresh, or --resume=force to continue anyway",
				errColor("ERROR"))
		}
		if existingState.Workflow != ctx.Workflow {
			return false, nil, fmt.Errorf(
				"%s Workflow changed from %q to %q.\n"+
					"    Use --resume=false to start fresh, or --resume=force to continue anyway",
				errColor("ERROR"), existingState.Workflow, ctx.Workflow)
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
		fmt.Printf("    %s Max turns reached, attempting handoff/resume (%d/%d)...\n",
			warning("RESUME"), resumptions, r.MaxResumptions)

		// Run create_handoff to save state
		handoffResult, handoffErr := r.runHandoffCycle(ctx, stageName, model, invoker)
		if handoffErr != nil {
			if r.Debug {
				fmt.Printf("[DEBUG] Handoff/resume failed: %v\n", handoffErr)
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
		fmt.Printf("    %s Max resumptions (%d) reached, proceeding with partial result\n",
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
		fmt.Printf("[DEBUG] Running create_handoff skill...\n")
	}

	_, createErr := invoker.InvokeWithSkill("create_handoff", model, handoffContext)
	if createErr != nil {
		return nil, fmt.Errorf("create_handoff failed: %w", createErr)
	}

	// Step 2: Resume from handoff
	if r.Debug {
		fmt.Printf("[DEBUG] Running resume_handoff skill...\n")
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

	fmt.Printf("\n%s Checkpoint reached after stage: %s\n", warning("PAUSE"), bold(stageName))
	fmt.Printf("    Please review the output in: %s\n", ctx.RunDir)
	fmt.Printf("    Press Enter to continue, or Ctrl+C to abort...\n")

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

// SetupWorktree creates a worktree for the run if needed.
func SetupWorktree(ticket string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	mgr, err := worktree.NewManager(cwd)
	if err != nil {
		return "", err
	}

	return mgr.Create(ticket)
}

// GetRunDir returns the run directory path for a ticket.
func GetRunDir(workDir, ticket string) string {
	return filepath.Join(workDir, "thoughts", "shared", "runs", ticket)
}
