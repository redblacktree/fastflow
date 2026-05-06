# fastflow

A multi-agent development workflow orchestrator that automates the full development lifecycle from goal to PR.

## Overview

fastflow orchestrates development workflows by running specialized stages in isolated coding-agent contexts:

```
Goal → Worktree → Research → Plan → Implement → Validate → Commit/PR
```

Each stage communicates via markdown files in `thoughts/`, enabling clean handoffs between agent contexts.

The default backend is **Claude Code**. You can also run stages using **OpenAI Codex CLI** by
setting `"backend": "codex"` on individual stages (or using the built-in `codex-quick` workflow).
The default Claude workflows use Claude slash-command skills that Codex cannot execute, so Codex
works per-stage rather than as a global drop-in. See [docs/backends.md](docs/backends.md) for the
full capability matrix, setup instructions, and how to add new backends.

## Prerequisites

- A coding agent CLI installed and authenticated. Supported backends:
  - **Claude Code** (default): <https://claude.ai/claude-code>
  - **Codex CLI**: <https://github.com/openai/codex>

## Authentication

### Claude Code (default)

Claude Code must be authenticated before fastflow can run. For headless or automated environments
(Linux VMs, CI, containers) where a browser-based login isn't feasible, use `setup-token` to create
a long-lived authentication token:

```bash
claude setup-token
```

This avoids the interactive OAuth flow that requires a browser. Without a valid session, fastflow
will fail immediately with:

```
claude is not logged in: please run 'claude' interactively and use /login to authenticate
```

### Codex CLI

```bash
codex login
# or set OPENAI_API_KEY in your environment
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

### Background runs

To launch a workflow in the background (detached from the terminal), pass `--background`:

```bash
fastflow run --goal "Add user authentication" --ticket ENG-1234 --background
```

fastflow re-execs itself as a detached process, writes all output to `fastflow.log` inside the run directory, and returns immediately. Use `fastflow monitor` to track progress.

### Workspace trust

By default `fastflow run` only starts from a git workspace that has an `origin` remote. This catches accidental runs from scratch directories or stale mirrors before fastflow creates state or worktrees. In environments where runs must also be confined away from specific paths (for example, to prevent a stale `.openclaw` workspace mirror from being targeted accidentally), add blocked-prefix guardrails:

```bash
# Block a specific path prefix at invocation time
fastflow run --goal "..." --ticket ENG-1 \
  --blocked-workspace-prefix /Users/me/.openclaw

# Block multiple prefixes (repeat the flag)
fastflow run --goal "..." --ticket ENG-1 \
  --blocked-workspace-prefix /tmp \
  --blocked-workspace-prefix /private/tmp
```

The same prefixes can be set via the environment variable `FASTFLOW_BLOCKED_WORKSPACE_PREFIXES` (colon-separated list):

```bash
export FASTFLOW_BLOCKED_WORKSPACE_PREFIXES=/Users/me/.openclaw:/tmp
fastflow run --goal "..." --ticket ENG-1
```

If the resolved working directory has no git `origin` remote or falls under any blocked prefix, `fastflow run` exits immediately with a non-zero status. To bypass the workspace-trust checks in a trusted one-off scenario, pass `--allow-untrusted-workspace` (or set `FASTFLOW_ALLOW_UNTRUSTED_WORKSPACE=1`).

### Validate configuration

```bash
fastflow validate
```

### Run an on-completion hook

Use `--on-complete` to fire a shell command after a run finishes (success, failure, or signal-driven termination). Useful for Slack notifications, hand-off automation, or cleanup tasks for long-lived runs.

```bash
fastflow run --goal "Add feature X" --ticket ENG-1234 \
  --on-complete 'curl -X POST -d "ticket=$FASTFLOW_TICKET status=$FASTFLOW_STATUS" https://hooks.slack.com/services/...'
