// Package config provides JSON configuration parsing and types for the fastflow orchestrator.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
)

// Defaults holds global default settings that can be overridden per-stage.
type Defaults struct {
	MaxBudgetUsd float64 `json:"maxBudgetUsd,omitempty"`
}

// Config is the top-level orchestrator configuration.
type Config struct {
	Workflows          map[string]Workflow       `json:"workflows"`
	Stages             map[string]Stage          `json:"stages"`
	DefaultWorkflow    string                    `json:"default_workflow"`
	DefaultJudgePrompt string                    `json:"default_judge_prompt"`
	JudgeModel         string                    `json:"judge_model"`
	JudgeHarness       string                    `json:"judge_harness,omitempty"` // empty = Harness
	Harness            string                    `json:"harness,omitempty"`       // empty = "claude"
	Harnesses          map[string]harness.Config `json:"harnesses,omitempty"`     // per-harness settings
	JudgeBackend       string                    `json:"judge_backend,omitempty"` // deprecated alias for JudgeHarness
	Backend            string                    `json:"backend,omitempty"`       // deprecated alias for Harness
	Backends           map[string]harness.Config `json:"backends,omitempty"`      // deprecated alias for Harnesses
	Defaults           Defaults                  `json:"defaults,omitempty"`
}

// Workflow defines a named workflow as a sequence of stages.
type Workflow struct {
	Description        string   `json:"description"`
	Stages             []string `json:"stages"`
	SkipGoal           bool     `json:"skip_goal,omitempty"`
	IgnoreConfigChange bool     `json:"ignore_config_change,omitempty"`
}

// Stage defines a pipeline stage configuration.
type Stage struct {
	PromptFile       string         `json:"prompt_file,omitempty"`
	Skill            string         `json:"skill,omitempty"`
	Requires         []string       `json:"requires,omitempty"`
	Model            string         `json:"model,omitempty"`
	BackupModels     []ModelAttempt `json:"backup_models,omitempty"`
	EscalationModels []ModelAttempt `json:"escalation_models,omitempty"`
	Harness          string         `json:"harness,omitempty"` // per-stage override; empty = Config.Harness
	Backend          string         `json:"backend,omitempty"` // deprecated alias for Harness
	Checkpoint       bool           `json:"checkpoint,omitempty"`
	JudgePrompt      string         `json:"judge_prompt,omitempty"`
	MaxBudgetUsd     *float64       `json:"maxBudgetUsd,omitempty"`
}

// ModelAttempt describes one retry attempt. Harness is the public name;
// Backend remains as a deprecated compatibility alias.
type ModelAttempt struct {
	Harness    string   `json:"harness,omitempty"`
	Backend    string   `json:"backend,omitempty"`
	Model      string   `json:"model,omitempty"`
	PromptFile string   `json:"prompt_file,omitempty"`
	Skill      string   `json:"skill,omitempty"`
	Requires   []string `json:"requires,omitempty"`
}

// HarnessName returns the explicit harness or a caller-provided default.
func (a ModelAttempt) HarnessName(defaultHarness string) string {
	if strings.TrimSpace(a.Harness) != "" {
		return strings.TrimSpace(a.Harness)
	}
	if strings.TrimSpace(a.Backend) != "" {
		return strings.TrimSpace(a.Backend)
	}
	return defaultHarness
}

// EffectiveBudget returns the maxBudgetUsd for a stage, falling back to the
// global default. Returns 0 if no budget is configured.
func (c *Config) EffectiveBudget(stage *Stage) float64 {
	if stage.MaxBudgetUsd != nil {
		return *stage.MaxBudgetUsd
	}
	return c.Defaults.MaxBudgetUsd
}

// HarnessConfig returns the per-harness config block, or zero value if absent.
func (c *Config) HarnessConfig(name string) harness.Config {
	if c.Harnesses != nil {
		if cfg, ok := c.Harnesses[name]; ok {
			return cfg
		}
	}
	if c.Backends != nil {
		return c.Backends[name]
	}
	return harness.Config{}
}

// BackendConfig is the deprecated alias for HarnessConfig.
func (c *Config) BackendConfig(name string) harness.Config {
	return c.HarnessConfig(name)
}

// HarnessForStage returns the harness name for a stage, falling back to the global harness.
func (c *Config) HarnessForStage(stage *Stage) string {
	if stage.Harness != "" {
		return stage.Harness
	}
	if stage.Backend != "" {
		return stage.Backend
	}
	if c.Harness != "" {
		return c.Harness
	}
	if c.Backend != "" {
		return c.Backend
	}
	return "claude"
}

// BackendForStage is the deprecated alias for HarnessForStage.
func (c *Config) BackendForStage(stage *Stage) string {
	return c.HarnessForStage(stage)
}

// JudgeHarnessName returns the configured judge harness.
func (c *Config) JudgeHarnessName() string {
	if c.JudgeHarness != "" {
		return c.JudgeHarness
	}
	if c.JudgeBackend != "" {
		return c.JudgeBackend
	}
	if c.Harness != "" {
		return c.Harness
	}
	if c.Backend != "" {
		return c.Backend
	}
	return "claude"
}

// Load reads and parses a config file from the given path.
// If path is empty, it looks for orchestrator.json in the current directory.
func Load(path string) (*Config, error) {
	if path == "" {
		path = "orchestrator.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	// Set defaults
	if cfg.JudgeModel == "" {
		cfg.JudgeModel = "haiku"
	}
	if cfg.DefaultWorkflow == "" {
		cfg.DefaultWorkflow = "full"
	}
	if cfg.DefaultJudgePrompt == "" {
		cfg.DefaultJudgePrompt = "Did this stage complete successfully? Look for clear evidence that the required work was done. Respond with YES or NO followed by a brief explanation."
	}
	if cfg.Harness == "" {
		cfg.Harness = cfg.Backend
	}
	if cfg.Harness == "" {
		cfg.Harness = "claude"
	}
	if cfg.Backend == "" {
		cfg.Backend = cfg.Harness
	}
	if cfg.JudgeHarness == "" {
		cfg.JudgeHarness = cfg.JudgeBackend
	}
	if cfg.JudgeHarness == "" {
		cfg.JudgeHarness = cfg.Harness
	}
	if cfg.JudgeBackend == "" {
		cfg.JudgeBackend = cfg.JudgeHarness
	}

	return &cfg, nil
}

// GetWorkflow returns the workflow with the given name, or an error if not found.
func (c *Config) GetWorkflow(name string) (*Workflow, error) {
	if name == "" {
		name = c.DefaultWorkflow
	}

	wf, ok := c.Workflows[name]
	if !ok {
		return nil, fmt.Errorf("workflow %q not found (available: %v)", name, workflowNames(c.Workflows))
	}
	return &wf, nil
}

// GetStage returns the stage with the given name, or an error if not found.
func (c *Config) GetStage(name string) (*Stage, error) {
	stage, ok := c.Stages[name]
	if !ok {
		return nil, fmt.Errorf("stage %q not found", name)
	}
	return &stage, nil
}

// ResolvePromptFile resolves a prompt file path relative to the config directory.
func ResolvePromptFile(configPath, promptFile string) string {
	if filepath.IsAbs(promptFile) {
		return promptFile
	}
	configDir := filepath.Dir(configPath)
	if configDir == "." {
		return promptFile
	}
	return filepath.Join(configDir, promptFile)
}

func workflowNames(workflows map[string]Workflow) []string {
	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	return names
}

// StageNames returns a sorted list of all stage names in the config.
func (c *Config) StageNames() []string {
	names := make([]string, 0, len(c.Stages))
	for name := range c.Stages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
