# Backends

fastflow can drive any of several coding-agent CLIs as a backend. Each stage
runs against the backend named in its `backend` field, falling back to the
top-level `backend` field, which defaults to `"claude"`.

## Configuration

```json
{
  "backend": "claude",
  "judge_backend": "claude",
  "backends": {
    "claude": { "binary": "claude", "default_model": "sonnet" },
    "codex":  { "binary": "codex",  "default_model": "o4-mini" }
  },
  "stages": {
    "research":        { "backend": "claude", "model": "sonnet", "prompt_file": ".claude/stages/research.md" },
    "implement-codex": { "backend": "codex",  "prompt_file": ".codex/stages/implement.md" }
  }
}
```

| Field | Default | Description |
|---|---|---|
| `backend` | `"claude"` | Global default backend; omit to keep using Claude. |
| `judge_backend` | same as `backend` | Backend used for judge evaluations; falls back to `backend`. |
| `backends.<name>.binary` | same as backend name | Override the binary path/name. |
| `backends.<name>.default_model` | backend-specific | Override the model used when `stage.model` is empty. |
| `stages.<name>.backend` | global `backend` | Per-stage backend override. |

Existing `orchestrator.json` files that omit these fields continue to work
unchanged — the `backend` field defaults to `"claude"`.

> **Important:** Setting `"backend": "codex"` at the top level does **not** make
> the default workflows (`full`, `plan-first`, `debug`, `quick`, `review`) work
> with Codex. Those workflows use Claude slash-command skills (e.g.
> `/ff_create_plan`, `/ff_implement_plan`) that Codex cannot resolve. Codex is
> only supported via the dedicated `codex-quick` workflow (and its
> `implement-codex` / `commit-codex` stages) or custom Codex-specific stages
> you author yourself. Use `"backend"` as a per-stage override, not a global
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
See "GitHub Copilot CLI status" below.

## Fallback Behavior When a Backend Lacks a Feature

- **Budget cap not supported** (`SupportsBudget=false`): a `WARN` is printed at
  stage start; the stage proceeds without a cap. Only relevant if `maxBudgetUsd`
  is configured on a Codex stage.
- **Slash-command skills not supported** (`SupportsSlashCommands=false`): the
  backend resolves the skill body from `.<backend>/commands/<skill>.md`
  (preferred) or `.claude/commands/<skill>.md` (fallback), strips YAML
  frontmatter, and inlines the body before the skill context. The handoff
  cycle (create_handoff / resume_handoff) is skipped with a warning; partial
  output is returned to the judge instead.
- **Resume not supported** (`SupportsResume=false`): the budget-handoff cycle
  returns a clear error. Currently only defensive — budget caps only trigger
  for Claude.
- **Streaming not supported** (`SupportsStreaming=false`): `--verbose` is a
  no-op for that backend; the runner still surfaces the final `Output` text.

## Setup

### Claude Code (default)

1. Install: <https://claude.ai/claude-code>
2. Authenticate: run `claude` then `/login` — or for headless:
   ```bash
   claude setup-token
   ```
3. Use the included `.claude/` templates created by `fastflow init`.

No additional configuration is needed. Omitting `"backend"` from
`orchestrator.json` uses Claude automatically.

### Codex CLI

1. Install: <https://github.com/openai/codex>
2. Authenticate:
   ```bash
   codex login
   # or set OPENAI_API_KEY in your environment
   ```
3. Author Codex-friendly stage prompts under `.codex/stages/` that **do not
   rely on `/slash-commands`**. See
   `internal/templates/files/.codex/stages/` for starter templates.
4. Set `"backend": "codex"` on the specific stages (or use the built-in
   `codex-quick` workflow). Do **not** set it as the global `backend` unless
   every stage in your workflow has a Codex-compatible prompt file that does
   not use Claude slash-command skills.

Codex does not support `--max-turns` or `--max-budget-usd`. fastflow ignores
those fields on Codex stages with a runtime warning.

### GitHub Copilot CLI Status

Copilot CLI exposes `-p`/`--prompt` for non-interactive runs and
`--output-format json|text`. The blocking gap today is that Copilot's batch
mode is documented to collapse intermediate output, which fastflow's judge
needs in full to evaluate stages reliably.

A Copilot backend is **not implemented** in this release. When upstream
batch-mode output is fixed, the implementation would mirror the Codex backend:

| Substitution | Value |
|---|---|
| Binary | `copilot` |
| Args | `-p <prompt> --output-format json --allow-all-tools --no-ask-user` |
| Resume | `--continue` (already supported) |
| Auth detection | `GH_TOKEN` / `COPILOT_GITHUB_TOKEN` |
| Capabilities | `SupportsBudget=false, SupportsMaxTurns=false, SupportsResume=true, SupportsStreaming=false, SupportsSlashCommands=false` |

## Adding a New Backend

1. Create `internal/backend/<name>/` with at least:
   - `<name>.go` — `Backend` struct implementing `backend.Backend`
   - `init.go` — calls `backend.Register("<name>", ...)` from `init()`
2. Add a blank import in `cmd/fastflow/main.go`:
   ```go
   _ "github.com/redblacktree/fastflow/internal/backend/<name>"
   ```
3. Add tests under `internal/backend/<name>/` covering:
   - `Capabilities()` values
   - Auth error classification
   - Any custom event parsing
4. Document the backend in this file and update the capability matrix.

The `backend.Backend` interface is intentionally small:

```go
type Backend interface {
    Name() string
    Capabilities() Capabilities
    DefaultModel() string
    Invoke(opts InvokeOptions) (*InvokeResult, error)
}
```

`InvokeOptions` fields not honored by a backend are silently ignored; consult
`Capabilities()` before relying on them. Return `backend.ErrNotLoggedIn`,
`backend.ErrInvalidAPIKey`, or `backend.ErrRateLimited` (or wrap them) from
`Invoke` so callers can switch on `errors.Is`.
