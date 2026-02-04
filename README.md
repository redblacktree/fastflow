# FastFlow Orchestrator

A workflow orchestration system for Claude Code that manages multi-stage software development workflows.

## Overview

This orchestrator provides structured workflows for software development tasks, including:

- Research and codebase exploration
- Implementation planning
- Code implementation
- Validation and testing
- Git commit and PR creation

## Workflows

The orchestrator supports multiple workflow patterns defined in `orchestrator.json`:

- **full**: Complete workflow (research → plan → implement → validate → commit)
- **plan-first**: Skip research, start with planning
- **debug**: Specialized workflow for bug fixes
- **quick**: Fast-track for simple changes

## Acknowledgements

The prompts and workflow structure in this project were heavily inspired by and adapted from [HumanLayer](https://github.com/humanlayer/humanlayer). We are grateful for their work in pioneering effective agent orchestration patterns.

## Configuration

- Stage prompts: `.claude/stages/`
- Command prompts: `.claude/commands/`
- Workflow definitions: `orchestrator.json`
