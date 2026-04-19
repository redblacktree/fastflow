package backend

import (
	"fmt"
	"sort"
)

// Config is the per-backend configuration block from orchestrator.json.
type Config struct {
	Binary       string `json:"binary,omitempty"`        // override binary path; empty = backend default
	DefaultModel string `json:"default_model,omitempty"` // override DefaultModel(); empty = backend default
}

// Constructor builds a Backend from its config block.
type Constructor func(cfg Config) (Backend, error)

var registry = map[string]Constructor{}

// Register adds a backend constructor under a name. Called from each
// backend package's init().
func Register(name string, ctor Constructor) {
	registry[name] = ctor
}

// New returns a Backend for the given name, or an error if not registered.
func New(name string, cfg Config) (Backend, error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown backend %q (registered: %v)", name, Names())
	}
	return ctor(cfg)
}

// Names returns the sorted list of registered backend names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
