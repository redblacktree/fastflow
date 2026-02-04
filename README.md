# fastflow

A multi-agent development workflow orchestrator that automates the full development lifecycle from goal to PR.

## Overview

fastflow orchestrates development workflows by running specialized stages in isolated Claude Code contexts:

```
Goal → Worktree → Research → Plan → Implement → Validate → Commit/PR
```

Each stage communicates via markdown files in `thoughts/`, enabling clean handoffs between agent contexts.

## Installation

```bash
go install github.com/dustinrasener/fastflow/cmd/fastflow@latest
```

Or build from source:

```bash
git clone https://github.com/dustinrasener/fastflow.git
cd fastflow
go build -o fastflow ./cmd/fastflow
```

## Prerequisites

- Go 1.25.5+
- [Claude Code](https://claude.ai/claude-code) CLI installed and authenticated

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

## Configuration

fastflow uses `orchestrator.json` for workflow configuration. See the included config for examples of:

- Defining workflows and their stages
- Configuring stage models (sonnet, opus, haiku)
- Setting up checkpoints for human review
- Custom judge prompts for stage validation

## License

MIT License - see [LICENSE](LICENSE) for details.
