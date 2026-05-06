package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/redblacktree/fastflow/internal/config"

	_ "github.com/redblacktree/fastflow/internal/backend/claude"
	_ "github.com/redblacktree/fastflow/internal/backend/codex"
)

func TestLoad_BackendDefaultsToClaude(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.Backend != "claude" {
		t.Errorf("Backend = %q, want claude", cfg.Backend)
	}
	if cfg.JudgeBackend != "claude" {
		t.Errorf("JudgeBackend = %q, want claude", cfg.JudgeBackend)
	}
}

func TestLoad_ExplicitBackends(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"backend":          "codex",
		"judge_backend":    "claude",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.Backend != "codex" {
		t.Errorf("Backend = %q, want codex", cfg.Backend)
	}
	if cfg.JudgeBackend != "claude" {
		t.Errorf("JudgeBackend = %q, want claude", cfg.JudgeBackend)
	}
}

func TestLoad_JudgeBackendDefaultsToBackend(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"backend":          "codex",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.JudgeBackend != "codex" {
		t.Errorf("JudgeBackend = %q, want codex (inherited from backend)", cfg.JudgeBackend)
	}
}

func TestLoad_PerStageBackendOverride(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"backend":          "claude",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1", "s2"}}},
		"stages": map[string]any{
			"s1": map[string]any{"skill": "test"},
			"s2": map[string]any{"skill": "test", "backend": "codex"},
		},
	})
	s1, err := cfg.GetStage("s1")
	if err != nil {
		t.Fatalf("GetStage(s1): %v", err)
	}
	s2, err := cfg.GetStage("s2")
	if err != nil {
		t.Fatalf("GetStage(s2): %v", err)
	}
	if s1.Backend != "" {
		t.Errorf("s1.Backend = %q, want empty", s1.Backend)
	}
	if s2.Backend != "codex" {
		t.Errorf("s2.Backend = %q, want codex", s2.Backend)
	}
}

func TestValidate_RejectsUnknownBackend(t *testing.T) {
	cfg := &config.Config{
		Backend:         "nonexistent",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]config.Stage{"s1": {Skill: "test"}},
	}
	res := config.Validate(cfg)
	if res.IsValid() {
		t.Error("expected validation error for unknown backend")
	}
}

func TestValidate_AcceptsAnyModelString(t *testing.T) {
	// Old behavior rejected anything outside {opus, sonnet, haiku}. New
	// behavior delegates to the backend; "gpt-4o" should now validate.
	cfg := &config.Config{
		Backend:         "codex",
		JudgeBackend:    "codex",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]config.Stage{"s1": {Skill: "test", Model: "gpt-4o"}},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected valid, got: %s", res.Error())
	}
}

func TestBackendForStage_FallsBackToGlobal(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"backend":          "codex",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	stage, _ := cfg.GetStage("s1")
	if got := cfg.BackendForStage(stage); got != "codex" {
		t.Errorf("BackendForStage = %q, want codex", got)
	}
}

func TestValidate_WarnsCodexGlobalWithClaudePaths(t *testing.T) {
	cfg := &config.Config{
		Backend:         "codex",
		JudgeBackend:    "codex",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {PromptFile: ".claude/stages/implement.md"},
		},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected no errors, got: %s", res.Error())
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for codex global backend with .claude/ stage prompt, got none")
	}
}

func TestValidate_WarnsCodexPerStageOverrideWithClaudePrompt(t *testing.T) {
	cfg := &config.Config{
		Backend:         "claude",
		JudgeBackend:    "claude",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {PromptFile: ".claude/stages/implement.md", Backend: "codex"},
		},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected no errors, got: %s", res.Error())
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for codex per-stage override with .claude/ prompt, got none")
	}
}

func TestValidate_WarnsCodexPerStageOverrideWithClaudeRequire(t *testing.T) {
	cfg := &config.Config{
		Backend:         "claude",
		JudgeBackend:    "claude",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {
				Skill:    "test",
				Backend:  "codex",
				Requires: []string{".claude/commands/ff_implement_plan.md"},
			},
		},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected no errors, got: %s", res.Error())
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for codex per-stage override with .claude/ require, got none")
	}
}

func writeAndLoad(t *testing.T, m map[string]any) *config.Config {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	path := filepath.Join(dir, "orchestrator.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}
