package backend_test

import (
	"testing"

	"github.com/redblacktree/fastflow/internal/backend"
	_ "github.com/redblacktree/fastflow/internal/backend/claude"
	_ "github.com/redblacktree/fastflow/internal/backend/codex"
)

func TestRegistryHasClaudeAndCodex(t *testing.T) {
	names := backend.Names()
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["claude"] {
		t.Error("claude backend not registered")
	}
	if !found["codex"] {
		t.Error("codex backend not registered")
	}
}

func TestNew_UnknownBackend(t *testing.T) {
	if _, err := backend.New("nonexistent", backend.Config{}); err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestNew_ClaudeDefaults(t *testing.T) {
	b, err := backend.New("claude", backend.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "claude" {
		t.Errorf("Name() = %q, want claude", b.Name())
	}
	if b.DefaultModel() != "sonnet" {
		t.Errorf("DefaultModel() = %q, want sonnet", b.DefaultModel())
	}
	caps := b.Capabilities()
	if !caps.SupportsBudget || !caps.SupportsResume || !caps.SupportsSlashCommands {
		t.Errorf("claude caps unexpectedly limited: %+v", caps)
	}
}

func TestNew_ClaudeBinaryAndModelOverride(t *testing.T) {
	b, err := backend.New("claude", backend.Config{Binary: "/opt/claude", DefaultModel: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	if b.DefaultModel() != "opus" {
		t.Errorf("DefaultModel() = %q, want opus", b.DefaultModel())
	}
}

func TestNew_CodexDefaults(t *testing.T) {
	b, err := backend.New("codex", backend.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "codex" {
		t.Errorf("Name() = %q, want codex", b.Name())
	}
	caps := b.Capabilities()
	if caps.SupportsBudget {
		t.Error("codex SupportsBudget should be false")
	}
	if caps.SupportsMaxTurns {
		t.Error("codex SupportsMaxTurns should be false")
	}
	if caps.SupportsSlashCommands {
		t.Error("codex SupportsSlashCommands should be false")
	}
	if !caps.SupportsResume {
		t.Error("codex SupportsResume should be true")
	}
	if !caps.SupportsStreaming {
		t.Error("codex SupportsStreaming should be true")
	}
}