```

The command is executed via `sh -c` and receives these environment variables:

| Variable            | Value                                                        |
|---------------------|--------------------------------------------------------------|
| `FASTFLOW_TICKET`   | The ticket identifier passed via `--ticket`                  |
| `FASTFLOW_STATUS`   | `complete` on success, `failed` on error or signal           |
| `FASTFLOW_WORKTREE` | Absolute path to the run's working directory (worktree root) |
| `FASTFLOW_RUN_DIR`  | Absolute path to the run artifact directory (`thoughts/...`) |
| `FASTFLOW_ERROR`    | Error message when `STATUS=failed`, empty on success         |
| `FASTFLOW_WORKFLOW` | Workflow name (e.g., `full`, `plan-first`)                   |
| `FASTFLOW_PR_URL`   | Reserved (currently empty)                                   |

Behavior:

- The hook fires on every terminal outcome, including `SIGTERM`/`SIGINT` shutdown of long-lived `--background` runs.
- The command is persisted in `state.json`, so it survives `--resume`.
- The command has a 5-minute execution budget; if it exceeds that, it is killed with a warning and the run continues to exit normally.
- Hook failures (non-zero exit, timeout) are logged as warnings and do **not** affect fastflow's own exit code.

Examples:

```bash
# Append outcome to a log file
fastflow run --ticket ENG-100 --goal "..." \
  --on-complete 'echo "$(date) $FASTFLOW_TICKET=$FASTFLOW_STATUS" >> ~/fastflow.log'

# Open the run directory in your editor on failure
fastflow run --ticket ENG-101 --goal "..." \
  --on-complete '[ "$FASTFLOW_STATUS" = "failed" ] && code "$FASTFLOW_RUN_DIR"'

# Fire-and-forget Slack notification (background run)
fastflow run --ticket ENG-102 --goal "..." --background \
  --on-complete 'curl -fsSL -X POST -H "Content-Type: application/json" \
    -d "{\"text\":\"$FASTFLOW_TICKET finished: $FASTFLOW_STATUS\"}" \
    "$SLACK_WEBHOOK_URL"'
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
- Configuring stage models and backends
- Setting up checkpoints for human review
- Custom judge prompts for stage validation

See [docs/backends.md](docs/backends.md) for backend setup, the capability matrix, and fallback behavior.

### fastflow reconcile stale-handoff

Detects parent Linear issues that have a `Q HANDOFF / RETEST READY` comment but are missing their QA and Review child lanes. In dry-run mode (default) it prints a fact-backed proposal listing exactly what would be created. With `--apply` it executes the repair exactly once: creates a `QA: <title>` child (assigned to the QA owner), creates a `Review: <title>` child (assigned to the reviewer), moves the parent to `In Review`, copies the canonical handoff boundary onto the QA child, and posts a repair pointer on the parent. Re-running on a post-repair parent is a no-op.

**Required environment variables**

| Variable | Flag override | Description |
|---|---|---|
| `LINEAR_API_KEY` | `--linear-token` | Linear personal API token |
| `LINEAR_TEAM_ID` | `--team` | Linear team ID |
| `LINEAR_IN_REVIEW_STATE_ID` | `--in-review-state` | Workflow state UUID for "In Review" |
| `LINEAR_QA_ASSIGNEE_ID` | `--qa-assignee` | Linear user UUID for the QA child |
| `LINEAR_REVIEW_ASSIGNEE_ID` | `--review-assignee` | Linear user UUID for the Review child |

**Examples**

```bash
# Dry-run — inspect the proposal without making any changes
LINEAR_API_KEY=... fastflow reconcile stale-handoff --issue REL-548

# Apply the repair
LINEAR_API_KEY=... \
  LINEAR_TEAM_ID=... \
  LINEAR_IN_REVIEW_STATE_ID=... \
  LINEAR_QA_ASSIGNEE_ID=... \
  LINEAR_REVIEW_ASSIGNEE_ID=... \
  fastflow reconcile stale-handoff --issue REL-548 --apply
```

## Acknowledgements

The workflow prompts and orchestration patterns in this project were inspired by [HumanLayer](https://github.com/humanlayer/humanlayer).

## License

MIT License - see [LICENSE](LICENSE) for details.
