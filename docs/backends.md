# Harnesses

fastflow can drive coding-agent CLIs through harnesses. Each stage runs against
the harness named in its `harness` field, falling back to the top-level
`harness`, which defaults to `"claude"`.

`backend`, `judge_backend`, and `backends` are still accepted as deprecated
aliases for existing configs. New configs should use `harness`,
`judge_harness`, and `harnesses`.

## Configuration

```json
{
  "harness": "claude",
  "judge_harness": "claude",
  "harnesses": {
    "claude": { "binary": "claude", "default_model": "sonnet" },
    "codex":  {
      "binary": "codex",
      "default_model": "gpt-5.3-codex-spark",
      "codex": {
        "model_context_window": 128000,
        "context_handoff_threshold_percent": 50,
        "dangerously_bypass_approvals_and_sandbox": true
      }
    }
  },
  "stages": {
    "research": {
      "harness": "claude",
      "model": "sonnet",
      "prompt_file": ".claude/stages/research.md",
      "backup": [
        { "harness": "codex", "model": "gpt-5.3-codex", "prompt_file": ".codex/stages/research.md" }
      ],
      "escalation": [
        { "harness": "claude", "model": "opus", "prompt_file": ".claude/stages/research-escalated.md" }
      ]
    },
    "implement-codex": {
      "harness": "codex",
      "prompt_file": ".codex/stages/implement.md"
    }
  }
}
```

| Field | Default | Description |
|---|---|---|
| `harness` | `"claude"` | Global default harness. |
| `judge_harness` | same as `harness` | Harness used for judge evaluations. |
| `harnesses.<name>.binary` | same as harness name | Override the binary path/name. |
| `harnesses.<name>.default_model` | harness-specific | Override the model used when `stage.model` is empty. |
| `harnesses.codex.codex.model_context_window` | unset | Context window used for FastFlow's Codex handoff percentage math. |
| `harnesses.codex.codex.context_handoff_threshold_percent` | unset | After a completed Codex turn reports usage at or above this percentage, FastFlow resumes the session to run `ff_create_handoff`, then starts a fresh session through `ff_resume_handoff`. |
| `harnesses.codex.codex.model_auto_compact_token_limit` | Codex default | Optional direct passthrough to Codex CLI's auto-compaction token threshold; use only as a higher last-resort backstop if desired. |
| `harnesses.codex.codex.tool_output_token_limit` | Codex default | Optional direct passthrough to Codex CLI's tool output token limit. |
| `harnesses.codex.codex.sandbox` | Codex default | Optional Codex CLI sandbox mode passthrough. Valid values are `read-only`, `workspace-write`, and `danger-full-access`. |
| `harnesses.codex.codex.dangerously_bypass_approvals_and_sandbox` | `false` | When true, FastFlow passes `--dangerously-bypass-approvals-and-sandbox` to Codex CLI instead of `--full-auto`, giving the nested Codex run local/no-sandbox behavior. Use only in trusted worktrees. |
| `stages.<name>.harness` | global `harness` | Per-stage harness override. |
| `stages.<name>.model` | harness default model | Primary model for a stage. |
| `stages.<name>.backup` | none | Ordered attempts for transient harness/model failures such as rate limits, capacity, unavailable models, or 5xx errors. |
| `stages.<name>.escalation` | none | Ordered attempts used only after the judge rejects a completed stage. |

Each `backup` or `escalation` entry requires a `model` and may set `harness`,
`prompt_file`, `skill`, or `requires`. If `harness` is omitted, the attempt
uses the stage's effective harness.

Existing `orchestrator.json` files that omit these fields continue to work
unchanged. The deprecated `backend` field still defaults to `"claude"` and is
treated as an alias for `harness`.
The deprecated `backup_models` and `escalation_models` fields are also still
accepted as aliases for `backup` and `escalation`.

## New Config Values

These values are available for new configs:

- `harness`: top-level default harness for stage execution. Supported values
  today are `claude` and `codex`; additional harnesses can be registered in
  code.
- `judge_harness`: harness used by the judge. It defaults to `harness` when
  omitted.
- `harnesses`: per-harness settings keyed by harness name. Each entry can set
  common fields such as `binary` and `default_model`. Harness-specific behavior
  is nested under that harness's own key, such as `harnesses.codex.codex`.
- `stages.<name>.harness`: per-stage harness override. This is the preferred
  spelling for what older configs expressed as `backend`.
- `stages.<name>.backup`: ordered fallback attempts for transient harness/model
  errors. Use this for rate limits, capacity failures, unavailable models, and
  similar retryable failures.
- `stages.<name>.escalation`: ordered retry attempts used only when a stage
  completed but the judge rejected the result.

`backup` and `escalation` entries have this shape:

```json
{
  "harness": "codex",
  "model": "gpt-5.3-codex",
  "prompt_file": ".codex/stages/implement.md",
  "requires": [".codex/skills/implement/SKILL.md"]
}
```

Only `model` is required. `harness` defaults to the stage's effective harness,
and `prompt_file`, `skill`, and `requires` let an attempt switch to a
harness-specific prompt path when needed. Do not set both `prompt_file` and
`skill` on the same attempt.

> **Important:** Setting `"harness": "codex"` at the top level does **not** make
> the default workflows (`full`, `plan-first`, `debug`, `quick`, `review`) work
> with Codex. Those workflows use Claude slash-command prompts under
> `.claude/stages/`. Use the Codex-specific workflows (`codex-quick`,
> `codex-full`, `codex-debug`, `codex-review`, `codex-review-with-replies`, or
> `codex-merge-conflict`), which use `.codex/stages/` prompts and
> `.codex/skills/`, or define custom Codex-specific stages.

