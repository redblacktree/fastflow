// Package main provides the fastflow CLI entry point.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dustinrasener/fastflow/internal/config"
	"github.com/dustinrasener/fastflow/internal/runner"
	"github.com/dustinrasener/fastflow/internal/templates"
	"github.com/dustinrasener/fastflow/internal/worktree"
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

Goal can be provided via (in priority order):
  1. --goal flag:      fastflow run --goal "Add feature" --ticket ENG-1234
  2. --goal-file flag: fastflow run --goal-file goal.txt --ticket ENG-1234
  3. Piped stdin:      echo "Add feature" | fastflow run --ticket ENG-1234
  4. Interactive:      fastflow run --ticket ENG-1234 (prompts for input)

For interactive input, type your goal (multi-line supported) and press
Enter twice (two blank lines) to submit.

Examples:
  # Explicit goal flag
  fastflow run --goal "Add user authentication" --ticket ENG-1234

  # Read goal from file
  fastflow run --goal-file requirements.md --ticket ENG-1235

  # Pipe goal from another command
  cat feature-spec.txt | fastflow run --ticket ENG-1236

  # Interactive mode (will prompt for goal)
  fastflow run --ticket ENG-1237

  # Plan-first workflow
  fastflow run --goal "Fix typo" --ticket ENG-1238 --workflow plan-first

  # Skip checkpoints (for CI/automation)
  fastflow run --goal "Refactor auth" --ticket ENG-1239 --no-review`,
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

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize fastflow in the current directory",
	Long: `Initialize fastflow configuration and supporting files in the current directory.

This creates the following:
  - orchestrator.json - Main configuration file
  - .claude/stages/   - Stage prompt templates
  - .claude/commands/ - Claude Code skill definitions
  - .claude/agents/   - Sub-agent configurations
  - thoughts/shared/  - Runtime directories for plans, research, etc.

