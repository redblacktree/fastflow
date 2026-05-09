package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/redblacktree/fastflow/internal/config"

	_ "github.com/redblacktree/fastflow/internal/harness/claude"
	_ "github.com/redblacktree/fastflow/internal/harness/codex"
)

func TestLoad_HarnessDefaultsToClaude(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", cfg.Harness)
	}
	if cfg.JudgeHarness != "claude" {
		t.Errorf("JudgeHarness = %q, want claude", cfg.JudgeHarness)
	}
}

func TestLoad_ExplicitHarnesses(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"harness":          "codex",
		"judge_harness":    "claude",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.Harness != "codex" {
		t.Errorf("Harness = %q, want codex", cfg.Harness)
	}
	if cfg.JudgeHarness != "claude" {
		t.Errorf("JudgeHarness = %q, want claude", cfg.JudgeHarness)
	}
}

func TestLoad_BackendFieldsRemainAliases(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"backend":          "codex",
		"judge_backend":    "claude",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages": map[string]any{
			"s1": map[string]any{"skill": "test", "backend": "claude"},
		},
	})
	if cfg.Harness != "codex" {
		t.Errorf("Harness = %q, want codex from backend alias", cfg.Harness)
	}
	if cfg.JudgeHarness != "claude" {
		t.Errorf("JudgeHarness = %q, want claude from judge_backend alias", cfg.JudgeHarness)
	}
	stage, _ := cfg.GetStage("s1")
	if got := cfg.HarnessForStage(stage); got != "claude" {
		t.Errorf("HarnessForStage = %q, want claude from backend alias", got)
	}
}

func TestLoad_JudgeHarnessDefaultsToHarness(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"harness":          "codex",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	if cfg.JudgeHarness != "codex" {
		t.Errorf("JudgeHarness = %q, want codex (inherited from harness)", cfg.JudgeHarness)
	}
}

func TestLoad_PerStageHarnessOverride(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"harness":          "claude",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1", "s2"}}},
		"stages": map[string]any{
			"s1": map[string]any{"skill": "test"},
			"s2": map[string]any{"skill": "test", "harness": "codex"},
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
	if s1.Harness != "" {
		t.Errorf("s1.Harness = %q, want empty", s1.Harness)
	}
	if s2.Harness != "codex" {
		t.Errorf("s2.Harness = %q, want codex", s2.Harness)
	}
}

func TestValidate_RejectsUnknownHarness(t *testing.T) {
	cfg := &config.Config{
		Harness:         "nonexistent",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]config.Stage{"s1": {Skill: "test"}},
	}
	res := config.Validate(cfg)
	if res.IsValid() {
		t.Error("expected validation error for unknown harness")
	}
}

func TestValidate_AcceptsAnyModelString(t *testing.T) {
	// Old behavior rejected anything outside {opus, sonnet, haiku}. New
	// behavior delegates to the harness; "gpt-4o" should now validate.
	cfg := &config.Config{
		Harness:         "codex",
		JudgeHarness:    "codex",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]config.Stage{"s1": {Skill: "test", Model: "gpt-4o"}},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected valid, got: %s", res.Error())
	}
}

func TestLoad_StageBackupAndEscalationModels(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages": map[string]any{
			"s1": map[string]any{
				"skill":             "test",
				"backup_models":     []map[string]any{{"harness": "claude", "model": "sonnet"}},
				"escalation_models": []map[string]any{{"harness": "claude", "model": "opus"}},
			},
		},
	})
	stage, err := cfg.GetStage("s1")
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}
	if len(stage.BackupModels) != 1 || stage.BackupModels[0].Model != "sonnet" {
		t.Fatalf("BackupModels = %+v, want sonnet", stage.BackupModels)
	}
	if len(stage.EscalationModels) != 1 || stage.EscalationModels[0].Model != "opus" {
		t.Fatalf("EscalationModels = %+v, want opus", stage.EscalationModels)
	}
}

func TestValidate_RejectsEmptyBackupModel(t *testing.T) {
	cfg := &config.Config{
		Harness:         "claude",
		JudgeHarness:    "claude",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {Skill: "test", BackupModels: []config.ModelAttempt{{Harness: "claude", Model: " "}}},
		},
	}
	res := config.Validate(cfg)
	if res.IsValid() {
		t.Fatal("expected validation error for empty backup_models entry")
	}
}

func TestHarnessForStage_FallsBackToGlobal(t *testing.T) {
	cfg := writeAndLoad(t, map[string]any{
		"default_workflow": "full",
		"harness":          "codex",
		"workflows":        map[string]any{"full": map[string]any{"stages": []string{"s1"}}},
		"stages":           map[string]any{"s1": map[string]any{"skill": "test"}},
	})
	stage, _ := cfg.GetStage("s1")
	if got := cfg.HarnessForStage(stage); got != "codex" {
		t.Errorf("HarnessForStage = %q, want codex", got)
	}
}

func TestValidate_WarnsCodexGlobalWithClaudePaths(t *testing.T) {
	cfg := &config.Config{
		Harness:         "codex",
		JudgeHarness:    "codex",
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
		t.Error("expected warning for codex global harness with .claude/ stage prompt, got none")
	}
}

func TestValidate_WarnsCodexPerStageOverrideWithClaudePrompt(t *testing.T) {
	cfg := &config.Config{
		Harness:         "claude",
		JudgeHarness:    "claude",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {PromptFile: ".claude/stages/implement.md", Harness: "codex"},
		},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected no errors, got: %s", res.Error())
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for codex per-stage harness override with .claude/ prompt, got none")
	}
}

func TestValidate_WarnsCodexPerStageOverrideWithClaudeRequire(t *testing.T) {
	cfg := &config.Config{
		Harness:         "claude",
		JudgeHarness:    "claude",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			"s1": {
				Skill:    "test",
				Harness:  "codex",
				Requires: []string{".claude/commands/ff_implement_plan.md"},
			},
		},
	}
	res := config.Validate(cfg)
	if !res.IsValid() {
		t.Errorf("expected no errors, got: %s", res.Error())
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for codex per-stage harness override with .claude/ require, got none")
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
