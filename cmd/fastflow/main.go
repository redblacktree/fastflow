// Package main provides the fastflow CLI entry point.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dustinrasener/fastflow/internal/config"
	"github.com/dustinrasener/fastflow/internal/runner"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var version = "0.1.0"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "fastflow",
	Short: "Orchestrate multi-agent development workflows",
	Long: `fastflow automates the full development workflow:
Goal → Worktree → Research → Plan → Implement → Validate → Commit/PR

Each stage runs in a fresh Claude Code context, communicating via
markdown files in thoughts/.`,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a workflow pipeline",
	Long: `Run a complete workflow pipeline from goal to PR.

Examples:
  # Full workflow (default): research → plan → implement → validate → commit
  fastflow run --goal "Add user authentication" --ticket ENG-1234

  # Plan-first workflow: skips research
  fastflow run --goal "Fix typo in README" --ticket ENG-1235 --workflow plan-first

  # Debug workflow: uses debug skill, focused on investigation
  fastflow run --goal "Fix login timeout bug" --ticket ENG-1236 --workflow debug

  # Skip checkpoints (for CI/automation)
  fastflow run --goal "Refactor auth system" --ticket ENG-1237 --no-review`,
	RunE: runRun,
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration and dependencies",
	Long: `Validate the orchestrator configuration file and check that all
required dependencies (prompt files, commands) exist.`,
	RunE: runValidate,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("fastflow version %s\n", version)
	},
}

// Flags
var (
	flagGoal       string
	flagTicket     string
	flagWorkflow   string
	flagNoReview   bool
	flagConfigPath string
	flagDryRun     bool
)

func init() {
	// Run command flags
	runCmd.Flags().StringVar(&flagGoal, "goal", "", "Goal description for the pipeline (required)")
	runCmd.Flags().StringVar(&flagTicket, "ticket", "", "Ticket identifier (required)")
	runCmd.Flags().StringVar(&flagWorkflow, "workflow", "", "Workflow to run (default: from config)")
	runCmd.Flags().BoolVar(&flagNoReview, "no-review", false, "Skip checkpoint pauses")
	runCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")
	runCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would run without executing")

	runCmd.MarkFlagRequired("goal")
	runCmd.MarkFlagRequired("ticket")

	// Validate command flags
	validateCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")

	// Add commands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(versionCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	info := color.New(color.FgCyan).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()

	// Load config
	cfg, err := config.Load(flagConfigPath)
	if err != nil {
		return fmt.Errorf("%s Failed to load config: %w", errColor("ERROR"), err)
	}

	// Validate config
	result := config.Validate(cfg)
	if !result.IsValid() {
		return fmt.Errorf("%s %s", errColor("ERROR"), result.Error())
	}

	// Validate workflow exists
	if flagWorkflow != "" {
		if _, err := cfg.GetWorkflow(flagWorkflow); err != nil {
			return fmt.Errorf("%s %w", errColor("ERROR"), err)
		}
	}

	// Validate dependencies
	depResult := config.ValidateDependencies(cfg)
	if !depResult.IsValid() {
		return fmt.Errorf("%s %s", errColor("ERROR"), depResult.Error())
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Setup worktree (or use current directory)
	workDir := cwd
	branchName := flagTicket

	fmt.Printf("%s Setting up worktree for ticket: %s\n", info(">>>"), flagTicket)
	wtPath, err := runner.SetupWorktree(flagTicket)
	if err != nil {
		fmt.Printf("    Note: Using current directory (worktree creation failed: %v)\n", err)
	} else {
		workDir = wtPath
		fmt.Printf("    Worktree: %s\n", workDir)
	}

	// Create run context
	ctx := &runner.RunContext{
		Goal:       flagGoal,
		Ticket:     flagTicket,
		Workflow:   flagWorkflow,
		WorkDir:    workDir,
		RunDir:     runner.GetRunDir(workDir, flagTicket),
		RepoPath:   cwd,
		BranchName: branchName,
	}

	// Create and run the pipeline
	r := runner.NewRunner(cfg, flagConfigPath)
	r.NoReview = flagNoReview
	r.DryRun = flagDryRun

	return r.Run(ctx)
}

func runValidate(cmd *cobra.Command, args []string) error {
	success := color.New(color.FgGreen).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()
	warning := color.New(color.FgYellow).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	configPath := flagConfigPath
	if configPath == "" {
		configPath = "orchestrator.json"
	}

	fmt.Printf("Validating configuration: %s\n\n", bold(configPath))

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("%s Failed to load config: %v\n", errColor("ERROR"), err)
		return err
	}
	fmt.Printf("%s Configuration file loaded\n", success("OK"))

	// Validate config structure
	result := config.Validate(cfg)
	if !result.IsValid() {
		fmt.Printf("%s Configuration validation failed:\n", errColor("ERROR"))
		for _, e := range result.Errors {
			fmt.Printf("  - %s: %s\n", e.Field, e.Message)
		}
		return fmt.Errorf("configuration validation failed")
	}
	fmt.Printf("%s Configuration structure valid\n", success("OK"))

	// Show workflows
	fmt.Printf("\n%s Workflows:\n", bold("Configured"))
	for name, wf := range cfg.Workflows {
		marker := " "
		if name == cfg.DefaultWorkflow {
			marker = "*"
		}
		fmt.Printf("  %s %s: %s\n", marker, name, wf.Description)
		fmt.Printf("      Stages: %v\n", wf.Stages)
	}

	// Show stages
	fmt.Printf("\n%s Stages:\n", bold("Configured"))
	for name, stage := range cfg.Stages {
		fmt.Printf("  - %s", name)
		if stage.Model != "" {
			fmt.Printf(" (model: %s)", stage.Model)
		}
		if stage.Checkpoint {
			fmt.Printf(" %s", warning("[checkpoint]"))
		}
		fmt.Println()
		if stage.PromptFile != "" {
			fmt.Printf("      Prompt: %s\n", stage.PromptFile)
		}
		if stage.Skill != "" {
			fmt.Printf("      Skill: %s\n", stage.Skill)
		}
	}

	// Validate dependencies
	fmt.Printf("\n%s Checking dependencies...\n", bold(""))
	depResult := config.ValidateDependencies(cfg)
	if !depResult.IsValid() {
		fmt.Printf("%s Dependency validation failed:\n", errColor("ERROR"))
		for _, e := range depResult.Errors {
			fmt.Printf("  - %s: %s\n", e.Field, e.Message)
		}
		return fmt.Errorf("dependency validation failed")
	}
	fmt.Printf("%s All dependencies found\n", success("OK"))

	// Check for claude CLI
	fmt.Printf("\n%s Checking runtime dependencies...\n", bold(""))
	if _, err := exec("which", "claude"); err != nil {
		fmt.Printf("%s Claude CLI not found in PATH\n", warning("WARN"))
	} else {
		fmt.Printf("%s Claude CLI found\n", success("OK"))
	}

	fmt.Printf("\n%s Configuration is valid!\n", success("SUCCESS"))
	return nil
}

// exec is a helper to run a command and check if it exists.
func exec(name string, args ...string) (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	_ = dir // silence unused warning

	// Use exec.LookPath instead
	_, err = os.Stat("/usr/bin/" + name)
	if err == nil {
		return "/usr/bin/" + name, nil
	}
	_, err = os.Stat("/usr/local/bin/" + name)
	if err == nil {
		return "/usr/local/bin/" + name, nil
	}
	return "", fmt.Errorf("command not found: %s", name)
}
