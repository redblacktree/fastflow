---
description: Single-Shot Multi-Agent Orchestrator with JSON Declarative Configuration
model: opus
status: draft
created: 2026-02-03
last_updated: 2026-02-03
last_updated_by: claude
---

# Single-Shot Multi-Agent Orchestrator

## Overview

Build a portable Go binary (`fastflow`) that automates the full development workflow:
**Goal → Worktree → Research → Plan → Implement → Validate → Commit/PR**

Each stage runs in a fresh Claude Code context, communicating via markdown files in `thoughts/`. The orchestrator is configured via a declarative JSON schema and can be used across any project.

## Current State Analysis

The existing `.claude/` system has:
- **11 commands**: Manual slash commands (`/create_plan`, `/implement_plan`, etc.)
- **6 agents**: Specialized sub-agents for research tasks
- **thoughts/ directory**: Markdown-based knowledge persistence
- **Worktree support**: `create_worktree.sh` script

**Gap**: No automated orchestration layer. User must manually invoke each command and manage context transitions.

## Desired End State

```bash
# Full workflow (default): research → plan → implement → validate → commit
$ fastflow run --goal "Add user authentication" --ticket ENG-1234

# Plan-first workflow: skips research
$ fastflow run --goal "Fix typo in README" --ticket ENG-1235 --workflow plan-first

# Debug workflow: uses debug skill, focused on investigation
$ fastflow run --goal "Fix login timeout bug" --ticket ENG-1236 --workflow debug

# Skip checkpoints (for CI/automation)
$ fastflow run --goal "Refactor auth system" --ticket ENG-1237 --no-review

# Validate config and dependencies before running
$ fastflow validate --config orchestrator.json
```

This single command:
1. Creates worktree `~/wt/fastflow/ENG-1234`
2. Launches research agent → outputs `thoughts/shared/runs/ENG-1234/research.md`
3. Launches planning agent → outputs `thoughts/shared/runs/ENG-1234/plan.md`
4. Launches implementation agent → updates plan with progress
5. Launches validation agent → outputs `thoughts/shared/runs/ENG-1234/validation.md`
6. Launches commit/PR agent → creates PR and outputs `thoughts/shared/runs/ENG-1234/pr.md`

Each stage is a fresh Claude Code session with context loaded from markdown files.

## Key Discoveries

From analyzing the existing system:
1. **Agents use YAML frontmatter** - Defines name, description, tools, model
2. **Commands use markdown instructions** - Detailed prompts with examples
3. **Thoughts sync via `humanlayer thoughts sync`** - Should run even with local files; the HumanLayer system benefits from syncing
4. **Context passing pattern** - Read previous outputs, write new outputs
5. **Success criteria pattern** - Automated (commands) vs Manual (human verification)

## What We're NOT Doing

- Not replacing existing commands/agents (they still work for interactive use)
- Not building a complex daemon or server
- Not using external databases or APIs
- Runtime requires only the `fastflow` binary and `claude` CLI (Go toolchain only needed for building)

---

## Implementation Approach

### Architecture

```
fastflow (Go binary)
    ↓ reads & validates
orchestrator.json (workflow config)
    ↓ verifies dependencies exist
.claude/stages/*.md (stage prompts)
.claude/commands/*.md (skills referenced by stages)
    ↓ executes stages
claude CLI (fresh context per stage)
    ↓ reads/writes
thoughts/shared/runs/{ticket}/*.md (context files)
```

### Why Go?

- **Portable**: Single binary works on macOS, Linux, Windows
- **Validation**: Verify `.claude/` dependencies exist before running
- **Better UX**: Rich CLI with progress indicators, colored output
- **Extensibility**: Easy to add features like parallelism, retries, resume
- **No runtime deps**: Users don't need bash/shell compatibility

