# SKILLS-SPEC: Migrate Fastflow Prompts to Claude Code Skills

**Status:** Draft
**Author:** Q
**Date:** 2026-03-26
**Updated:** 2026-04-01

## Decision Log

- **2026-04-01:** No backward compatibility. We're the only users (plus one other who's in daily contact). This is a breaking change shipped as v0.6. No phased migration, no `prompt_file` fallback, no `--legacy` flags. Rip out embedded prompts, ship skills, done.

## Problem

Fastflow embeds ~30 prompt files (16 commands, 8 stage prompts, 6 agents) in `internal/templates/files/.claude/` totaling ~3,400 lines. These are compiled into the Go binary via `embed.FS` and copied into every repo on `fastflow init`.

This creates several problems:

1. **Repo pollution.** Every initialized repo gets `.claude/commands/`, `.claude/stages/`, and `.claude/agents/` directories — files the repo owner didn't write and shouldn't need to maintain.
2. **Tight coupling.** Prompt updates require a fastflow binary release. Users can't update prompts independently.
3. **No reuse.** The prompts are locked inside fastflow. Other tools or workflows can't use `ff_create_plan` without fastflow.
4. **No customization.** Users who want to tweak a single prompt must fork fastflow or manually edit generated files and hope `fastflow init --force` doesn't overwrite them.

## Proposal

Migrate all embedded prompts to **Claude Code skills** and make skills the sole execution path. Remove the embedded prompt system entirely. The existing `InvokeWithSkill` method and `skill` field on Stage config already support this — the plumbing exists, it just isn't the default path.

## Current Architecture

```
orchestrator.json
  └─ stages.plan.prompt_file = ".claude/stages/plan.md"
  └─ stages.plan.requires = [".claude/commands/ff_create_plan.md"]

fastflow init
  └─ embed.FS → copies .claude/{commands,stages,agents}/ into repo

fastflow run
  └─ reads stage prompt_file from repo
  └─ passes it to claude --print
  └─ stage prompt references /ff_create_plan (a .claude/commands/ file)
```

## Target Architecture

```
orchestrator.json
  └─ stages.plan.skill = "ff_create_plan"
  └─ stages.plan.model = "opus"

fastflow skills install
  └─ extracts embedded skills → ~/.claude/skills/fastflow/

fastflow run
  └─ pre-flight: validates all referenced skills exist
  └─ invokes: claude --print "/ff_create_plan <context>"
```

## Design

### 1. Skill Structure

Each fastflow prompt becomes a Claude Code skill installed at `~/.claude/skills/fastflow/<name>/`:

```
~/.claude/skills/fastflow/
├── ff_create_plan/
│   └── SKILL.md
├── ff_implement_plan/
│   └── SKILL.md
├── ff_research_codebase/
│   └── SKILL.md
├── ff_validate_plan/
│   └── SKILL.md
├── ff_commit/
│   └── SKILL.md
├── ff_debug/
│   └── SKILL.md
├── ff_describe_pr/
│   └── SKILL.md
├── ff_resolve_conflicts/
│   └── SKILL.md
├── ff_reply_to_comments/
│   └── SKILL.md
├── ff_local_review/
│   └── SKILL.md
├── ff_create_handoff/
│   └── SKILL.md
├── ff_resume_handoff/
│   └── SKILL.md
├── ff_linear/
│   └── SKILL.md
└── ...
```

#### Agent Prompts

**Decision: Inline into parent skills.** Agent prompts (e.g. `codebase-locator.md`, `thoughts-analyzer.md`) are inlined into the skills that reference them. The shared agents (`codebase-locator`, `codebase-analyzer`) only overlap between `ff_create_plan` and `ff_research_codebase` — not enough duplication to justify nested skills. Can refactor later if reuse becomes real.

**Stage prompts** (e.g. `stages/plan.md`) are thin wrappers — their content gets folded into the corresponding skill or absorbed into the context fastflow passes at invocation time.

### 2. Orchestrator Config Changes

Replace `prompt_file` + `requires` with `skill`:

**Before:**
```json
{
  "plan": {
    "prompt_file": ".claude/stages/plan.md",
    "model": "opus",
    "checkpoint": true,
    "requires": [".claude/commands/ff_create_plan.md"]
  }
}
```

**After:**
```json
{
  "plan": {
    "skill": "ff_create_plan",
    "model": "opus",
    "checkpoint": true
  }
}
```

The `prompt_file` and `requires` fields are removed from the config schema. No fallback.

### 3. Context Injection