Examples:
  # Initialize in current directory
  fastflow init

  # Force overwrite existing files
  fastflow init --force

  # Preview what would be written
  fastflow init --dry-run`,
	RunE: runInit,
}

// Flags
var (
	flagGoal        string
	flagGoalFile    string
	flagTicket      string
	flagWorkflow    string
	flagNoReview    bool
	flagConfigPath  string
	flagDryRun      bool
	flagDebug       bool
	flagResume      string
	flagInteractive bool
	flagInitForce   bool
)

func init() {
	// Run command flags
	runCmd.Flags().StringVar(&flagGoal, "goal", "", "Goal description for the pipeline")
	runCmd.Flags().StringVar(&flagGoalFile, "goal-file", "", "Path to file containing goal description")
	runCmd.Flags().StringVar(&flagTicket, "ticket", "", "Ticket identifier (required)")
	runCmd.Flags().StringVar(&flagWorkflow, "workflow", "", "Workflow to run (default: from config)")
	runCmd.Flags().BoolVar(&flagNoReview, "no-review", false, "Skip checkpoint pauses")
	runCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")
	runCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would run without executing")
	runCmd.Flags().BoolVar(&flagDebug, "debug", false, "Enable verbose debug output")
	runCmd.Flags().StringVar(&flagResume, "resume", "auto", "Resume behavior: auto (default), true, false, or force")
	runCmd.Flags().BoolVarP(&flagInteractive, "interactive", "i", false, "Prompt for human input on interactive questions (default: auto-answer)")

	// Only ticket is required - goal can come from multiple sources
	_ = runCmd.MarkFlagRequired("ticket")

	// Validate command flags
	validateCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")

	// Init command flags
	initCmd.Flags().BoolVar(&flagInitForce, "force", false, "Overwrite existing files")
	initCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview what would be written")

	// Add commands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
}

// resolveGoal determines the goal from various input sources in priority order:
// 1. --goal flag (explicit)
// 2. --goal-file flag (file-based)
// 3. Piped stdin
// 4. Interactive stdin prompt
func resolveGoal() (string, error) {
	// Priority 1: Explicit --goal flag
	if flagGoal != "" {
		return flagGoal, nil
	}

	// Priority 2: --goal-file flag
	if flagGoalFile != "" {
		return readGoalFile(flagGoalFile)
	}

	// Priority 3: Check if stdin has piped data
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Data is being piped in
		return readPipedStdin()
	}

	// Priority 4: Interactive stdin prompt
	return readInteractiveStdin()
}

// readGoalFile reads goal from a file, auto-detecting format.
// Supports plain text or markdown with frontmatter.
func readGoalFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read goal file: %w", err)
	}

	text := string(content)

	// Check for frontmatter (starts with ---)
	if strings.HasPrefix(text, "---") {
		return parseGoalFromFrontmatter(text)
	}

	// Plain text - return as-is (trimmed)
	return strings.TrimSpace(text), nil
}

// parseGoalFromFrontmatter extracts goal from markdown with frontmatter.
// Looks for 'goal:' field in frontmatter, falls back to body content.
func parseGoalFromFrontmatter(content string) (string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || lines[0] != "---" {
		return strings.TrimSpace(content), nil
	}

	// Find end of frontmatter
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		// No closing ---, treat as plain text
		return strings.TrimSpace(content), nil
	}

	// Look for goal: field in frontmatter
	for i := 1; i < endIdx; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "goal:") {
			goalValue := strings.TrimSpace(strings.TrimPrefix(line, "goal:"))
			// Remove quotes if present
			goalValue = strings.Trim(goalValue, "\"'")
			if goalValue != "" {
				return goalValue, nil
			}
		}
	}

	// No goal field found, use body content (after frontmatter)
	body := strings.Join(lines[endIdx+1:], "\n")
	return strings.TrimSpace(body), nil
}

// readPipedStdin reads all data from piped stdin.
func readPipedStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read from stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// readInteractiveStdin prompts user for goal input.
// Two consecutive blank lines signal end of input.
func readInteractiveStdin() (string, error) {
	info := color.New(color.FgCyan).SprintFunc()
	fmt.Printf("%s Enter goal (two blank lines to submit):\n", info(">>>"))

	scanner := bufio.NewScanner(os.Stdin)
	var lines []string
	blankCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			blankCount++
			if blankCount >= 2 {
				// Two consecutive blank lines - submit
				break
			}
			lines = append(lines, line)
		} else {
			blankCount = 0
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading stdin: %w", err)
	}

	// Remove trailing blank lines (we may have added one before detecting the double-blank)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n"), nil
}

func runRun(cmd *cobra.Command, args []string) error {
	info := color.New(color.FgCyan).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()

	// Load config first (needed to check workflow settings)
	cfg, err := config.Load(flagConfigPath)
	if err != nil {
		return fmt.Errorf("%s Failed to load config: %w", errColor("ERROR"), err)
	}

	// Validate config
	result := config.Validate(cfg)
	if !result.IsValid() {
		return fmt.Errorf("%s %s", errColor("ERROR"), result.Error())
	}

	// Get the workflow to check if it requires a goal
	workflowName := flagWorkflow
	if workflowName == "" {
		workflowName = cfg.DefaultWorkflow
	}
	workflow, err := cfg.GetWorkflow(workflowName)
	if err != nil {
		return fmt.Errorf("%s %w", errColor("ERROR"), err)
	}

	// Validate dependencies
	depResult := config.ValidateDependencies(cfg)
	if !depResult.IsValid() {
		return fmt.Errorf("%s %s", errColor("ERROR"), depResult.Error())
	}

	// Resolve goal from various input sources (unless workflow skips goal)
	var goal string
	if !workflow.SkipGoal {
		goal, err = resolveGoal()
		if err != nil {
			return fmt.Errorf("%s Failed to get goal: %w", errColor("ERROR"), err)
		}
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Setup worktree (or use current directory)
	workDir := cwd
	branchName := flagTicket
	worktreeExisted := false

	// Check if worktree already exists
	mgr, err := worktree.NewManager(cwd)
	if err == nil {
		worktreeExisted = mgr.Exists(flagTicket)
	}

	fmt.Printf("%s Setting up worktree for ticket: %s\n", info(">>>"), flagTicket)
	wtPath, err := runner.SetupWorktree(flagTicket)
	if err != nil {
		fmt.Printf("    Note: Using current directory (worktree creation failed: %v)\n", err)
	} else {
		workDir = wtPath
		if worktreeExisted {
			fmt.Printf("    Worktree: %s (existing)\n", workDir)
		} else {
			fmt.Printf("    Worktree: %s (created)\n", workDir)
		}
	}

	// Adjust resume flag for auto mode when worktree is new
	resumeMode := flagResume
	if resumeMode == "auto" && !worktreeExisted {
		resumeMode = "false" // Fresh worktree, no need to check for state
	}

	// Create run context
	ctx := &runner.RunContext{
		Goal:       goal,
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
	r.Debug = flagDebug
	r.Resume = resumeMode
	r.Interactive = flagInteractive

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

func runInit(cmd *cobra.Command, args []string) error {
	success := color.New(color.FgGreen).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()
	warning := color.New(color.FgYellow).SprintFunc()
	info := color.New(color.FgCyan).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	// Get target directory (current directory)
	targetDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("%s Failed to get current directory: %w", errColor("ERROR"), err)
	}

	// Validate target is a git repository
	gitDir := filepath.Join(targetDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return fmt.Errorf("%s %s is not a git repository (no .git directory found)", errColor("ERROR"), targetDir)
	}

	fmt.Printf("%s Initializing fastflow in %s\n\n", info(">>>"), bold(targetDir))

	opts := templates.WriteOptions{
		Force:  flagInitForce,
		DryRun: flagDryRun,
	}

	if flagDryRun {
		fmt.Printf("%s Dry run mode - no files will be written\n\n", warning("NOTE"))
	}

	result, err := templates.Write(targetDir, opts)
	if err != nil {
		return fmt.Errorf("%s Failed to write templates: %w", errColor("ERROR"), err)
	}

	// Print results
	if len(result.Created) > 0 {
		fmt.Printf("%s Created %d files:\n", success("OK"), len(result.Created))
		for _, f := range result.Created {
			fmt.Printf("    %s\n", f)
		}
	}

	if len(result.Overwritten) > 0 {
		fmt.Printf("\n%s Overwritten %d files:\n", warning("OK"), len(result.Overwritten))
		for _, f := range result.Overwritten {
			fmt.Printf("    %s\n", f)
		}
	}

	if len(result.Skipped) > 0 {
		fmt.Printf("\n%s Skipped %d existing files (use --force to overwrite):\n", warning("SKIP"), len(result.Skipped))
		for _, f := range result.Skipped {
			fmt.Printf("    %s\n", f)
		}
	}

	if !flagDryRun {
		fmt.Printf("\n%s Fastflow initialized!\n", success("SUCCESS"))
		fmt.Printf("\n%s Next steps:\n", info(">>>"))
		fmt.Printf("    1. Review orchestrator.json and customize workflows\n")
		fmt.Printf("    2. Run 'fastflow validate' to verify configuration\n")
		fmt.Printf("    3. Run 'fastflow run --goal \"...\" --ticket TICKET-123'\n")
	}

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
