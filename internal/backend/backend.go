// Package backend defines the interface fastflow uses to invoke a coding agent CLI.
package backend

// InvokeOptions carries everything a backend needs to run one invocation.
// Fields not supported by a given backend are silently ignored; consumers
// should consult Capabilities() before relying on them.
type InvokeOptions struct {
	Prompt       string
	Model        string
	WorkDir      string
	MaxTurns     int     // 0 = backend default
	MaxBudgetUsd float64 // 0 = no budget cap; backends without budget support ignore
	Verbose      bool    // request streaming tool activity if backend supports it
	Debug        bool

	// Skill, if non-empty, asks the backend to invoke a named skill/command
	// with the given context. Backends translate this to their native form
	// (e.g. Claude prepends "/<skill>\n\n", Codex inlines a resolved file).
	Skill        string
	SkillContext string

	// Continue, if true, asks the backend to resume the most recent session
	// in WorkDir (Claude --continue, codex exec resume --last). Backends
	// without resume support return an error if Continue is true.
	Continue bool
}

// InvokeResult is the shared shape returned by every backend.
type InvokeResult struct {
	Output       string  // primary text result for downstream consumers (judge, prompt builders)
	RawOutput    string  // full output incl. stderr & non-structured lines, for auth/error sniffing
	ExitCode     int
	HitMaxTurns  bool
	HitBudgetCap bool
	SessionID    string
	TotalCostUsd float64 // 0 if backend doesn't report cost
}

// Capabilities advertises what a backend can and cannot do, so the runner
// and config validator can warn or degrade instead of silently misbehaving.
type Capabilities struct {
	SupportsBudget        bool // honors MaxBudgetUsd and reports HitBudgetCap
	SupportsMaxTurns      bool // honors MaxTurns and reports HitMaxTurns
	SupportsResume        bool // honors InvokeOptions.Continue
	SupportsStreaming     bool // emits tool activity when Verbose=true
	SupportsSlashCommands bool // can invoke a Skill via slash-command syntax (Claude-style)
}

// Backend invokes a coding agent CLI.
type Backend interface {
	Name() string
	Capabilities() Capabilities
	DefaultModel() string
	Invoke(opts InvokeOptions) (*InvokeResult, error)
}

// Common sentinel errors that backends may return. Concrete backends embed
// their own descriptive text but should wrap one of these where applicable
// so callers can switch on errors.Is().
var (
	ErrNotLoggedIn   = errSentinel("backend: not authenticated")
	ErrRateLimited   = errSentinel("backend: rate limit reached")
	ErrInvalidAPIKey = errSentinel("backend: API key rejected")
	ErrUnsupported   = errSentinel("backend: requested feature not supported")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