Skills are static files, so fastflow can't use placeholder substitution. Instead, fastflow passes context as the **invocation argument** to `InvokeWithSkill`:

```go
context := fmt.Sprintf(`## Fastflow Context
- Ticket: %s
- Goal: %s
- Run directory: %s
- Workflow: %s
- Stage: %s

## Goal Content
%s`, ticket, goalSummary, runDir, workflow, stage, goalContent)

invoker.InvokeWithSkill(stage.Skill, model, context)
```

This already works — `InvokeWithSkill` prepends `/<skill>\n\n<context>` and passes it to `claude --print`.

### 4. Pre-flight Validation

Before executing any workflow, fastflow validates all skills exist:

```go
func (r *Runner) ValidateSkills(workflow *config.Workflow) error {
    var missing []string
    for _, stageName := range workflow.Stages {
        stage := r.Config.Stages[stageName]
        if stage.Skill == "" {
            return fmt.Errorf("stage %q has no skill defined", stageName)
        }
        if !skillExists(stage.Skill) {
            missing = append(missing, stage.Skill)
        }
    }
    if len(missing) > 0 {
        return fmt.Errorf("missing skills: %s\nInstall with: fastflow skills install",
            strings.Join(missing, ", "))
    }
    return nil
}

func skillExists(name string) bool {
    home, _ := os.UserHomeDir()
    path := filepath.Join(home, ".claude", "skills", "fastflow", name, "SKILL.md")
    _, err := os.Stat(path)
    return err == nil
}
```

Note: every stage must now have a `skill` field. No fallback to `prompt_file`.

### 5. Skill Distribution

New CLI command: `fastflow skills install`

```
fastflow skills install          # install all skills
fastflow skills install --list   # show available skills
fastflow skills install <name>   # install a specific skill
fastflow skills update           # update to latest versions (alias for install --force)
fastflow skills path             # print skill install directory
```

**Source:** Skills are stored in `skills/` within the fastflow repo and embedded via `embed.FS`. `fastflow skills install` extracts them to `~/.claude/skills/fastflow/`. Skills are versioned with the binary — v0.6 binary installs v0.6 skills.

**Custom overrides:** Project-local `.claude/skills/` takes precedence over `~/.claude/skills/fastflow/` in Claude Code's skill resolution. This means users can customize a skill per-repo without it getting clobbered by `fastflow skills update`.

### 6. `fastflow init` Changes

```
fastflow init
  ├── creates orchestrator.json (with skill references, no prompt_file)
  ├── creates thoughts/ scaffold
  ├── runs fastflow skills install (if skills not found)
  └── does NOT create .claude/commands/, .claude/stages/, or .claude/agents/
```

### 7. Skill Namespacing

Skills live at `~/.claude/skills/fastflow/<name>/`. The `fastflow/` namespace avoids collisions with skills from other tools and makes `fastflow skills update` a clean wipe-and-replace of that single directory.

## Removed Code

The following are deleted, not deprecated:

| Removed | Replacement |
|---------|-------------|
| `internal/templates/files/.claude/commands/*.md` | `skills/*/SKILL.md` |
| `internal/templates/files/.claude/stages/*.md` | Absorbed into skills + runner context |
| `internal/templates/files/.claude/agents/*.md` | Inlined into parent skills |
| `prompt_file` field on Stage config | `skill` field (already exists) |
| `requires` field on Stage config | Skills are self-contained |
| Template copy logic for `.claude/` dirs in `fastflow init` | `fastflow skills install` |

## Go Code Changes

### Files Modified

| File | Change |
|------|--------|
| `internal/config/config.go` | Remove `PromptFile` and `Requires` from Stage struct |
| `internal/config/validate.go` | Require `Skill` on every stage; add skill existence validation |
| `internal/runner/runner.go` | Add `ValidateSkills()` call; update hardcoded handoff/resume to use skills |
| `internal/templates/templates.go` | Replace `.claude/` copy logic with `InstallSkills()` targeting `~/.claude/skills/fastflow/` |
| `cmd/fastflow/main.go` | Register `skills` subcommand |

### Files Added

| File | Purpose |
|------|---------|
| `skills/ff_create_plan/SKILL.md` | Converted from commands + stage + agent prompts |
| `skills/ff_implement_plan/SKILL.md` | Converted from commands/ff_implement_plan.md |
| `skills/...` | One directory per skill (see inventory below) |
| `cmd/fastflow/skills.go` | `fastflow skills` subcommand |

### Files Removed

