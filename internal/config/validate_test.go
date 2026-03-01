package config

import "testing"

func TestValidate_NegativeStageBudget(t *testing.T) {
	neg := -1.0
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test", MaxBudgetUsd: &neg}},
		DefaultWorkflow: "full",
	}
	result := Validate(cfg)
	if result.IsValid() {
		t.Error("expected validation error for negative stage maxBudgetUsd")
	}
}

func TestValidate_NegativeDefaultBudget(t *testing.T) {
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test"}},
		DefaultWorkflow: "full",
		Defaults:        Defaults{MaxBudgetUsd: -1.0},
	}
	result := Validate(cfg)
	if result.IsValid() {
		t.Error("expected validation error for negative defaults.maxBudgetUsd")
	}
}

func TestValidate_ValidBudget(t *testing.T) {
	budget := 5.0
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test", MaxBudgetUsd: &budget}},
		DefaultWorkflow: "full",
		Defaults:        Defaults{MaxBudgetUsd: 2.0},
	}
	result := Validate(cfg)
	if !result.IsValid() {
		t.Errorf("expected valid config, got errors: %s", result.Error())
	}
}

func TestValidate_ZeroBudgetIsValid(t *testing.T) {
	zero := 0.0
	cfg := &Config{
		Workflows:       map[string]Workflow{"full": {Stages: []string{"s1"}}},
		Stages:          map[string]Stage{"s1": {Skill: "test", MaxBudgetUsd: &zero}},
		DefaultWorkflow: "full",
		Defaults:        Defaults{MaxBudgetUsd: 0},
	}
	result := Validate(cfg)
	if !result.IsValid() {
		t.Errorf("expected zero budget to be valid, got errors: %s", result.Error())
	}
}
