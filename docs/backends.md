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
    "codex":  { "binary": "codex",  "default_model": "o4-mini" }
  },
  "stages": {
    "research": {
      "harness": "claude",
      "model": "sonnet",
      "backup_models": [
        { "harness": "claude", "model": "opus" }
      ],
      "escalation_models": [
        { "harness": "claude", "model": "opus" }
      ],
      "prompt_file": ".claude/stages/research.md"
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
| `stages.<name>.harness` | global `harness` | Per-stage harness override. |
| `stages.<name>.model` | harness default model | Primary model for a stage. |
| `stages.<name>.backup_models` | none | Ordered attempts for transient harness/model failures such as rate limits, capacity, unavailable models, or 5xx errors. |
| `stages.<name>.escalation_models` | none | Ordered attempts used only after the judge rejects a completed stage. |

Each `backup_models` or `escalation_models` entry requires a `model` and may
set `harness`, `prompt_file`, `skill`, or `requires`. If `harness` is omitted,
the attempt uses the stage's effective harness.

Existing `orchestrator.json` files that omit these fields continue to work
unchanged. The deprecated `backend` field still defaults to `"claude"` and is
treated as an alias for `harness`.

## New Config Values

These values are available for new configs:

- `harness`: top-level default harness for stage execution. Supported values
  today are `claude` and `codex`; additional harnesses can be registered in
  code.
- `judge_harness`: harness used by the judge. It defaults to `harness` when
  omitted.
- `harnesses`: per-harness settings keyed by harness name. Each entry can set
  `binary` and `default_model`.
- `stages.<name>.harness`: per-stage harness override. This is the preferred
  spelling for what older configs expressed as `backend`.
- `stages.<name>.backup_models`: ordered fallback attempts for transient
  harness/model errors. Use this for rate limits, capacity failures,
  unavailable models, and similar retryable failures.
- `stages.<name>.escalation_models`: ordered retry attempts used only when a
  stage completed but the judge rejected the result.

`backup_models` and `escalation_models` entries have this shape:

```json
{
  "harness": "claude",
  "model": "opus",
  "prompt_file": ".claude/stages/implement.md",
  "requires": [".claude/commands/ff_implement_plan.md"]
}
```

Only `model` is required. `harness` defaults to the stage's effective harness,
and `prompt_file`, `skill`, and `requires` let an attempt switch to a
harness-specific prompt path when needed. Do not set both `prompt_file` and
`skill` on the same attempt.

> **Important:** Setting `"harness": "codex"` at the top level does **not** make
> the default workflows (`full`, `plan-first`, `debug`, `quick`, `review`) work
> with Codex. Those workflows use Claude slash-command skills (e.g.
> `/ff_create_plan`, `/ff_implement_plan`) that Codex cannot resolve. Codex is
> only supported via the dedicated `codex-quick` workflow (and its
> `implement-codex` / `commit-codex` stages) or custom Codex-specific stages
> you author yourself. Use `"harness"` as a per-stage override, not a global
> drop-in replacement, unless you have authored Codex-compatible prompts for
> every stage.

## Capability Matrix

| Capability | Claude Code | Codex CLI | GitHub Copilot CLI* |
|---|---|---|---|
| `--max-budget-usd` | yes | no | no |
| `--max-turns` | yes | no | no |
| Session resume | yes (`--continue`) | yes (`exec resume --last`) | yes (`--continue`) |
| Streaming tool activity | yes (`stream-json`) | yes (`--json` events) | no (batch mode collapses output) |
| Slash-command skills | yes | no (skill body is inlined) | no |
| Auth env var | `ANTHROPIC_API_KEY` (with claude.ai fallback) | `OPENAI_API_KEY` | `GH_TOKEN` / `GITHUB_TOKEN` / `COPILOT_GITHUB_TOKEN` |

\* Copilot CLI capability mapping is documented for future implementation only.

## Fallback Behavior When a Harness Lacks a Feature

- **Budget cap not supported** (`SupportsBudget=false`): a `WARN` is printed at
  stage start; the stage proceeds without a cap. Only relevant if `maxBudgetUsd`
  is configured on a Codex stage.
- **Slash-command skills not supported** (`SupportsSlashCommands=false`): the
  harness resolves the skill body from `.<harness>/commands/<skill>.md`
  (preferred) or `.claude/commands/<skill>.md` (fallback), strips YAML
  frontmatter, and inlines the body before the skill context. The handoff
  cycle (create_handoff / resume_handoff) is skipped with a warning; partial
  output is returned to the judge instead.
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
3. Author Codex-friendly stage prompts under `.codex/stages/` that do not rely
   on `/slash-commands`. See `internal/templates/files/.codex/stages/` for
   starter templates.
4. Set `"harness": "codex"` on the specific stages, or use the built-in
   `codex-quick` workflow. Do not set it as the global `harness` unless every
   stage in your workflow has a Codex-compatible prompt file.

Codex does not support `--max-turns` or `--max-budget-usd`. fastflow ignores
those fields on Codex stages with a runtime warning.

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
