// Package main provides the fastflow CLI entry point.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/redblacktree/fastflow/internal/config"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/runner"
	"github.com/redblacktree/fastflow/internal/state"
	"github.com/redblacktree/fastflow/internal/templates"
	"github.com/redblacktree/fastflow/internal/worktree"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// version is set by goreleaser via -ldflags "-X main.version=...".
// Must be a plain string constant for -X to work.
var version = "dev"

func init() {
	// For go install builds, read the version stamped by the Go toolchain.
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	// rootCmd.Version must be set here because the var initializer runs
	// before init(), so the command would capture "dev" otherwise.
	rootCmd.Version = version
}

func main() {
	defer output.Close()
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
  fastflow run --goal "Refactor auth" --ticket ENG-1239 --no-review

  # Run a single stage
  fastflow run --ticket ENG-1240 --stage plan --goal "Implement feature X"

  # Re-run a stage (reads existing goal from run directory)
  fastflow run --ticket ENG-1240 --stage plan

  # Force run even if another run is active (recovery)
  fastflow run --ticket ENG-1241 --force`,
	RunE: runRun,
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration and dependencies",
	Long: `Validate the orchestrator configuration file and check that all
required dependencies (prompt files, commands) exist.`,
	RunE: runValidate,
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

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List runs and their status",
	Long: `List all runs for the current repository with their status and ticket summaries.

Shows both worktree-based runs and runs started with --no-worktree.

Examples:
  # List all runs
  fastflow list

  # List only tickets matching a prefix
  fastflow list --prefix FAS-
  fastflow list --prefix ENG-`,
	RunE: runList,
}

var cleanCmd = &cobra.Command{
	Use:   "clean [ticket]",
	Short: "Remove worktrees",
	Long: `Remove git worktrees for completed tickets.

Examples:
  # Remove a specific worktree
  fastflow clean FAS-001

  # Remove all worktrees matching a prefix (requires confirmation)
  fastflow clean --prefix FAS-

  # Remove without confirmation
  fastflow clean --prefix FAS- --force`,
	RunE: runClean,
}

// Flags
var (
	flagGoal        string
	flagGoalFile    string
	flagTicket      string
	flagWorkflow    string
	flagStage       string
	flagNoReview    bool
	flagConfigPath  string
	flagDryRun      bool
	flagDebug       bool
	flagResume      string
	flagInteractive bool
	flagInitForce   bool
	flagListPrefix  string
	flagCleanPrefix string
	flagCleanForce  bool
	flagNoWorktree  bool
	flagNoFetch      bool
	flagVerbose      bool
	flagLogFile      string
	flagNoColor      bool
	flagRunForce     bool
	flagOnComplete   string
	flagBackground   bool
)

func init() {
	// Persistent flags (available to all commands)
	rootCmd.PersistentFlags().StringVar(&flagLogFile, "log-file", "", "Write output to a log file (ANSI codes stripped)")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "Disable colored output")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if flagNoColor {
			color.NoColor = true
		}
		return output.Setup(flagLogFile)
	}

	// Run command flags
	runCmd.Flags().StringVar(&flagGoal, "goal", "", "Goal description for the pipeline")
	runCmd.Flags().StringVar(&flagGoalFile, "goal-file", "", "Path to file containing goal description")
	runCmd.Flags().StringVar(&flagTicket, "ticket", "", "Ticket identifier (required)")
	runCmd.Flags().StringVar(&flagWorkflow, "workflow", "", "Workflow to run (default: from config)")
	runCmd.Flags().StringVar(&flagStage, "stage", "", "Run a single named stage (mutually exclusive with --workflow)")
	runCmd.Flags().BoolVar(&flagNoReview, "no-review", false, "Skip checkpoint pauses")
	runCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")
	runCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would run without executing")
	runCmd.Flags().BoolVar(&flagDebug, "debug", false, "Enable verbose debug output")
	runCmd.Flags().StringVar(&flagResume, "resume", "auto", "Resume behavior: auto (default), true, false, or force")
	runCmd.Flags().BoolVarP(&flagInteractive, "interactive", "i", false, "Prompt for human input on interactive questions (default: auto-answer)")
	runCmd.Flags().BoolVar(&flagNoWorktree, "no-worktree", false, "Use current directory instead of creating worktree")
	runCmd.Flags().BoolVar(&flagNoFetch, "no-fetch", false, "Skip fetching main branch from origin before creating worktree")
	runCmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Show tool activity during stage execution")
	runCmd.Flags().BoolVar(&flagRunForce, "force", false, "Bypass duplicate run detection (use for recovery)")
	runCmd.Flags().StringVar(&flagOnComplete, "on-complete", "", "Shell command to run after completion (receives FASTFLOW_* env vars)")
	runCmd.Flags().BoolVar(&flagBackground, "background", false, "Run in background (detach from terminal)")

	// Only ticket is required - goal can come from multiple sources
	_ = runCmd.MarkFlagRequired("ticket")

	// Validate command flags
	validateCmd.Flags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: orchestrator.json)")

	// Init command flags
	initCmd.Flags().BoolVar(&flagInitForce, "force", false, "Overwrite existing files")
	initCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview what would be written")

	// List command flags
	listCmd.Flags().StringVar(&flagListPrefix, "prefix", "", "Filter tickets by prefix (e.g., FAS-, ENG-)")

	// Clean command flags
	cleanCmd.Flags().StringVar(&flagCleanPrefix, "prefix", "", "Clean all worktrees matching prefix")
	cleanCmd.Flags().BoolVar(&flagCleanForce, "force", false, "Skip confirmation prompt")

	// Add commands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(cleanCmd)
}

// resolveGoal determines the goal from various input sources in priority order:
// 1. --goal flag (explicit)
// 2. --goal-file flag (file-based)
// 3. Existing goal.md in run directory
// 4. Piped stdin
// 5. Interactive stdin prompt
func resolveGoal(runDir string) (string, error) {
	// Priority 1: Explicit --goal flag
	if flagGoal != "" {
		return flagGoal, nil
	}

	// Priority 2: --goal-file flag
	if flagGoalFile != "" {
		return readGoalFile(flagGoalFile)
	}

	// Priority 3: Existing goal.md in run directory
	if existing := runner.ReadExistingGoal(runDir); existing != "" {
		return existing, nil
	}

	// Priority 4: Check if stdin has piped data
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Data is being piped in
		return readPipedStdin()
	}

	// Priority 5: Interactive stdin prompt
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
	output.Printf("%s Enter goal (two blank lines to submit):\n", info(">>>"))

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
	warning := color.New(color.FgYellow).SprintFunc()

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

	// Handle --stage flag (mutually exclusive with --workflow)
	if flagStage != "" {
		if flagWorkflow != "" {
			return fmt.Errorf("%s --stage and --workflow are mutually exclusive", errColor("ERROR"))
		}
		// Validate stage exists
		if _, err := cfg.GetStage(flagStage); err != nil {
			available := cfg.StageNames()
			return fmt.Errorf("%s stage %q not found (available stages: %s)",
				errColor("ERROR"), flagStage, strings.Join(available, ", "))
		}
	}

	// Workflow resolution — only when NOT in --stage mode
	var workflow *config.Workflow
	if flagStage == "" {
		workflowName := flagWorkflow
		if workflowName == "" {
			workflowName = cfg.DefaultWorkflow
		}
		workflow, err = cfg.GetWorkflow(workflowName)
		if err != nil {
			return fmt.Errorf("%s %w", errColor("ERROR"), err)
		}
	}

	// Validate dependencies (skip for --stage since we only need one stage)
	if flagStage == "" {
		depResult := config.ValidateDependencies(cfg)
		if !depResult.IsValid() {
			return fmt.Errorf("%s %s", errColor("ERROR"), depResult.Error())
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

	if flagNoWorktree {
		// User explicitly requested no worktree
		output.Printf("%s Using current directory (--no-worktree)\n", info(">>>"))
		// Get current branch name for context
		branchName = getCurrentBranch(cwd)
		if branchName == "" {
			branchName = flagTicket
		}
	} else {
		// Build setup options
		mgr, err := worktree.NewManager(cwd)
		opts := runner.SetupWorktreeOpts{
			NoFetch: flagNoFetch,
		}
		if err == nil && !mgr.Exists(flagTicket) {
			opts.AfterCreate = runner.DefaultPostCreateHooks(mgr.RepoName)
		}

		output.Printf("%s Setting up worktree for ticket: %s\n", info(">>>"), flagTicket)
		wtPath, existed, err := runner.SetupWorktree(flagTicket, opts)
		if err != nil {
			output.Printf("    Note: Using current directory (worktree creation failed: %v)\n", err)
		} else {
			workDir = wtPath
			worktreeExisted = existed
			if existed {
				output.Printf("    Worktree: %s (existing)\n", workDir)
			} else {
				output.Printf("    Worktree: %s (created)\n", workDir)
			}
		}
	}

	// Adjust resume flag for auto mode when worktree is new
	resumeMode := flagResume
	if resumeMode == "auto" && !worktreeExisted {
		resumeMode = "false" // Fresh worktree, no need to check for state
	}

	// Compute run directory (needs workDir from worktree setup)
	runDir := runner.GetRunDir(workDir, flagTicket)

	// Check for duplicate active run
	if !flagRunForce {
		checkResult, err := state.CheckActiveRun(runDir)
		if err != nil {
			var activeErr *state.ActiveRunError
			if errors.As(err, &activeErr) {
				return fmt.Errorf("%s Run already active for ticket %s (pid %d). Use --force to override",
					errColor("ERROR"), flagTicket, activeErr.Pid)
			}
			return fmt.Errorf("%s Failed to check for active run: %w", errColor("ERROR"), err)
		}
		if checkResult != nil && checkResult.StalePID > 0 {
			output.Printf("%s Cleaned up stale PID file (pid %d was no longer running)\n",
				warning("WARNING"), checkResult.StalePID)
		}
	} else {
		output.Printf("%s Skipping duplicate run check (--force)\n", warning("WARNING"))
	}

	// Resolve goal from various input sources
	var goal string
	if flagStage != "" {
		// Goal is optional for --stage mode — don't prompt interactively
		if flagGoal != "" {
			goal = flagGoal
		} else if flagGoalFile != "" {
			goal, err = readGoalFile(flagGoalFile)
			if err != nil {
				return fmt.Errorf("%s Failed to get goal: %w", errColor("ERROR"), err)
			}
		} else {
			// Check piped stdin
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				goal, _ = readPipedStdin()
			}
			// Otherwise goal stays empty — that's OK for --stage
		}
	} else if !workflow.SkipGoal {
		goal, err = resolveGoal(runDir)
		if err != nil {
			return fmt.Errorf("%s Failed to get goal: %w", errColor("ERROR"), err)
		}
	}

	// Handle --background: re-exec as a detached child process
	if flagBackground && os.Getenv("FASTFLOW_DAEMONIZED") != "1" {
		// Ensure run directory exists for log file
		if err := os.MkdirAll(runDir, 0755); err != nil {
			return fmt.Errorf("%s Failed to create run directory: %w", errColor("ERROR"), err)
		}

		logPath := filepath.Join(runDir, "fastflow.log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("%s Failed to create log file: %w", errColor("ERROR"), err)
		}

		// Build child command with same args plus daemonize marker
		childEnv := append(os.Environ(), "FASTFLOW_DAEMONIZED=1")

		// If goal was resolved interactively (not from --goal or --goal-file),
		// pass it explicitly so the child doesn't need stdin
		childArgs := make([]string, len(os.Args[1:]))
		copy(childArgs, os.Args[1:])
		if goal != "" && flagGoal == "" && flagGoalFile == "" {
			childArgs = append(childArgs, "--goal", goal)
		}

		childCmd := exec.Command(os.Args[0], childArgs...)
		childCmd.Env = childEnv
		childCmd.Stdout = logFile
		childCmd.Stderr = logFile
		childCmd.Stdin = nil
		childCmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}

		if err := childCmd.Start(); err != nil {
			logFile.Close()
			return fmt.Errorf("%s Failed to start background process: %w", errColor("ERROR"), err)
		}

		childPid := childCmd.Process.Pid
		logFile.Close()

		// Don't wait for child — it's detached
		childCmd.Process.Release()

		output.Printf("%s fastflow running in background (PID %d)\n", info(">>>"), childPid)
		output.Printf("    Log: %s\n", logPath)
		return nil
	}

	// Create run context
	ctx := &runner.RunContext{
		Goal:       goal,
		Ticket:     flagTicket,
		Workflow:   flagWorkflow,
		Stage:      flagStage,
		WorkDir:    workDir,
		RunDir:     runDir,
		RepoPath:   cwd,
		BranchName: branchName,
		OnComplete: flagOnComplete,
	}

	// Create and run the pipeline
	r := runner.NewRunner(cfg, flagConfigPath)
	r.NoReview = flagNoReview
	r.DryRun = flagDryRun
	r.Debug = flagDebug
	r.Resume = resumeMode
	r.Interactive = flagInteractive
	r.Verbose = flagVerbose

	if ctx.Stage != "" {
		return r.RunSingleStage(ctx)
	}
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

	output.Printf("Validating configuration: %s\n\n", bold(configPath))

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		output.Printf("%s Failed to load config: %v\n", errColor("ERROR"), err)
		return err
	}
	output.Printf("%s Configuration file loaded\n", success("OK"))

	// Validate config structure
	result := config.Validate(cfg)
	if !result.IsValid() {
		output.Printf("%s Configuration validation failed:\n", errColor("ERROR"))
		for _, e := range result.Errors {
			output.Printf("  - %s: %s\n", e.Field, e.Message)
		}
		return fmt.Errorf("configuration validation failed")
	}
	output.Printf("%s Configuration structure valid\n", success("OK"))

	// Show workflows
	output.Printf("\n%s Workflows:\n", bold("Configured"))
	for name, wf := range cfg.Workflows {
		marker := " "
		if name == cfg.DefaultWorkflow {
			marker = "*"
		}
		output.Printf("  %s %s: %s\n", marker, name, wf.Description)
		output.Printf("      Stages: %v\n", wf.Stages)
	}

	// Show stages
	output.Printf("\n%s Stages:\n", bold("Configured"))
	for name, stage := range cfg.Stages {
		output.Printf("  - %s", name)
		if stage.Model != "" {
			output.Printf(" (model: %s)", stage.Model)
		}
		if stage.Checkpoint {
			output.Printf(" %s", warning("[checkpoint]"))
		}
		output.Println()
		if stage.PromptFile != "" {
			output.Printf("      Prompt: %s\n", stage.PromptFile)
		}
		if stage.Skill != "" {
			output.Printf("      Skill: %s\n", stage.Skill)
		}
	}

	// Validate dependencies
	output.Printf("\n%s Checking dependencies...\n", bold(""))
	depResult := config.ValidateDependencies(cfg)
	if !depResult.IsValid() {
		output.Printf("%s Dependency validation failed:\n", errColor("ERROR"))
		for _, e := range depResult.Errors {
			output.Printf("  - %s: %s\n", e.Field, e.Message)
		}
		return fmt.Errorf("dependency validation failed")
	}
	output.Printf("%s All dependencies found\n", success("OK"))

	// Check for claude CLI
	output.Printf("\n%s Checking runtime dependencies...\n", bold(""))
	if _, err := findExecutable("which", "claude"); err != nil {
		output.Printf("%s Claude CLI not found in PATH\n", warning("WARN"))
	} else {
		output.Printf("%s Claude CLI found\n", success("OK"))
	}

	output.Printf("\n%s Configuration is valid!\n", success("SUCCESS"))
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

	output.Printf("%s Initializing fastflow in %s\n\n", info(">>>"), bold(targetDir))

	opts := templates.WriteOptions{
		Force:  flagInitForce,
		DryRun: flagDryRun,
	}

	if flagDryRun {
		output.Printf("%s Dry run mode - no files will be written\n\n", warning("NOTE"))
	}

	result, err := templates.Write(targetDir, opts)
	if err != nil {
		return fmt.Errorf("%s Failed to write templates: %w", errColor("ERROR"), err)
	}

	// Print results
	if len(result.Created) > 0 {
		output.Printf("%s Created %d files:\n", success("OK"), len(result.Created))
		for _, f := range result.Created {
			output.Printf("    %s\n", f)
		}
	}

	if len(result.Overwritten) > 0 {
		output.Printf("\n%s Overwritten %d files:\n", warning("OK"), len(result.Overwritten))
		for _, f := range result.Overwritten {
			output.Printf("    %s\n", f)
		}
	}

	if len(result.Skipped) > 0 {
		output.Printf("\n%s Skipped %d existing files (use --force to overwrite):\n", warning("SKIP"), len(result.Skipped))
		for _, f := range result.Skipped {
			output.Printf("    %s\n", f)
		}
	}

	if !flagDryRun {
		output.Printf("\n%s Fastflow initialized!\n", success("SUCCESS"))
		output.Printf("\n%s Next steps:\n", info(">>>"))
		output.Printf("    1. Review orchestrator.json and customize workflows\n")
		output.Printf("    2. Run 'fastflow validate' to verify configuration\n")
		output.Printf("    3. Run 'fastflow run --goal \"...\" --ticket TICKET-123'\n")
	}

	return nil
}

// findExecutable is a helper to run a command and check if it exists.
func findExecutable(name string, args ...string) (string, error) {
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

func runList(cmd *cobra.Command, args []string) error {
	bold := color.New(color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// --- Source 1: Worktree-based runs ---
	type listEntry struct {
		Ticket  string
		Status  string
		Stage   string
		Created string
		Summary string
		Pid     int
		LogPath string
	}

	seen := make(map[string]bool)
	var entries []listEntry

	mgr, err := worktree.NewManager(cwd)
	if err == nil {
		worktrees, err := mgr.List()
		if err == nil {
			for _, wt := range worktrees {
				status := "active"
				if wt.IsOrphan {
					status = "orphaned"
				}

				created := ""
				summary := dim("(no goal file)")
				stage := ""

				// Try to read ticket info from goal.md
				for _, loc := range []string{wt.Path, cwd} {
					if info := worktree.ReadTicketInfo(loc, wt.Ticket); info != nil {
						if info.Created != "" {
							created = info.Created
							if len(created) > 19 {
								created = created[:19]
							}
							created = strings.Replace(created, "T", " ", 1)
						}
						if info.Goal != "" {
							summary = info.Goal
							if len(summary) > 40 {
								summary = summary[:37] + "..."
							}
						}
						break
					}
				}

				// Try to read state.json for richer status
				runDir := runner.GetRunDir(wt.Path, wt.Ticket)
				var entryPid int
				if st, loadErr := state.Load(runDir); loadErr == nil && st != nil && st.Status != "" {
					status = st.Status
					stage = st.Stage
					// Detect stale runs: status is running but process is dead
					if status == state.StatusRunning {
						pid, _ := state.ReadPID(runDir)
						if pid > 0 {
							if !state.IsProcessAlive(pid) {
								status = "stale"
							} else {
								entryPid = pid
							}
						}
					}
				}

				var entryLogPath string
				logPath := filepath.Join(runDir, "fastflow.log")
				if _, statErr := os.Stat(logPath); statErr == nil {
					entryLogPath = logPath
				}

				entries = append(entries, listEntry{
					Ticket:  wt.Ticket,
					Status:  status,
					Stage:   stage,
					Created: created,
					Summary: summary,
					Pid:     entryPid,
					LogPath: entryLogPath,
				})
				seen[wt.Ticket] = true
			}
		}
	}

	// --- Source 2: State-based runs (non-worktree) from current repo ---
	stateRuns, _ := state.ScanRunDirs(cwd)
	for _, run := range stateRuns {
		if seen[run.Ticket] {
			continue // Already listed via worktree
		}

		status := run.State.Status
		if status == "" {
			status = "unknown"
		}
		// Detect stale runs: status is running but process is dead
		var entryPid int
		if status == state.StatusRunning {
			pid, _ := state.ReadPID(run.RunDir)
			if pid > 0 {
				if !state.IsProcessAlive(pid) {
					status = "stale"
				} else {
					entryPid = pid
				}
			}
		}
		stage := run.State.Stage
		created := ""
		summary := dim("(no goal file)")

		// Read goal info
		if info := worktree.ReadTicketInfo(cwd, run.Ticket); info != nil {
			if info.Created != "" {
				created = info.Created
				if len(created) > 19 {
					created = created[:19]
				}
				created = strings.Replace(created, "T", " ", 1)
			}
			if info.Goal != "" {
				summary = info.Goal
				if len(summary) > 40 {
					summary = summary[:37] + "..."
				}
			}
		}

		var entryLogPath string
		logPath := filepath.Join(run.RunDir, "fastflow.log")
		if _, statErr := os.Stat(logPath); statErr == nil {
			entryLogPath = logPath
		}

		entries = append(entries, listEntry{
			Ticket:  run.Ticket,
			Status:  status,
			Stage:   stage,
			Created: created,
			Summary: summary,
			Pid:     entryPid,
			LogPath: entryLogPath,
		})
		seen[run.Ticket] = true
	}

	// Filter by prefix if specified
	if flagListPrefix != "" {
		var filtered []listEntry
		for _, e := range entries {
			if strings.HasPrefix(e.Ticket, flagListPrefix) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if len(entries) == 0 {
		if flagListPrefix != "" {
			output.Printf("No runs found matching prefix: %s\n", flagListPrefix)
		} else {
			output.Printf("No runs found.\n")
		}
		return nil
	}

	// Print header
	output.Printf("%s  %s  %s  %s  %s\n",
		bold(fmt.Sprintf("%-12s", "Ticket")),
		bold(fmt.Sprintf("%-12s", "Status")),
		bold(fmt.Sprintf("%-12s", "Stage")),
		bold(fmt.Sprintf("%-20s", "Created")),
		bold("Summary"))
	output.Printf("%s  %s  %s  %s  %s\n",
		strings.Repeat("─", 12),
		strings.Repeat("─", 12),
		strings.Repeat("─", 12),
		strings.Repeat("─", 20),
		strings.Repeat("─", 40))

	// Print each entry
	for _, e := range entries {
		statusFmt := e.Status
		switch e.Status {
		case "stale":
			statusFmt = color.New(color.FgRed).Sprint(e.Status)
		case state.StatusFailed:
			statusFmt = color.New(color.FgRed).Sprint(e.Status)
		case state.StatusComplete:
			statusFmt = color.New(color.FgGreen).Sprint(e.Status)
		case state.StatusRunning:
			statusFmt = color.New(color.FgCyan).Sprint(e.Status)
		}
		pidStr := ""
		if e.Pid > 0 {
			pidStr = fmt.Sprintf(" (pid %d)", e.Pid)
		}
		output.Printf("%-12s  %-12s  %-12s  %-20s  %s%s\n",
			e.Ticket, statusFmt, e.Stage, e.Created, e.Summary, pidStr)
		if e.LogPath != "" {
			output.Printf("%-12s  %s\n", "", dim("Log: "+e.LogPath))
		}
	}

	output.Printf("\n%d run(s) found\n", len(entries))
	return nil
}

func runClean(cmd *cobra.Command, args []string) error {
	success := color.New(color.FgGreen).SprintFunc()
	errColor := color.New(color.FgRed).SprintFunc()
	warning := color.New(color.FgYellow).SprintFunc()

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Create worktree manager
	mgr, err := worktree.NewManager(cwd)
	if err != nil {
		return fmt.Errorf("failed to create worktree manager: %w", err)
	}

	// Determine what to clean
	var toClean []worktree.WorktreeInfo

	if len(args) > 0 {
		// Clean specific ticket
		ticket := args[0]
		if !mgr.Exists(ticket) {
			return fmt.Errorf("worktree not found: %s", ticket)
		}
		toClean = append(toClean, worktree.WorktreeInfo{
			Ticket: ticket,
			Path:   mgr.WorktreePath(ticket),
		})
	} else if flagCleanPrefix != "" {
		// Clean by prefix
		worktrees, err := mgr.List()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}
		for _, wt := range worktrees {
			if strings.HasPrefix(wt.Ticket, flagCleanPrefix) {
				toClean = append(toClean, wt)
			}
		}
		if len(toClean) == 0 {
			output.Printf("No worktrees found matching prefix: %s\n", flagCleanPrefix)
			return nil
		}
	} else {
		return fmt.Errorf("specify a ticket or use --prefix to select worktrees to clean")
	}

	// Confirm if not forced
	if !flagCleanForce && len(toClean) > 0 {
		output.Printf("%s The following worktrees will be removed:\n", warning("WARNING"))
		for _, wt := range toClean {
			output.Printf("  - %s (%s)\n", wt.Ticket, wt.Path)
		}
		output.Printf("\nProceed? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			output.Println("Aborted.")
			return nil
		}
	}

	// Remove worktrees
	var removed, failed int
	for _, wt := range toClean {
		if err := mgr.Remove(wt.Ticket); err != nil {
			output.Printf("%s Failed to remove %s: %v\n", errColor("ERROR"), wt.Ticket, err)
			failed++
		} else {
			output.Printf("%s Removed %s\n", success("OK"), wt.Ticket)
			removed++
		}
	}

	output.Printf("\n%d worktree(s) removed", removed)
	if failed > 0 {
		output.Printf(", %d failed", failed)
	}
	output.Println()

	return nil
}

// getCurrentBranch returns the current git branch name.
func getCurrentBranch(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
