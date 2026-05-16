// Package harness defines the interface fastflow uses to invoke a coding agent CLI.
package harness

// InvokeOptions carries everything a harness needs to run one invocation.
// Fields not supported by a given harness are silently ignored; consumers
// should consult Capabilities() before relying on them.
type InvokeOptions struct {
	Prompt       string
	Model        string
	WorkDir      string
	MaxTurns     int     // 0 = harness default
	MaxBudgetUsd float64 // 0 = no budget cap; harnesses without budget support ignore
	Verbose      bool    // request streaming tool activity if harness supports it
	Debug        bool

	// Skill, if non-empty, asks the harness to invoke a named skill/command
	// with the given context. Harnesses translate this to their native form
	// (e.g. Claude prepends "/<skill>\n\n", Codex inlines a resolved file).
	Skill        string
	SkillContext string

	// Continue, if true, asks the harness to resume the most recent session
	// in WorkDir (Claude --continue, codex exec resume --last). Harnesses
	// without resume support return an error if Continue is true.
	Continue bool
}

// InvokeResult is the shared shape returned by every harness.
type InvokeResult struct {
	Output       string // primary text result for downstream consumers (judge, prompt builders)
	RawOutput    string // full output incl. stderr & non-structured lines, for auth/error sniffing
	ExitCode     int
	Harness      string // harness that produced this result, set by the runner when applicable
	Model        string // model that produced this result, set by the runner when applicable
	HitMaxTurns  bool
	HitBudgetCap bool
	// HitContextHandoff is set when a harness reports approximate context usage
	// above its configured handoff threshold.
	HitContextHandoff bool
	SessionID         string
	TotalCostUsd      float64 // 0 if harness doesn't report cost
	ContextTokens     int     // approximate tokens currently consuming context
	ContextWindow     int     // configured context window used for percentage math
	ContextPercent    float64
}

// Capabilities advertises what a harness can and cannot do, so the runner
// and config validator can warn or degrade instead of silently misbehaving.
type Capabilities struct {
	SupportsBudget        bool // honors MaxBudgetUsd and reports HitBudgetCap
	SupportsMaxTurns      bool // honors MaxTurns and reports HitMaxTurns
	SupportsResume        bool // honors InvokeOptions.Continue
	SupportsStreaming     bool // emits tool activity when Verbose=true
	SupportsSlashCommands bool // can invoke a Skill via slash-command syntax (Claude-style)
}

// Harness invokes a coding agent CLI.
type Harness interface {
	Name() string
	Capabilities() Capabilities
	DefaultModel() string
	Invoke(opts InvokeOptions) (*InvokeResult, error)
}

// Common sentinel errors that harnesses may return. Concrete harnesses embed
// their own descriptive text but should wrap one of these where applicable
// so callers can switch on errors.Is().
var (
	ErrNotLoggedIn   = errSentinel("harness: not authenticated")
	ErrRateLimited   = errSentinel("harness: rate limit reached")
	ErrInvalidAPIKey = errSentinel("harness: API key rejected")
	ErrUnsupported   = errSentinel("harness: requested feature not supported")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
