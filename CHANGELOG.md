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

Users upgrading from v0.5 to v0.6 must:

1. Run `fastflow skills install` to install skills
2. Update their `orchestrator.json` to use `skill` instead of `prompt_file`/`requires`
3. Delete `.claude/{commands,stages,agents}/` from their repos (optional but recommended)
4. Re-run `fastflow init --force` to get the updated `orchestrator.json` template
