# --resume Option Implementation Plan

## Overview

Add a `--resume` flag to fastflow that automatically resumes from the last successful stage when an existing worktree is detected. This enables recovery from failures without re-running completed stages.

## Current State Analysis

### What Exists Now
- **Worktree management** (`internal/worktree/worktree.go:54-57`): Already idempotent - `Create()` returns existing path if worktree exists
- **No state tracking**: Pipeline always starts from stage 1, even with existing worktree
- **Artifacts persist**: Goal file, stage outputs, and code changes survive failures
- **Run directory**: `{worktree}/thoughts/shared/runs/{ticket}/`

### What's Missing
- State tracking for completed stages
- Logic to skip completed stages on resume
- CLI flag for controlling resume behavior
- Worktree existence detection method

## Desired End State

After implementation:
1. `fastflow run --ticket X --goal "..."` with existing worktree → auto-resumes from last completed stage
2. `--resume=false` → clears run directory and starts fresh
3. `--resume=force` → resumes even if config has changed
4. When all stages are complete, prints helpful message suggesting `--resume=false`
5. Config changes detected and fail with clear error (unless `--resume=force`)

### Verification
- Run with fresh ticket → creates worktree, runs all stages
- Interrupt mid-run, re-run same ticket → resumes from last completed stage
- Re-run completed ticket → prints "all complete" message
- Re-run with `--resume=false` → clears state and runs all stages
- Change config, re-run → fails with config mismatch error
- Change config, `--resume=force` → continues anyway

## What We're NOT Doing

- Retry logic for failed stages (out of scope)
- Partial stage recovery (resume is at stage boundaries only)
- State file migration between fastflow versions
- Worktree cleanup/deletion commands

## Implementation Approach

Use a **state file** (`state.json`) in the run directory to track:
- Completed stages (by name)
- Config hash (to detect changes)
- Workflow name (to detect workflow changes)

This is preferred over output file detection because it explicitly tracks what **passed the judge**, not just what produced output.

---

## Phase 1: Add State Management

### Overview
Create a state package to track pipeline progress with JSON serialization.

### Changes Required:

#### 1. Create state package
**File**: `internal/state/state.go`
**Changes**: New file with state types and methods

```go
// Package state provides pipeline state tracking for resume functionality.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const StateFileName = "state.json"

// PipelineState tracks the progress of a pipeline run.
type PipelineState struct {
	// CompletedStages is the ordered list of stages that have passed the judge.
	CompletedStages []string `json:"completed_stages"`
	// Workflow is the workflow name used for this run.
	Workflow string `json:"workflow"`
	// ConfigHash is a hash of the relevant config to detect changes.
	ConfigHash string `json:"config_hash"`
}

// Load reads the state file from the run directory.
// Returns nil state (not error) if file doesn't exist.
func Load(runDir string) (*PipelineState, error) {
	path := filepath.Join(runDir, StateFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state PipelineState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}
	return &state, nil
}

// Save writes the state to the run directory.
func (s *PipelineState) Save(runDir string) error {
	path := filepath.Join(runDir, StateFileName)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	return nil
}

// MarkStageComplete adds a stage to the completed list and saves.
func (s *PipelineState) MarkStageComplete(runDir, stageName string) error {
	s.CompletedStages = append(s.CompletedStages, stageName)
	return s.Save(runDir)
}

// IsStageComplete checks if a stage has already been completed.
func (s *PipelineState) IsStageComplete(stageName string) bool {
	for _, completed := range s.CompletedStages {
		if completed == stageName {
			return true
		}
	}
	return false
}

// ComputeConfigHash generates a hash from workflow stages for change detection.
func ComputeConfigHash(workflow string, stages []string) string {
	h := sha256.New()
	h.Write([]byte(workflow))
	for _, stage := range stages {
		h.Write([]byte(stage))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NewState creates a new state for a fresh run.
func NewState(workflow string, stages []string) *PipelineState {
	return &PipelineState{
		CompletedStages: []string{},
		Workflow:        workflow,
		ConfigHash:      ComputeConfigHash(workflow, stages),
	}
}
```

### Success Criteria:

#### Automated Verification:
- [ ] Code compiles: `go build ./...`
- [ ] No linting errors: `go vet ./...`

#### Manual Verification:
- [ ] State file can be created and read back correctly

---

## Phase 2: Add Worktree Existence Check

### Overview
Add a method to check if a worktree already exists without creating it.

### Changes Required:

#### 1. Add Exists method to worktree manager
**File**: `internal/worktree/worktree.go`
**Changes**: Add new method

```go
// Exists checks if a worktree already exists for the given ticket.
func (m *Manager) Exists(ticket string) bool {
	wtPath := m.WorktreePath(ticket)
	_, err := os.Stat(wtPath)
	return err == nil
}
```

Add after line 47 (after `WorktreePath` method).

### Success Criteria:

#### Automated Verification:
- [ ] Code compiles: `go build ./...`

---

## Phase 3: Add Resume Flag to CLI

