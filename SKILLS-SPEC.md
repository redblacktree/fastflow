# SKILLS-SPEC: Migrate Fastflow Prompts to Claude Code Skills

**Status:** Draft  
**Author:** Q  
**Date:** 2026-03-26

## Problem

Fastflow embeds ~30 prompt files (16 commands, 8 stage prompts, 6 agents) in `internal/templates/files/.claude/` totaling ~3,400 lines. These are compiled into the Go binary via `embed.FS` and copied into every repo on `fastflow init`.

This creates several problems:

1. **Repo pollution.** Every initialized repo gets `.claude/commands/`, `.claude/stages/`, and `.claude/agents/` directories — files the repo owner didn't write and shouldn't need to maintain.
2. **Tight coupling.** Prompt updates require a fastflow binary release. Users can't update prompts independently.
3. **No reuse.** The prompts are locked inside fastflow. Other tools or workflows can't use `ff_create_plan` without fastflow.
4. **No customization.** Users who want to tweak a single prompt (e.g. change the plan format) must fork fastflow or manually edit the generated files and hope `fastflow init --force` doesn't overwrite them.

## Proposal

Migrate all embedded prompts to **Claude Code skills** and make skills the primary execution path. The existing `InvokeWithSkill` method and `skill` field on Stage config already support this — the plumbing exists, it just isn't the default path.

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
  └─ clones skills from repo → ~/.claude/skills/fastflow/

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
│   └── SKILL.md          # current ff_create_plan.md content
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

#### What about stage prompts and agents?

**Stage prompts** (e.g. `stages/plan.md`) are thin wrappers that tell Claude "read the goal, find research, then run `/ff_create_plan`". These get **folded into the corresponding skill** as a preamble or absorbed into the context that fastflow passes at invocation time.

**Agent prompts** (e.g. `agents/codebase-locator.md`) are sub-agent definitions referenced by the larger skills. Two options:

- **Option A: Inline them.** The skills that reference sub-agents (like `ff_create_plan` referencing `codebase-locator`) include the agent behavior directly. Simpler, but duplicates content if multiple skills reference the same agent.
- **Option B: Nested skills.** Agent prompts become their own skills (e.g. `ff_agent_codebase_locator`), and parent skills reference them by slash command. More modular, but deeper dependency chain.

**Recommendation:** Option A for now. The agent prompts are specialized enough that sharing across skills is rare in practice. Can refactor to Option B later if reuse becomes real.

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

The `requires` field becomes unnecessary — skill existence is validated at pre-flight, not by checking for files in the repo.

