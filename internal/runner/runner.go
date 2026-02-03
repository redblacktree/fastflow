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
	"github.com/dustinrasener/fastflow/internal/worktree"
	"github.com/fatih/color"
)

// Runner orchestrates the execution of pipeline stages.
type Runner struct {
	Config     *config.Config
	ConfigPath string
	NoReview   bool
	DryRun     bool
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
		Config:     cfg,
		ConfigPath: configPath,
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
	bold := color.New(color.Bold).SprintFunc()

	fmt.Printf("\n%s Running workflow: %s\n", info(">>>"), bold(ctx.Workflow))
	fmt.Printf("    Goal: %s\n", ctx.Goal)
	fmt.Printf("    Ticket: %s\n", ctx.Ticket)
	fmt.Printf("    Working directory: %s\n", ctx.WorkDir)
	fmt.Printf("    Run directory: %s\n\n", ctx.RunDir)

	// Create the run directory
	if err := os.MkdirAll(ctx.RunDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	// Write the goal file
	if err := r.writeGoalFile(ctx); err != nil {
		return fmt.Errorf("failed to write goal file: %w", err)
	}

	// Execute each stage
	for i, stageName := range workflow.Stages {
		stage, err := r.Config.GetStage(stageName)
		if err != nil {
			return err
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

// executeStage runs a single stage.
func (r *Runner) executeStage(ctx *RunContext, stageName string, stage *config.Stage) (*InvokeResult, error) {
	invoker := NewClaudeInvoker(ctx.WorkDir)

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
	if stage.Skill != "" {
		return invoker.InvokeWithSkill(stage.Skill, model, prompt)
	}

	return invoker.Invoke(prompt, model)
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
