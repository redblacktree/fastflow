# fastflow

A multi-agent development workflow orchestrator that automates the full development lifecycle from goal to PR.

## Overview

fastflow orchestrates development workflows by running specialized stages in isolated Claude Code contexts:

```
Goal → Worktree → Research → Plan → Implement → Validate → Commit/PR
```

Each stage communicates via markdown files in `thoughts/`, enabling clean handoffs between agent contexts.

## Prerequisites

- [Claude Code](https://claude.ai/claude-code) CLI installed and authenticated

## Authentication

Claude Code must be authenticated before fastflow can run. For headless or automated environments (Linux VMs, CI, containers) where a browser-based login isn't feasible, use `setup-token` to create a long-lived authentication token:

```bash
claude setup-token
```

This avoids the interactive OAuth flow that requires a browser. Without a valid session, fastflow will fail immediately with:

```
claude is not logged in: please run 'claude' interactively and use /login to authenticate
```

## Installation

Download the latest release from the [releases page](https://github.com/redblacktree/fastflow/releases).

Or build from source (requires Go 1.21+):

```bash
git clone https://github.com/redblacktree/fastflow.git
cd fastflow
go build -o fastflow ./cmd/fastflow
```

## Usage

### Run a full workflow

```bash
fastflow run --goal "Add user authentication" --ticket ENG-1234
```

### Initialize a repository

```bash
fastflow init
```

### Available workflows

- **full** (default): research → plan → implement → validate → commit
- **plan-first**: Skip research, start with planning
- **debug**: Debug workflow for bug fixes
- **quick**: When plan already exists

```bash
# Plan-first workflow
fastflow run --goal "Fix typo in README" --ticket ENG-1235 --workflow plan-first

# Debug workflow
fastflow run --goal "Fix login timeout bug" --ticket ENG-1236 --workflow debug

# Skip checkpoints (for CI/automation)
fastflow run --goal "Refactor auth system" --ticket ENG-1237 --no-review
```

### Validate configuration

```bash
fastflow validate
```

### Monitor

Start a web dashboard for monitoring in-flight fastflow runs:

```bash
fastflow monitor
```

Open `http://localhost:8080` in a browser to view the dashboard. The dashboard shows all active and recent runs with their ticket, status, current stage, timestamps, PID (if live), and log file path. It auto-refreshes every 5 seconds.

Options:
- `--addr` — Address to listen on (default: `:8080`)

```bash
# Use a custom address
fastflow monitor --addr :9090
```

#### API

The monitor also exposes a JSON API:

- `GET /api/runs` — Returns all runs as a JSON array
- `GET /api/runs?prefix=ENG-` — Filter runs by ticket prefix

Each entry includes: `ticket`, `status`, `stage`, `created`, `updated_at`, `pid`, `log_path`, `work_dir`, `run_dir`.

## Configuration

fastflow uses `orchestrator.json` for workflow configuration. See the included config for examples of:

- Defining workflows and their stages
- Configuring stage models (sonnet, opus, haiku)
- Setting up checkpoints for human review
- Custom judge prompts for stage validation

## Upgrading to v0.6

v0.6 replaces embedded prompt files with Claude Code skills. This is a breaking change.

### What changed

- Stages now use `skill` instead of `prompt_file` and `requires`
- `fastflow init` no longer creates `.claude/commands`, `.claude/stages`, or `.claude/agents`
- Fastflow skills are installed to `~/.claude/skills/fastflow/`

### Machine-level upgrade

Run this once on each machine after upgrading the fastflow binary:

```bash
fastflow skills install
```

This installs all built-in fastflow skills. You do not need to install workflow items one by one.

### New repositories

For new repositories, just run:

```bash
fastflow init
fastflow validate
```

`fastflow init` will create the new `orchestrator.json` format and auto-install skills if needed.

### Existing repositories initialized with v0.5

Existing repos need both of these changes:

1. Install skills on the machine:

```bash
fastflow skills install
```

2. Update the repo's `orchestrator.json` to use `skill` fields instead of `prompt_file` and `requires`

Then verify:

```bash
fastflow validate
```

If your repo is still using the stock generated `orchestrator.json`, you can refresh it with:

```bash
fastflow init --force
```

If you have customized `orchestrator.json`, update it manually instead of overwriting it.

### Minimal config migration example

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

### Optional cleanup for existing repos

After migrating an older repo, you can delete these legacy directories if they are still present:

```text
.claude/commands/
.claude/stages/
.claude/agents/
```

They are no longer used by v0.6.

## Acknowledgements

The workflow prompts and orchestration patterns in this project were inspired by [HumanLayer](https://github.com/humanlayer/humanlayer).

## License

MIT License - see [LICENSE](LICENSE) for details.
