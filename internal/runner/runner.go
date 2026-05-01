package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/redblacktree/fastflow/internal/config"
	"github.com/redblacktree/fastflow/internal/judge"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/state"
	"github.com/redblacktree/fastflow/internal/worktree"
)

const (
	maxInteractionAttempts = 3
	maxEvalRetries         = 2
)

var onCompleteTimeout = 5 * time.Minute

// Runner orchestrates the execution of pipeline stages.
type Runner struct {
	Config     *config.Config
	ConfigPath string
	NoReview   bool
	DryRun     bool
	Debug      bool
	Verbose    bool
	Resume     string // "auto", "true", "false", or "force"
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
	Stage      string // Single stage to run (mutually exclusive with Workflow)
	WorkDir    string
	RunDir     string
	RepoPath   string
	BranchName string
	OnComplete string // Shell command to execute after run completes
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

	// Write PID file
	if err := state.WritePID(ctx.RunDir); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer state.RemovePID(ctx.RunDir)

	// Initialize or use existing state
	if pipelineState == nil {
		pipelineState = state.NewState(ctx.Workflow, workflow.Stages, ctx.Ticket, ctx.WorkDir)
	}

	// Store PID in state and save
	pipelineState.Pid = os.Getpid()
	if ctx.OnComplete != "" {
		pipelineState.OnComplete = ctx.OnComplete
	}
	if err := pipelineState.Save(ctx.RunDir); err != nil {
		return fmt.Errorf("failed to save initial state: %w", err)
	}

	// Install signal handler — write failed status before exiting
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)
	go func() {
		sig := <-sigChan
		handleTerminationSignal(ctx, pipelineState, sig)
		os.Exit(1)
	}()

	// Defer final status write for error exits
	var runErr error
	defer func() {
		if runErr != nil {
			if pipelineState.Status != state.StatusComplete && pipelineState.Status != state.StatusFailed {
				pipelineState.SetFinalStatus(ctx.RunDir, state.StatusFailed, 1, runErr.Error()) //nolint:errcheck
			}
			executeOnComplete(ctx, pipelineState)
		}
	}()

	// Write the goal file (preserves existing if goal unchanged)
	if err := r.writeGoalFile(ctx); err != nil {
		runErr = fmt.Errorf("failed to write goal file: %w", err)
		return runErr
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
			runErr = err
			return runErr
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
			runErr = err
			return runErr
		}

		// Run judge evaluation
		judgeResult, err := r.evaluateStage(ctx, stageName, stage, result)
		if err != nil {
			output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
			runErr = err
			return runErr
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
					runErr = err
					return runErr
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
				runErr = err
				return runErr
			}

			// Re-evaluate
			judgeResult, err = r.evaluateStage(ctx, stageName, stage, result)
			if err != nil {
				output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
				runErr = err
				return runErr
			}
		}

		if judgeResult.Interactive && interactionAttempts >= maxInteractionAttempts {
			output.Printf("    %s Max interaction attempts (%d) reached\n",
				warning("WARN"), maxInteractionAttempts)
			runErr = fmt.Errorf("stage %s still requires input after %d attempts: %s",
				stageName, maxInteractionAttempts, judgeResult.Reasoning)
			return runErr
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
				runErr = err
				return runErr
			}

			judgeResult, err = r.evaluateStage(ctx, stageName, stage, result)
			if err != nil {
				output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
				runErr = err
				return runErr
			}
		}

		if !judgeResult.Success {
			output.Printf("    %s Stage did not pass evaluation after %d retries\n", errColor("FAILED"), maxEvalRetries)
			output.Printf("    Reason: %s\n", judgeResult.Reasoning)
			runErr = fmt.Errorf("stage %s failed evaluation: %s", stageName, judgeResult.Reasoning)
			return runErr
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
				runErr = err
				return runErr
			}
			// Restore running status after checkpoint resumes
			if err := pipelineState.SetStatus(ctx.RunDir, state.StatusRunning); err != nil {
				output.Printf("    %s Failed to update status: %v\n", warning("WARN"), err)
			}
		}
	}

	if err := pipelineState.SetFinalStatus(ctx.RunDir, state.StatusComplete, 0, ""); err != nil {
		output.Printf("    %s Failed to update status: %v\n", warning("WARN"), err)
	}
	executeOnComplete(ctx, pipelineState)
	output.Printf("\n%s Pipeline completed successfully!\n", success("SUCCESS"))
	return nil
}

