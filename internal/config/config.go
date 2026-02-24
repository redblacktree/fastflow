// Package config provides JSON configuration parsing and types for the fastflow orchestrator.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Config is the top-level orchestrator configuration.
type Config struct {
	Workflows          map[string]Workflow `json:"workflows"`
	Stages             map[string]Stage    `json:"stages"`
	DefaultWorkflow    string              `json:"default_workflow"`
	DefaultJudgePrompt string              `json:"default_judge_prompt"`
	JudgeModel         string              `json:"judge_model"`
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
	PromptFile  string   `json:"prompt_file,omitempty"`
	Skill       string   `json:"skill,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	Model       string   `json:"model,omitempty"`
	Checkpoint  bool     `json:"checkpoint,omitempty"`
	JudgePrompt string   `json:"judge_prompt,omitempty"`
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