### JSON Schema Design

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "workflows": {
      "type": "object",
      "description": "Named workflow definitions",
      "additionalProperties": {
        "type": "object",
        "properties": {
          "description": { "type": "string" },
          "stages": { "type": "array", "items": { "type": "string" } }
        }
      }
    },
    "stages": {
      "type": "object",
      "description": "Stage definitions referenced by workflows",
      "additionalProperties": {
        "type": "object",
        "properties": {
          "prompt_file": { "type": "string", "description": "Path to .claude/stages/*.md prompt file" },
          "skill": { "type": "string", "description": "Skill to invoke (e.g., 'debug') instead of prompt_file" },
          "requires": { "type": "array", "items": { "type": "string" }, "description": "Paths that must exist (.claude/commands/*.md, etc.)" },
          "model": { "type": "string", "enum": ["opus", "sonnet", "haiku"] },
          "checkpoint": { "type": "boolean", "description": "If true, pause for human review after this stage" },
          "judge_prompt": { "type": "string", "description": "Custom success criteria for this stage (used by judge)" }
        }
      }
    },
    "default_workflow": { "type": "string" },
    "default_judge_prompt": { "type": "string", "description": "Fallback judge prompt if stage doesn't define one" },
    "judge_model": { "type": "string", "enum": ["opus", "sonnet", "haiku"], "default": "haiku" }
  }
}
```

**Notes:**
- The `requires` field declares dependencies on `.claude/` files that must exist. `fastflow validate` checks these before running.
- File I/O follows convention: stage outputs go to `thoughts/shared/runs/{ticket}/{stage_name}.md`
- The orchestrator injects `{ticket}`, `{goal}`, `{run_dir}` placeholders into prompts at runtime.

### Success Evaluation (LLM as Judge)

After each stage completes, a judge prompt evaluates whether the stage succeeded:

1. **Run stage** → Claude executes the work
2. **Capture output** → CLI output and any generated files
3. **Run judge** → Fast model (haiku) evaluates success based on criteria
4. **Decision** → `YES` continues pipeline, `NO` halts with reasoning

**Judge receives:**
- Original goal and ticket
- Stage name and its `judge_prompt` (or `default_judge_prompt`)
- Full CLI output from Claude
- Contents of stage output file (if created)

**Judge responds with:**
```
YES|NO: <brief reasoning>
```

This is more reliable than markers because:
- Judge focuses specifically on evaluation
- Assesses actual work done, not self-reported completion
- Uses cheap/fast model (haiku by default)
- Custom criteria per stage when needed

### Predefined Workflows

| Workflow | Stages | Use Case |
|----------|--------|----------|
| `full` (default) | research → plan → implement → validate → commit | Complex features, new functionality |
| `plan-first` | plan → implement → validate → commit | Simple changes, minor features |
| `debug` | debug → implement → validate → commit | Bug fixes, uses `/debug` skill |
| `quick` | implement → validate → commit | When you already have a plan |

### Human Review Checkpoint

Checkpoints are configured declaratively in the JSON via `"checkpoint": true` on any stage. By default, the `plan` stage has `"checkpoint": true`, meaning:
- Pipeline pauses after plan generation
- User reviews `thoughts/shared/runs/{ticket}/plan.md`
- User confirms to continue or provides feedback
- Pipeline resumes with implementation

The `--no-review` flag skips all checkpoints (useful for CI/automation where human review isn't needed).

---

## Phase 1: Core Go Binary

**Goal**: Create the `fastflow` Go binary with CLI parsing and config validation

### Project Structure

```
cmd/
└── fastflow/
    └── main.go          # Entry point, CLI setup
internal/
├── config/
│   ├── config.go        # JSON config structs
│   └── validate.go      # Config validation logic
├── runner/
│   ├── runner.go        # Stage execution logic
│   └── claude.go        # Claude CLI invocation
├── judge/
│   └── judge.go         # LLM-as-judge evaluation logic
└── worktree/
    └── worktree.go      # Git worktree management