| File |
|------|
| `internal/templates/files/.claude/commands/*.md` (all 16) |
| `internal/templates/files/.claude/stages/*.md` (all 8) |
| `internal/templates/files/.claude/agents/*.md` (all 6) |

## File Inventory

### Commands → Skills (16 files, ~2,420 lines)

| File | Lines | Skill Name | Notes |
|------|-------|------------|-------|
| `ff_create_plan.md` | 449 | `ff_create_plan` | + 4 inlined agent prompts |
| `ff_linear.md` | 388 | `ff_linear` | Linear API integration |
| `ff_iterate_plan.md` | 249 | `ff_iterate_plan` | Plan refinement |
| `ff_resume_handoff.md` | 217 | `ff_resume_handoff` | Runner invokes directly |
| `ff_research_codebase.md` | 213 | `ff_research_codebase` | + 3 inlined agent prompts |
| `ff_debug.md` | 200 | `ff_debug` | Debugging workflow |
| `ff_validate_plan.md` | 166 | `ff_validate_plan` | Validation |
| `ff_resolve_conflicts.md` | 155 | `ff_resolve_conflicts` | Merge conflicts |
| `ff_create_handoff.md` | 95 | `ff_create_handoff` | Runner invokes directly |
| `ff_describe_pr.md` | 76 | `ff_describe_pr` | PR descriptions |
| `ff_implement_plan.md` | 72 | `ff_implement_plan` | Implementation |
| `ff_local_review.md` | 48 | `ff_local_review` | Self-review |
| `ff_create_worktree.md` | 41 | `ff_create_worktree` | Worktree setup |
| `ff_commit.md` | 39 | `ff_commit` | Commit + PR |
| `ff_oneshot.md` | 6 | `ff_oneshot` | Quick single-shot |
| `ff_oneshot_plan.md` | 6 | `ff_oneshot_plan` | Quick plan |

### Stage Prompts (8 files, ~140 lines) — Folded into skills or context

| File | Lines | Absorbed Into |
|------|-------|---------------|
| `stages/plan.md` | 11 | `ff_create_plan` context |
| `stages/implement.md` | 11 | `ff_implement_plan` context |
| `stages/commit.md` | 11 | `ff_commit` context |
| `stages/research.md` | 11 | `ff_research_codebase` context |
| `stages/validate.md` | 9 | `ff_validate_plan` context |
| `stages/debug.md` | 9 | `ff_debug` context |
| `stages/merge-conflict.md` | 11 | `ff_resolve_conflicts` context |
| `stages/fetch-feedback.md` | 67 | `ff_fetch_feedback` (new skill) |
| `stages/reply-to-comments.md` | — | `ff_reply_to_comments` context |

### Agent Prompts (6 files, ~873 lines) — Inlined into parent skills

| File | Lines | Inlined Into |
|------|-------|--------------|
| `codebase-pattern-finder.md` | 227 | `ff_create_plan`, `ff_research_codebase` |
| `thoughts-analyzer.md` | 145 | `ff_create_plan` |
| `codebase-analyzer.md` | 143 | `ff_create_plan`, `ff_research_codebase` |
| `thoughts-locator.md` | 127 | `ff_create_plan` |
| `codebase-locator.md` | 122 | `ff_create_plan`, `ff_research_codebase` |
| `web-search-researcher.md` | 109 | `ff_research_codebase` |

## Open Questions

1. **Handoff/resume skills.** `ff_create_handoff` and `ff_resume_handoff` are invoked directly by the runner, not via stage config. The runner's hardcoded references need updating to use `InvokeWithSkill`.

2. **`reply-to-comments` stage.** Exists only in the `review-with-replies` workflow. The stage prompt file doesn't currently exist in embedded templates but the command does. Needs to be created as a skill.

3. **`ff_create_plan` size after inlining.** Will push past 600 lines with 4 agent prompts inlined. Acceptable — it's the most complex stage and self-containment is worth the length.

## Success Criteria

1. `fastflow run --workflow full` completes using skills exclusively
2. `fastflow init` produces a repo with no `.claude/commands/`, `.claude/stages/`, or `.claude/agents/` directories
3. `fastflow skills install` installs all skills to `~/.claude/skills/fastflow/`
4. Pre-flight validation catches missing skills with a clear install instruction
5. No `prompt_file` or `requires` references remain in the codebase
6. Skills are usable outside fastflow via `claude --print "/ff_create_plan <context>"`
7. Version bump to 0.6 with breaking change documented in CHANGELOG