### Overview
Add `--resume` flag with three modes: auto (default), false, and force.

### Changes Required:

#### 1. Add flag variable and registration
**File**: `cmd/fastflow/main.go`
**Changes**: Add flag variable and register it

Add to flag variables (around line 77):
```go
	flagResume     string
```

Add to `init()` function (around line 88):
```go
	runCmd.Flags().StringVar(&flagResume, "resume", "auto", "Resume behavior: auto (default), true, false, or force")
```

### Success Criteria:

#### Automated Verification:
- [ ] Code compiles: `go build ./...`
- [ ] Help text shows new flag: `go run ./cmd/fastflow run --help`

---

## Phase 4: Integrate Resume Logic into Runner

### Overview
Modify the runner to use state tracking and handle resume scenarios.

### Changes Required:

#### 1. Add Resume field to Runner
**File**: `internal/runner/runner.go`
**Changes**: Add field to Runner struct

Update Runner struct (around line 18):
```go
type Runner struct {
	Config         *config.Config
	ConfigPath     string
	NoReview       bool
	DryRun         bool
	Debug          bool
	Resume         string // "auto", "true", "false", or "force"
	MaxResumptions int
}
```

#### 2. Add state import
**File**: `internal/runner/runner.go`
**Changes**: Add import

```go
import (
	// ... existing imports ...
	"github.com/dustinrasener/fastflow/internal/state"
)
```

#### 3. Modify Run method to handle resume
**File**: `internal/runner/runner.go`
**Changes**: Replace the Run method with resume-aware version

Replace the entire `Run` method (lines 48-124) with:

```go
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
```

### Success Criteria:

#### Automated Verification:
- [ ] Code compiles: `go build ./...`
- [ ] No linting errors: `go vet ./...`

---

## Phase 5: Wire Up Resume Flag in Main

### Overview
Connect the CLI flag to the runner and handle worktree existence for auto-detection.

### Changes Required:

#### 1. Update runRun function
**File**: `cmd/fastflow/main.go`
**Changes**: Update function to pass resume flag and handle auto-detection

Find the section that creates the runner (around line 162) and add:
```go
	r.Resume = flagResume
```

Also, update the worktree setup section (around line 137-148) to set resume to "auto" with worktree awareness:

Replace lines 137-148 with:
```go
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
```

Then update the runner creation to use `resumeMode`:
```go
	r := runner.NewRunner(cfg, flagConfigPath)
	r.NoReview = flagNoReview
	r.DryRun = flagDryRun
	r.Debug = flagDebug
	r.Resume = resumeMode
```

Also add import for worktree package at the top:
```go
	"github.com/dustinrasener/fastflow/internal/worktree"
```

### Success Criteria:

#### Automated Verification:
- [ ] Code compiles: `go build ./...`
- [ ] Binary runs: `go run ./cmd/fastflow version`
- [ ] Help shows resume flag: `go run ./cmd/fastflow run --help | grep resume`

#### Manual Verification:
- [ ] Fresh run creates state.json in run directory
- [ ] Re-run with same ticket skips completed stages
- [ ] `--resume=false` clears state and runs all stages
- [ ] Config change detected and fails appropriately
- [ ] `--resume=force` bypasses config check

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation that the feature works end-to-end before considering complete.

---

## Testing Strategy

### Manual Testing Steps:

1. **Fresh run test**:
   ```bash
   fastflow run --ticket TEST-001 --goal "Test goal"
   # Verify: worktree created, all stages run, state.json created
   ```

2. **Resume test**:
   ```bash
   # Interrupt with Ctrl+C during stage 2
   fastflow run --ticket TEST-002 --goal "Test goal"
   # Re-run same command
   fastflow run --ticket TEST-002 --goal "Test goal"
   # Verify: skips stage 1, continues from stage 2
   ```

3. **All complete test**:
   ```bash
   # Run to completion
   fastflow run --ticket TEST-003 --goal "Test goal"
   # Re-run
   fastflow run --ticket TEST-003 --goal "Test goal"
   # Verify: prints "All stages already complete!" message
   ```

4. **Fresh start override**:
   ```bash
   fastflow run --ticket TEST-003 --goal "Test goal" --resume=false
   # Verify: clears state, runs all stages again
   ```

5. **Config change detection**:
   ```bash
   # Run with workflow A
   fastflow run --ticket TEST-004 --goal "Test goal" --workflow full
   # Modify orchestrator.json stages
   # Re-run
   fastflow run --ticket TEST-004 --goal "Test goal" --workflow full
   # Verify: fails with config mismatch error
   ```

6. **Force resume**:
   ```bash
   fastflow run --ticket TEST-004 --goal "Test goal" --resume=force
   # Verify: continues despite config change
   ```

## References

- Research document: `thoughts/shared/research/2026-02-03-failure-resumption-behavior.md`
- Runner implementation: `internal/runner/runner.go`
- Worktree management: `internal/worktree/worktree.go`
- CLI entry point: `cmd/fastflow/main.go`