go.mod
go.sum
orchestrator.json        # Default pipeline config
```

### CLI Commands

```bash
fastflow run --goal "..." --ticket "..." [--workflow name] [--no-review] [--config path]
fastflow validate [--config path]
fastflow version
```

### Key Implementation Details

1. **CLI Framework**: Use `cobra` for commands and flags
2. **Config Parsing**: Native Go JSON unmarshaling with validation
3. **Dependency Verification**: Check all `requires` paths exist before running
4. **Claude Invocation**: `exec.Command` with proper signal handling
5. **Judge Evaluation**: After each stage, run judge prompt to verify success
6. **Checkpoint Handling**: Prompt user for confirmation, read stdin
7. **Progress Output**: Colored status messages for each stage

### Files to Create

1. **Go source files** (see project structure above)

2. **`orchestrator.json`** - Default pipeline configuration
   - Defines stage library: research, debug, plan, implement, validate, commit
   - Defines workflows: full, plan-first, debug, quick
   - Sets model preferences per stage
   - Declares `requires` dependencies for validation

3. **`thoughts/shared/runs/.gitkeep`** - Ensure runs directory exists

### Success Criteria

**Automated:**
- [x] `go build ./cmd/fastflow` compiles without errors
- [x] `fastflow --help` shows usage including subcommands
- [x] `fastflow run --help` shows run flags
- [x] `fastflow validate` checks config and reports missing dependencies
- [x] `fastflow run --workflow invalid` exits with clear error

**Manual:**
- [ ] Review code structure for clarity
- [ ] Verify checkpoint pause works (respects `"checkpoint": true` in config)
- [ ] Verify `--no-review` skips checkpoints

---

## Phase 2: Stage Prompts

**Goal**: Create minimal stage prompts that delegate to existing `.claude/commands/`

### Design Decision

Stage prompts are thin wrappers that:
1. Read the goal file at `thoughts/shared/runs/{ticket}/goal.md`
2. Invoke the appropriate existing command (e.g., `/create_plan`, `/implement_plan`)
3. Let commands handle their own output locations

Commands write to their standard locations:
- `/research_codebase` → `thoughts/shared/research/YYYY-MM-DD-{ticket}-*.md`
- `/create_plan` → `thoughts/shared/plans/YYYY-MM-DD-{ticket}-*.md`
- etc.

Stages find prior outputs by searching for files containing `{ticket}` in their filename.

### Files to Create

1. **`.claude/stages/research.md`** - Invokes `/research_codebase`
2. **`.claude/stages/debug.md`** - Invokes `/debug`
3. **`.claude/stages/plan.md`** - Invokes `/create_plan`
4. **`.claude/stages/implement.md`** - Invokes `/implement_plan`
5. **`.claude/stages/validate.md`** - Invokes `/validate_plan`
6. **`.claude/stages/commit.md`** - Invokes `/commit` and `/describe_pr`

### Success Criteria

**Automated:**
- [x] All 6 prompt files exist in `.claude/stages/`
- [x] `fastflow validate` passes

**Manual:**
- [x] Prompts are minimal and delegate to commands

---

## Phase 3: Context Passing Mechanism

**Goal**: Implement context passing between stages via goal file and ticket-based file discovery

### Design Decision

Instead of stages writing to a central `runs/{ticket}/` directory, we:
1. Orchestrator creates `thoughts/shared/runs/{ticket}/goal.md` with goal and ticket info
2. Stage prompts reference `{ticket}` which gets interpolated by the orchestrator
3. Stages find prior outputs by searching for files containing `{ticket}` in standard directories
4. Judge evaluates success from CLI output (no success markers needed)

### Implementation Details

1. **Goal File** (created by orchestrator):
   ```markdown
   ---
   ticket: {ticket}
   goal: {goal}
   created: {timestamp}
   ---

   # Goal

   {goal}

   # Context

   Repository: {repo_name}
   Branch: {branch_name}
   Working Directory: {work_dir}
   ```

2. **File Discovery Pattern**:
   - Research: `thoughts/shared/research/*{ticket}*.md`
   - Plans: `thoughts/shared/plans/*{ticket}*.md`

3. **Judge Evaluation**:
   - Judge receives full CLI output from stage
   - Evaluates based on judge_prompt criteria
   - No file markers needed - judge analyzes what actually happened

### Success Criteria

**Automated:**
- [x] `goal.md` is created with correct content (implemented in runner.go)
- [x] Placeholder interpolation works (`{ticket}`, `{goal}`, `{run_dir}`)
- [x] Judge prompts updated to evaluate CLI output

**Manual:**
- [ ] Context flows correctly between stages (needs E2E test)

---

## Phase 4: Claude Code Integration

**Goal**: Implement the Claude Code invocation and judge evaluation patterns

### Stage Execution Flow

```
┌─────────────────────────────────────────────────────────┐
│ For each stage in workflow:                             │
│                                                         │
│   1. Run Claude with stage prompt                       │
│      └─→ Capture CLI output                             │
│                                                         │
│   2. Run Judge (haiku) with:                            │
│      • Goal, ticket, stage name                         │
│      • Stage's judge_prompt (or default)                │
│      • CLI output from step 1                           │
│      • Stage output file contents (if exists)           │
│      └─→ Returns YES/NO with reasoning                  │
│                                                         │
│   3. If NO: halt pipeline, show reasoning               │
│      If YES: continue to next stage (or checkpoint)     │
└─────────────────────────────────────────────────────────┘
```

### Implementation Details

1. **Stage Invocation**:
   ```bash
   claude --model {model} \
          --print \
          --dangerously-skip-permissions \
          --max-turns 50 \
          "{prompt_with_context}"
   ```

2. **Judge Invocation**:
   ```bash
   claude --model haiku \
          --print \
          --max-turns 1 \
          "Evaluate if this stage succeeded: ..."
   ```

3. **Prompt Injection**:
   - Read stage prompt from `.claude/stages/{stage}.md`
   - Replace `{ticket}`, `{goal}`, `{run_dir}` placeholders
   - Prepend with "Read the goal file first: thoughts/shared/runs/{ticket}/goal.md"

4. **Working Directory**:
   - Change to worktree directory before running Claude
   - Ensures all file operations are relative to worktree

5. **Error Handling**:
   - Check Claude exit code (non-zero = immediate failure)
   - Run judge evaluation
   - If judge returns NO: abort pipeline with reasoning
   - If judge returns YES: proceed to checkpoint (if configured) or next stage

### Success Criteria

**Automated:**
- [ ] Claude is invoked with correct flags
- [ ] Prompts are correctly interpolated
- [ ] Judge is invoked after each stage
- [ ] Judge YES/NO is correctly parsed
- [ ] Pipeline halts on judge NO with clear output

**Manual:**
- [ ] Observe successful stage + judge flow
- [ ] Verify judge reasoning is helpful when failing

---

## Phase 5: End-to-End Testing

**Goal**: Test the complete pipeline with a real task

### Test Plan

1. **Test Case 1: Simple Feature**
   - Goal: "Add a hello world endpoint to the API"
   - Expected: Pipeline completes all stages
   - Verification: PR is created with working code

2. **Test Case 2: Pipeline Failure**
   - Goal: Use invalid goal that should fail validation
   - Expected: Pipeline stops at validation stage
   - Verification: Clear error message, no PR created

3. **Test Case 3: Resume Capability**
   - Goal: Interrupt pipeline mid-way
   - Expected: Can resume from last successful stage
   - Verification: Re-running skips completed stages

### Success Criteria

**Automated:**
- [ ] Test case 1 produces a valid PR
- [ ] Test case 2 fails gracefully
- [ ] Test case 3 resumes correctly

**Manual:**
- [ ] Review generated code quality
- [ ] Review PR description quality

---

## Testing Strategy

### Unit Testing (Go)
- `config_test.go`: JSON config parsing and validation
- `validate_test.go`: Dependency checking with mock filesystem
- `runner_test.go`: Placeholder interpolation, success marker detection

### Integration Testing
- Test worktree creation with real git
- Test Claude invocation with `--dry-run` flag
- Test context file I/O

### End-to-End Testing
- Full pipeline execution with mock goal
- Verify all outputs are generated
- Verify PR is created

### Running Tests
```bash
go test ./...                    # All unit tests
go test -v ./internal/config/    # Specific package
fastflow run --dry-run ...       # Integration test without Claude
```

---

## File Summary

| File | Purpose |
|------|---------|
| `cmd/fastflow/main.go` | CLI entry point |
| `internal/config/config.go` | Config structs and parsing |
| `internal/config/validate.go` | Dependency validation |
| `internal/runner/runner.go` | Stage execution logic |
| `internal/runner/claude.go` | Claude CLI invocation |
| `internal/judge/judge.go` | LLM-as-judge evaluation |
| `internal/worktree/worktree.go` | Git worktree management |
| `orchestrator.json` | Default pipeline config |
| `.claude/stages/research.md` | Research stage prompt |
| `.claude/stages/debug.md` | Debug stage prompt (uses `/debug` skill) |
| `.claude/stages/plan.md` | Planning stage prompt |
| `.claude/stages/implement.md` | Implementation stage prompt |
| `.claude/stages/validate.md` | Validation stage prompt |
| `.claude/stages/commit.md` | Commit/PR stage prompt |
| `thoughts/shared/runs/.gitkeep` | Runs directory placeholder |

---

## References

- Existing commands: `.claude/commands/*.md`
- Existing agents: `.claude/agents/*.md`
- Settings: `.claude/settings.json`
- Worktree script: `hack/create_worktree.sh` (if exists)
- Go CLI libraries: [cobra](https://github.com/spf13/cobra), [color](https://github.com/fatih/color)
