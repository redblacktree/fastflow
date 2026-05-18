package harness

import (
	"fmt"
	"sort"
)

// Config is the per-harness configuration block from orchestrator.json.
type Config struct {
	Binary       string `json:"binary,omitempty"`        // override binary path; empty = harness default
	DefaultModel string `json:"default_model,omitempty"` // override DefaultModel(); empty = harness default

	Codex CodexConfig `json:"codex,omitempty"`
}

// CodexConfig contains Codex CLI-specific configuration passthroughs.
type CodexConfig struct {
	ModelContextWindow         int     `json:"model_context_window,omitempty"`
	ModelAutoCompactTokenLimit int     `json:"model_auto_compact_token_limit,omitempty"`
	ContextHandoffThresholdPct float64 `json:"context_handoff_threshold_percent,omitempty"`
	ToolOutputTokenLimit       int     `json:"tool_output_token_limit,omitempty"`
	Sandbox                    string  `json:"sandbox,omitempty"`
	BypassApprovalsAndSandbox  bool    `json:"dangerously_bypass_approvals_and_sandbox,omitempty"`
}

// Constructor builds a Harness from its config block.
type Constructor func(cfg Config) (Harness, error)

var registry = map[string]Constructor{}

// Register adds a harness constructor under a name. Called from each
// harness package's init().
func Register(name string, ctor Constructor) {
	registry[name] = ctor
}

// New returns a Harness for the given name, or an error if not registered.
func New(name string, cfg Config) (Harness, error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q (registered: %v)", name, Names())
	}
	return ctor(cfg)
}

// Names returns the sorted list of registered harness names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