## Capability Matrix

| Capability | Claude Code | Codex CLI | GitHub Copilot CLI* |
|---|---|---|---|
| `--max-budget-usd` | yes | no | no |
| `--max-turns` | yes | no | no |
| Session resume | yes (`--continue`) | yes (`exec resume --last`) | yes (`--continue`) |
| Streaming tool activity | yes (`stream-json`) | yes (`--json` events) | no (batch mode collapses output) |
| Slash-command skills | yes | no; Codex prompt files can invoke native `$skill` skills | no |
| Auth env var | `ANTHROPIC_API_KEY` (with claude.ai fallback) | `OPENAI_API_KEY` | `GH_TOKEN` / `GITHUB_TOKEN` / `COPILOT_GITHUB_TOKEN` |

\* Copilot CLI capability mapping is documented for future implementation only.

## Fallback Behavior When a Harness Lacks a Feature

- **Budget cap not supported** (`SupportsBudget=false`): a `WARN` is printed at
  stage start; the stage proceeds without a cap. Only relevant if `maxBudgetUsd`
  is configured on a Codex stage.
- **Slash-command skills not supported** (`SupportsSlashCommands=false`):
  `stage.skill` is handled by resolving and inlining
  `.<harness>/commands/<skill>.md` or `.claude/commands/<skill>.md`. Codex
  prompt files can also invoke native Codex skills with `$skill` from
  `.codex/skills/<skill>/SKILL.md`. The handoff cycle
  (create_handoff / resume_handoff) is skipped with a warning; partial output
  is returned to the judge instead.
- **Resume not supported** (`SupportsResume=false`): the budget-handoff cycle
  returns a clear error. Currently only defensive because budget caps only
  trigger for Claude.
- **Streaming not supported** (`SupportsStreaming=false`): `--verbose` is a
  no-op for that harness; the runner still surfaces the final `Output` text.

## Setup

### Claude Code (default)

1. Install: <https://claude.ai/claude-code>
2. Authenticate: run `claude` then `/login`, or for headless:
   ```bash
   claude setup-token
   ```
3. Use the included `.claude/` templates created by `fastflow init`.

No additional configuration is needed. Omitting `"harness"` from
`orchestrator.json` uses Claude automatically.

### Codex CLI

1. Install: <https://github.com/openai/codex>
2. Authenticate:
   ```bash
   codex login
   # or set OPENAI_API_KEY in your environment
   ```
3. Use the included Codex stage prompts under `.codex/stages/` and native
   Codex skills under `.codex/skills/`, or author your own.
4. Use a built-in Codex workflow such as `codex-quick` or `codex-full`, or set
   `"harness": "codex"` on specific stages. Do not set Codex as the global
   harness for Claude-oriented workflows unless every stage has a Codex prompt.

Codex does not support `--max-turns` or `--max-budget-usd`. fastflow ignores
those fields on Codex stages with a runtime warning.

Codex reports token usage at `turn.completed` in its JSON stream. To keep Codex
sessions around 40-60% context usage without relying on Codex auto-compaction,
set `harnesses.codex.codex`:

```json
{
  "harnesses": {
    "codex": {
      "binary": "codex",
      "default_model": "gpt-5.3-codex-spark",
      "codex": {
        "model_context_window": 128000,
        "context_handoff_threshold_percent": 50
      }
    }
  }
}
```

At runtime fastflow uses Codex's reported `input_tokens + output_tokens` as an
approximation. When that total is 50% or more of `model_context_window`, it
resumes the current Codex session to run `ff_create_handoff`, then starts a
fresh session through `ff_resume_handoff`. This avoids Codex auto-compaction in
the normal path; `model_auto_compact_token_limit` remains available only as an
optional Codex backstop.

### GitHub Copilot CLI Status

Copilot CLI exposes `-p`/`--prompt` for non-interactive runs and
`--output-format json|text`. The blocking gap today is that Copilot's batch
mode is documented to collapse intermediate output, which fastflow's judge
needs in full to evaluate stages reliably.

A Copilot harness is not implemented in this release. When upstream batch-mode
output is fixed, the implementation would mirror the Codex harness:

| Substitution | Value |
|---|---|
| Binary | `copilot` |
| Args | `-p <prompt> --output-format json --allow-all-tools --no-ask-user` |
| Resume | `--continue` (already supported) |
| Auth detection | `GH_TOKEN` / `COPILOT_GITHUB_TOKEN` |
| Capabilities | `SupportsBudget=false, SupportsMaxTurns=false, SupportsResume=true, SupportsStreaming=false, SupportsSlashCommands=false` |

## Adding a New Harness

1. Create `internal/harness/<name>/` with at least:
   - `<name>.go` with a `Harness` struct implementing `harness.Harness`
   - `init.go` that calls `harness.Register("<name>", ...)` from `init()`
2. Add a blank import in `cmd/fastflow/main.go`:
   ```go
   _ "github.com/redblacktree/fastflow/internal/harness/<name>"
   ```
3. Add tests under `internal/harness/<name>/` covering:
   - `Capabilities()` values
   - Auth error classification
   - Any custom event parsing
4. Document the harness in this file and update the capability matrix.

The `harness.Harness` interface is intentionally small:

```go
type Harness interface {
    Name() string
    Capabilities() Capabilities
    DefaultModel() string
    Invoke(opts InvokeOptions) (*InvokeResult, error)
}
```

`InvokeOptions` fields not honored by a harness are silently ignored; consult
`Capabilities()` before relying on them. Return `harness.ErrNotLoggedIn`,
`harness.ErrInvalidAPIKey`, or `harness.ErrRateLimited` (or wrap them) from
`Invoke` so callers can switch on `errors.Is`.
