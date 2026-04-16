# Changelog

## v0.6.0 — 2026-04-11

### Breaking Changes

- **Skills replace prompt files**: All stage prompts are now delivered via Claude Code skills instead of embedded prompt files. The `prompt_file` and `requires` fields have been removed from the stage configuration.
- **New `orchestrator.json` format**: Stages must use the `skill` field. Old configs with `prompt_file` will fail validation.
- **`fastflow init` no longer creates `.claude/` directories**: Command, stage, and agent prompts are no longer extracted into repos. Skills are installed to `~/.claude/skills/fastflow/` instead.

### Added

- `fastflow skills install` — install embedded skills to `~/.claude/skills/fastflow/`
- `fastflow skills install --list` — list available skills
- `fastflow skills install <name>` — install a specific skill
- `fastflow skills update` — update installed skills (overwrites existing)
- `fastflow skills path` — print skills install directory
- Pre-flight validation: workflow execution checks that all referenced skills are installed
- `review-with-replies` workflow added to default orchestrator template
- `reply-to-comments` and `fetch-feedback` skills added

### Removed

- `prompt_file` field from stage configuration
- `requires` field from stage configuration
- `.claude/commands/`, `.claude/stages/`, `.claude/agents/` embedded templates

### Migration

Users upgrading from v0.5 to v0.6 must handle both a machine-level change and, for existing repos, a repo-level config change.

#### Machine-level

Run this once after upgrading the fastflow binary:

```bash
fastflow skills install
```

This installs all built-in fastflow skills to `~/.claude/skills/fastflow/`. There is no separate migration skill and no need to install stages one by one.

#### Existing repos initialized with v0.5

Existing repos must update `orchestrator.json` to use `skill` instead of `prompt_file` and `requires`, then validate:

```bash
fastflow validate
```

If the repo still uses the stock generated config, you can refresh it with:

```bash
fastflow init --force
```

If the repo has custom workflow edits, update `orchestrator.json` manually instead of overwriting it.

Minimal example:

Before:

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

After:

```json
{
  "plan": {
    "skill": "create_plan",
    "model": "opus",
    "checkpoint": true
  }
}
```

#### Optional cleanup

After migrating an older repo, delete these legacy directories if present:

```text
.claude/commands/
.claude/stages/
.claude/agents/
```

#### New repos

New repos only need:

```bash
fastflow init
fastflow validate
```

`fastflow init` writes the new config format and auto-installs skills if they are missing.