`prompt_file` remains supported for backward compatibility (custom stages that aren't skills), but the default `orchestrator.json` shipped with fastflow uses skills exclusively.

### 3. Context Injection

Current stage prompts use `{ticket}`, `{goal}`, `{run_dir}` placeholders that fastflow substitutes before passing to Claude. Skills can't use this mechanism since they're static files.

Instead, fastflow passes context as the **invocation argument** to `InvokeWithSkill`:

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

This already works — `InvokeWithSkill` prepends `/<skill>\n\n<context>` and passes it to `claude --print`. Skills read the context from their invocation, not from placeholders.

### 4. Pre-flight Validation

Before executing any workflow, fastflow validates all skills exist:

```go
func (r *Runner) ValidateSkills(workflow *config.Workflow) error {
    var missing []string
    for _, stageName := range workflow.Stages {
        stage := r.Config.Stages[stageName]
        if stage.Skill != "" && !skillExists(stage.Skill) {
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
    // Check ~/.claude/skills/fastflow/<name>/SKILL.md
    home, _ := os.UserHomeDir()
    path := filepath.Join(home, ".claude", "skills", "fastflow", name, "SKILL.md")
    _, err := os.Stat(path)
    return err == nil
}
```

### 5. Skill Distribution

New CLI command: `fastflow skills install`

```
fastflow skills install          # install all default skills
fastflow skills install --list   # show available skills
fastflow skills install <name>   # install a specific skill
fastflow skills update           # update to latest versions
fastflow skills path             # print skill install directory
```

**Source:** Skills are stored in a dedicated directory within the fastflow repo (e.g. `skills/`) and distributed with the binary via `embed.FS` — same mechanism as today, but installed to `~/.claude/skills/fastflow/` instead of the repo's `.claude/` directory.

**Why embed, not a separate repo?** Keeps skills versioned with the fastflow release. A `fastflow skills install` with v0.5.0 installs v0.5.0 skills. Avoids version skew between binary and prompts. A future registry could supplement this for community skills.

### 6. `fastflow init` Changes

`fastflow init` currently copies all `.claude/` files into the repo. After migration:

```
fastflow init
  ├── creates orchestrator.json (with skill references)
  ├── creates thoughts/ scaffold
  ├── runs fastflow skills install (if skills not found)
  └── NO LONGER copies .claude/commands/, .claude/stages/, .claude/agents/
```

For repos that already have `.claude/` files from a previous init:
- `fastflow init` warns about stale files but doesn't delete them
- A `fastflow migrate` command (optional) removes old `.claude/` files and updates `orchestrator.json` to use skills

### 7. Backward Compatibility

- `prompt_file` continues to work. If a stage has both `skill` and `prompt_file`, `skill` takes precedence.
- `requires` is ignored when `skill` is set (skills are self-contained).
- Existing repos with `.claude/` files continue working — nothing breaks until the user runs `fastflow init --force` or `fastflow migrate`.
- `orchestrator.json` format is additive, not breaking.

## Migration Path

### Phase 1: Ship skills alongside prompts
- Add `skills/` directory to fastflow repo with all prompts converted to SKILL.md format
- Add `fastflow skills install` command
- Add pre-flight skill validation
- Update default `orchestrator.json` to use `skill` field
- **Keep embedded prompt files** — both paths work

### Phase 2: Default to skills
- `fastflow init` no longer copies `.claude/` files by default
- Add `fastflow init --legacy` for the old behavior
- Add `fastflow migrate` for existing repos
- Deprecation warnings when `prompt_file` is used for built-in stages

### Phase 3: Remove embedded prompts
- Remove `internal/templates/files/.claude/{commands,stages,agents}/`
- Remove `prompt_file` resolution for built-in stage names
- `prompt_file` still works for user-defined custom stages

## Open Questions

1. **Skill namespacing.** Should skills live at `~/.claude/skills/fastflow/ff_create_plan/` or `~/.claude/skills/ff_create_plan/`? Namespacing under `fastflow/` avoids collisions with other tools but adds a path segment.

2. **Handoff/resume skills.** `ff_create_handoff` and `ff_resume_handoff` are invoked internally by the runner (not via stage config). These should still become skills, but the runner's hardcoded references need updating.

3. **Agent prompt inlining.** The largest command (`ff_create_plan`, 449 lines) references multiple sub-agents. Inlining agent prompts could push it past 600 lines. Is that acceptable, or should we split into nested skills from the start?

4. **Custom skill overrides.** If a user wants to customize `ff_create_plan`, should they fork the skill in `~/.claude/skills/fastflow/` (gets overwritten on update) or place an override in a project-local `.claude/skills/` directory? Claude Code's skill resolution order determines this.

5. **`reply-to-comments` stage.** This stage exists only in the `review-with-replies` workflow and references `ff_reply_to_comments.md`. The stage prompt file doesn't currently exist in embedded templates but the command does. Needs to be created as a skill or the stage needs to directly reference the skill name.

## File Inventory

### Commands → Skills (16 files, ~2,420 lines)

| File | Lines | Skill Name | Notes |
|------|-------|------------|-------|
| `ff_create_plan.md` | 449 | `ff_create_plan` | References 4 sub-agents |
| `ff_linear.md` | 388 | `ff_linear` | Linear API integration |
| `ff_iterate_plan.md` | 249 | `ff_iterate_plan` | Plan refinement |
| `ff_resume_handoff.md` | 217 | `ff_resume_handoff` | Internal runner use |
| `ff_research_codebase.md` | 213 | `ff_research_codebase` | References 3 sub-agents |
| `ff_debug.md` | 200 | `ff_debug` | Debugging workflow |
| `ff_validate_plan.md` | 166 | `ff_validate_plan` | Validation |
| `ff_resolve_conflicts.md` | 155 | `ff_resolve_conflicts` | Merge conflicts |
| `ff_create_handoff.md` | 95 | `ff_create_handoff` | Internal runner use |
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

| File | Lines | Referenced By |
|------|-------|---------------|
| `codebase-pattern-finder.md` | 227 | `ff_create_plan`, `ff_research_codebase` |
| `thoughts-analyzer.md` | 145 | `ff_create_plan` |
| `codebase-analyzer.md` | 143 | `ff_create_plan`, `ff_research_codebase` |
| `thoughts-locator.md` | 127 | `ff_create_plan` |
| `codebase-locator.md` | 122 | `ff_create_plan`, `ff_research_codebase` |
| `web-search-researcher.md` | 109 | `ff_research_codebase` |

## Go Code Changes

### Files Modified

| File | Change |
|------|--------|
| `internal/config/config.go` | No structural change (Stage.Skill field already exists) |
| `internal/config/validate.go` | Add skill existence validation |
| `internal/runner/runner.go` | Add `ValidateSkills()` call before workflow execution; update hardcoded handoff/resume skill refs |
| `internal/runner/claude.go` | No change (`InvokeWithSkill` already works) |
| `internal/templates/templates.go` | Add `InstallSkills()` function targeting `~/.claude/skills/fastflow/` |
| `cmd/fastflow/skills.go` | New — `fastflow skills` subcommand |
| `cmd/fastflow/migrate.go` | New — `fastflow migrate` subcommand |

### Files Added

| File | Purpose |
|------|---------|
| `skills/ff_create_plan/SKILL.md` | Converted from commands/ff_create_plan.md |
| `skills/ff_implement_plan/SKILL.md` | Converted from commands/ff_implement_plan.md |
| `skills/...` | One directory per skill |

### Files Eventually Removed (Phase 3)

| File | Replacement |
|------|-------------|
| `internal/templates/files/.claude/commands/*.md` | `skills/*/SKILL.md` |
| `internal/templates/files/.claude/stages/*.md` | Absorbed into skills + runner context |
| `internal/templates/files/.claude/agents/*.md` | Inlined into parent skills |

## Success Criteria

1. `fastflow run --workflow full` completes using skills instead of embedded prompt files
2. A fresh `fastflow init` produces a repo with no `.claude/commands/` or `.claude/stages/` directories
3. `fastflow skills install` installs all skills to `~/.claude/skills/fastflow/`
4. Pre-flight validation catches missing skills with a clear install instruction
5. Existing repos with `prompt_file` in their `orchestrator.json` continue working unchanged
6. Skills are usable outside fastflow via `claude --print "/ff_create_plan <context>"`
