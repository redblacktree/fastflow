package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/redblacktree/fastflow/internal/harness"
)

func TestEffectiveBudget_StageOverride(t *testing.T) {
	budget := 5.0
	cfg := &Config{
		Defaults: Defaults{MaxBudgetUsd: 2.0},
	}
	stage := &Stage{MaxBudgetUsd: &budget}
	if got := cfg.EffectiveBudget(stage); got != 5.0 {
		t.Errorf("EffectiveBudget() = %v, want 5.0", got)
	}
}

func TestEffectiveBudget_FallbackToDefault(t *testing.T) {
	cfg := &Config{
		Defaults: Defaults{MaxBudgetUsd: 2.0},
	}
	stage := &Stage{}
	if got := cfg.EffectiveBudget(stage); got != 2.0 {
		t.Errorf("EffectiveBudget() = %v, want 2.0", got)
	}
}

func TestEffectiveBudget_NoBudget(t *testing.T) {
	cfg := &Config{}
	stage := &Stage{}
	if got := cfg.EffectiveBudget(stage); got != 0 {
		t.Errorf("EffectiveBudget() = %v, want 0", got)
	}
}

func TestEffectiveBudget_ZeroStageOverride(t *testing.T) {
	// A stage with MaxBudgetUsd explicitly set to 0 overrides the default.
	zero := 0.0
	cfg := &Config{
		Defaults: Defaults{MaxBudgetUsd: 2.0},
	}
	stage := &Stage{MaxBudgetUsd: &zero}
	if got := cfg.EffectiveBudget(stage); got != 0.0 {
		t.Errorf("EffectiveBudget() = %v, want 0.0 (stage override wins)", got)
	}
}

func TestLoadConfigWithBudget(t *testing.T) {
	dir := t.TempDir()
	budget := 7.5
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test", MaxBudgetUsd: &budget}},
		DefaultWorkflow: "full",
		Defaults:        Defaults{MaxBudgetUsd: 3.0},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	path := filepath.Join(dir, "orchestrator.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Defaults.MaxBudgetUsd != 3.0 {
		t.Errorf("Defaults.MaxBudgetUsd = %v, want 3.0", loaded.Defaults.MaxBudgetUsd)
	}

	s, ok := loaded.Stages["s1"]
	if !ok {
		t.Fatal("stage s1 not found")
	}
	if s.MaxBudgetUsd == nil {
		t.Fatal("stage s1 MaxBudgetUsd is nil, want 7.5")
	}
	if *s.MaxBudgetUsd != 7.5 {
		t.Errorf("stage s1 MaxBudgetUsd = %v, want 7.5", *s.MaxBudgetUsd)
	}
}

func TestLoadHarnessConfigWithNestedCodexSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test"}},
		DefaultWorkflow: "full",
		Harnesses: map[string]harness.Config{
			"codex": {
				Binary:       "codex",
				DefaultModel: "gpt-5.3-codex-spark",
				Codex: harness.CodexConfig{
					ModelContextWindow:         128000,
					ContextHandoffThresholdPct: 50,
					BypassApprovalsAndSandbox:  true,
				},
			},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	path := filepath.Join(dir, "orchestrator.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	codex := loaded.HarnessConfig("codex").Codex
	if codex.ModelContextWindow != 128000 {
		t.Errorf("ModelContextWindow = %v, want 128000", codex.ModelContextWindow)
	}
	if !codex.BypassApprovalsAndSandbox {
		t.Error("BypassApprovalsAndSandbox = false, want true")
	}
	if codex.ContextHandoffThresholdPct != 50 {
		t.Errorf("ContextHandoffThresholdPct = %v, want 50", codex.ContextHandoffThresholdPct)
	}
}