// RunSingleStage executes a single named stage without workflow state tracking.
// The stage runs exactly once — no judge retry loop, no resume state.
// Checkpoint behavior still applies if the stage has checkpoint: true.
func (r *Runner) RunSingleStage(ctx *RunContext) error {
	stageName := ctx.Stage
	stage, err := r.Config.GetStage(stageName)
	if err != nil {
		return err
	}

	info := color.New(color.FgCyan).SprintFunc()
	success := color.New(color.FgGreen).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	output.Printf("\n%s Running stage: %s\n", info(">>>"), bold(stageName))
	output.Printf("    Ticket: %s\n", ctx.Ticket)
	output.Printf("    Working directory: %s\n", ctx.WorkDir)
	output.Printf("    Run directory: %s\n", ctx.RunDir)
	if ctx.Goal != "" {
		output.Printf("    Goal: %s\n", ctx.Goal)
	}
	output.Println()

	// Create the run directory
	if err := os.MkdirAll(ctx.RunDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	// Write PID file
	if err := state.WritePID(ctx.RunDir); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer state.RemovePID(ctx.RunDir)

	// Load or create minimal state for status tracking
	pipelineState, _ := state.Load(ctx.RunDir)
	if pipelineState == nil {
		pipelineState = state.NewState("", nil, ctx.Ticket, ctx.WorkDir)
	}
	pipelineState.Pid = os.Getpid()
	pipelineState.Stage = stageName
	pipelineState.Status = state.StatusRunning
	if ctx.OnComplete != "" {
		pipelineState.OnComplete = ctx.OnComplete
	}
	pipelineState.Save(ctx.RunDir) //nolint:errcheck

	// Install signal handler — write failed status before exiting
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)
	go func() {
		sig := <-sigChan
		handleTerminationSignal(ctx, pipelineState, sig)
		os.Exit(1)
	}()

	// Defer final status write for error exits
	var runErr error
	defer func() {
		if runErr != nil {
			if pipelineState.Status != state.StatusComplete && pipelineState.Status != state.StatusFailed {
				pipelineState.SetFinalStatus(ctx.RunDir, state.StatusFailed, 1, runErr.Error()) //nolint:errcheck
			}
			executeOnComplete(ctx, pipelineState)
		}
	}()

	// Write goal file only if goal is provided (preserve existing goal.md)
	if ctx.Goal != "" {
		if err := r.writeGoalFile(ctx); err != nil {
			runErr = fmt.Errorf("failed to write goal file: %w", err)
			return runErr
		}
	}

	if r.DryRun {
		output.Printf("    [DRY RUN] Would execute stage: %s\n", stageName)
		return nil
	}

	// Execute the stage
	result, err := r.executeStage(ctx, stageName, stage)
	if err != nil {
		output.Printf("    %s Stage failed: %v\n", errColor("ERROR"), err)
		runErr = err
		return runErr
	}

	// Run judge evaluation (single pass — no retries for single-stage mode)
	judgeResult, err := r.evaluateStage(ctx, stageName, stage, result)
	if err != nil {
		output.Printf("    %s Judge evaluation failed: %v\n", errColor("ERROR"), err)
		runErr = err
		return runErr
	}

	if !judgeResult.Success {
		output.Printf("    %s Stage did not pass evaluation\n", errColor("FAILED"))
		output.Printf("    Reason: %s\n", judgeResult.Reasoning)
		runErr = fmt.Errorf("stage %s failed evaluation: %s", stageName, judgeResult.Reasoning)
		return runErr
	}

	output.Printf("    %s Stage completed successfully\n", success("PASS"))
	output.Printf("    %s\n", judgeResult.Reasoning)

	// Handle checkpoint (still applies in single-stage mode)
	if stage.Checkpoint && !r.NoReview {
		if err := r.handleCheckpoint(ctx, stageName); err != nil {
			runErr = err
			return runErr
		}
	}

	pipelineState.SetFinalStatus(ctx.RunDir, state.StatusComplete, 0, "") //nolint:errcheck
	executeOnComplete(ctx, pipelineState)
	output.Printf("\n%s Stage %s completed!\n", success("SUCCESS"), bold(stageName))
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
	invoker.MaxBudgetUsd = r.Config.EffectiveBudget(stage)

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

	// Handle budget exhaustion with automatic handoff/resume
	budgetResumptions := 0
	for result.HitBudgetCap && budgetResumptions < r.MaxResumptions {
		budgetResumptions++
		warning := color.New(color.FgYellow).SprintFunc()
		output.Printf("    %s Budget exhausted ($%.2f), attempting handoff/resume (%d/%d)...\n",
			warning("BUDGET"), result.TotalCostUsd, budgetResumptions, r.MaxResumptions)

		handoffResult, handoffErr := r.runBudgetHandoffCycle(ctx, stageName, stage, model, invoker)
		if handoffErr != nil {
			if r.Debug {
				output.Printf("[DEBUG] Budget handoff/resume failed: %v\n", handoffErr)
			}
			break
		}

		// Combine outputs
		result.Output += "\n\n--- [RESUMED AFTER BUDGET EXHAUSTION] ---\n\n" + handoffResult.Output
		result.HitBudgetCap = handoffResult.HitBudgetCap
		result.HitMaxTurns = handoffResult.HitMaxTurns
		result.ExitCode = handoffResult.ExitCode
		result.TotalCostUsd += handoffResult.TotalCostUsd
	}

	if result.HitBudgetCap && budgetResumptions >= r.MaxResumptions {
		warning := color.New(color.FgYellow).SprintFunc()
		output.Printf("    %s Max budget resumptions (%d) reached, proceeding with partial result\n",
			warning("WARN"), r.MaxResumptions)
	}

	// Clean up handoff file after budget handoff cycle
	if budgetResumptions > 0 {
		handoffPath := filepath.Join(ctx.WorkDir, ".fastflow", "handoff.md")
		os.Remove(handoffPath) //nolint:errcheck
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

// runBudgetHandoffCycle handles context handoff when budget is exhausted.
// Step 1: --continue on the same session to write .fastflow/handoff.md
// Step 2: Fresh session reads the handoff and continues the original task
func (r *Runner) runBudgetHandoffCycle(ctx *RunContext, stageName string, stage *config.Stage, model string, invoker *ClaudeInvoker) (*InvokeResult, error) {
	handoffPath := filepath.Join(ctx.WorkDir, ".fastflow", "handoff.md")

	// Step 1: Continue the exhausted session to write a handoff document
	if r.Debug {
		output.Printf("[DEBUG] Running --continue to write handoff document...\n")
	}

	handoffPrompt := fmt.Sprintf(`Your budget was exhausted before completing the task. Write a handoff document to %s that captures:

1. **What was accomplished** - summarize completed work
2. **What remains** - list remaining tasks
3. **Current state** - describe where you left off
4. **Critical context** - any decisions, discoveries, or gotchas the next session needs to know
5. **Files modified** - list files you changed or created

Write the handoff document now. This is your only task.`, handoffPath)

	// Ensure .fastflow directory exists
	if err := os.MkdirAll(filepath.Dir(handoffPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create .fastflow directory: %w", err)
	}

	_, createErr := invoker.InvokeContinue(handoffPrompt, model)
	if createErr != nil {
		return nil, fmt.Errorf("handoff continue failed: %w", createErr)
	}

	// Verify handoff file was created
	if _, err := os.Stat(handoffPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("handoff document was not created at %s", handoffPath)
	}

	// Step 2: Fresh session reads handoff and continues the original task
	if r.Debug {
		output.Printf("[DEBUG] Starting fresh session from handoff...\n")
	}

	// Build the original stage prompt
	originalPrompt, err := r.buildStagePrompt(ctx, stageName, stage)
	if err != nil {
		return nil, fmt.Errorf("failed to build stage prompt: %w", err)
	}

	resumePrompt := fmt.Sprintf(`You are resuming work from a previous session that ran out of budget.

**Read the handoff document first**: %s

**Original task prompt**:
%s

Continue the work described in the handoff document. Pick up where the previous session left off.`, handoffPath, originalPrompt)

	// Create a fresh invoker for the new session (same settings)
	freshInvoker := NewClaudeInvoker(ctx.WorkDir)
	freshInvoker.Debug = r.Debug
	freshInvoker.Verbose = r.Verbose
	freshInvoker.MaxBudgetUsd = invoker.MaxBudgetUsd

	var resumeResult *InvokeResult
	if stage.Skill != "" {
		resumeResult, err = freshInvoker.InvokeWithSkill(stage.Skill, model, resumePrompt)
	} else {
		resumeResult, err = freshInvoker.Invoke(resumePrompt, model)
	}
	if err != nil {
		return nil, fmt.Errorf("handoff resume failed: %w", err)
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

// writeGoalFile creates or updates the goal.md file in the run directory.
// If goal.md already exists with a non-empty goal that matches ctx.Goal,
// the file is preserved to retain any user-edited content.
func (r *Runner) writeGoalFile(ctx *RunContext) error {
	goalPath := filepath.Join(ctx.RunDir, "goal.md")

	// If goal.md already exists with a matching non-empty goal, preserve it.
	if existing, err := os.ReadFile(goalPath); err == nil {
		if existingGoal := parseGoalField(string(existing)); existingGoal != "" && ctx.Goal == existingGoal {
			return nil
		}
	}

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

// ReadExistingGoal reads the goal from an existing goal.md in the given directory.
// Returns an empty string if the file doesn't exist or has no goal.
func ReadExistingGoal(runDir string) string {
	goalPath := filepath.Join(runDir, "goal.md")
	content, err := os.ReadFile(goalPath)
	if err != nil {
		return ""
	}
	return parseGoalField(string(content))
}

// parseGoalField extracts the goal: value from goal.md frontmatter content.
// Returns empty string if content has no frontmatter or no non-empty goal field.
func parseGoalField(content string) string {
	if !strings.HasPrefix(content, "---") {
		return ""
	}

	lines := strings.Split(content, "\n")
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return ""
	}

	for i := 1; i < endIdx; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "goal:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "goal:"))
			return strings.Trim(value, "\"'")
		}
	}

	return ""
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

// executeOnComplete runs the on-complete shell command with environment variables
// describing the run result. Errors are logged but do not affect the caller.
// The command is killed after onCompleteTimeout to prevent hung hooks from
// wedging a long-lived or background run.
func executeOnComplete(ctx *RunContext, pState *state.PipelineState) {
	cmdStr := pState.OnComplete
	if cmdStr == "" {
		return
	}

	env := os.Environ()
	env = append(env,
		"FASTFLOW_TICKET="+pState.Ticket,
		"FASTFLOW_STATUS="+pState.Status,
		"FASTFLOW_WORKTREE="+ctx.WorkDir,
		"FASTFLOW_RUN_DIR="+ctx.RunDir,
		"FASTFLOW_ERROR="+pState.Error,
		"FASTFLOW_PR_URL=",
		"FASTFLOW_WORKFLOW="+pState.Workflow,
	)

	execCtx, cancel := context.WithTimeout(context.Background(), onCompleteTimeout)
	defer cancel()

	c := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	err := c.Run()
	if err != nil {
		warning := color.New(color.FgYellow).SprintFunc()
		if execCtx.Err() == context.DeadlineExceeded {
			output.Printf("    %s on-complete command timed out after %s\n",
				warning("WARN"), onCompleteTimeout)
			return
		}
		output.Printf("    %s on-complete command failed: %v\n", warning("WARN"), err)
	}
}

// handleTerminationSignal writes final state, fires the on-complete hook, and
// removes the PID file. Called from signal handler goroutines before os.Exit.
func handleTerminationSignal(ctx *RunContext, pState *state.PipelineState, sig os.Signal) {
	errMsg := fmt.Sprintf("received signal: %s", sig)
	pState.SetFinalStatus(ctx.RunDir, state.StatusFailed, 1, errMsg) //nolint:errcheck
	executeOnComplete(ctx, pState)
	state.RemovePID(ctx.RunDir)
}

// truncateForContext truncates output to fit within context limits.
func truncateForContext(output string, maxLen int) string {
	if len(output) <= maxLen {
		return output
	}
	half := maxLen / 2
	return output[:half] + "\n\n... [truncated] ...\n\n" + output[len(output)-half:]
}
