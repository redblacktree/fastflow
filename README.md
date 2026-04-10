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

## Acknowledgements

The workflow prompts and orchestration patterns in this project were inspired by [HumanLayer](https://github.com/humanlayer/humanlayer).

## License

MIT License - see [LICENSE](LICENSE) for details.
