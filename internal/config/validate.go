package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/redblacktree/fastflow/internal/backend"
)

// ValidationError represents a configuration validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationResult contains all validation errors and warnings found.
type ValidationResult struct {
	Errors   []ValidationError
	Warnings []ValidationError
}

// IsValid returns true if no validation errors were found.
func (r *ValidationResult) IsValid() bool {
	return len(r.Errors) == 0
}

// Error returns a combined error message for all validation errors.
func (r *ValidationResult) Error() string {
	if r.IsValid() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("configuration validation failed:\n")
	for _, err := range r.Errors {
		sb.WriteString(fmt.Sprintf("  - %s\n", err.Error()))
	}
	return sb.String()
}

// resolveBackendName returns the name treating empty as the default "claude".
func resolveBackendName(name string) string {
	if name == "" {
		return "claude"
	}
	return name
}

// Validate checks the configuration for errors.
func Validate(cfg *Config) *ValidationResult {
	result := &ValidationResult{}

	// Check default workflow exists
	if cfg.DefaultWorkflow != "" {
		if _, ok := cfg.Workflows[cfg.DefaultWorkflow]; !ok {
			result.Errors = append(result.Errors, ValidationError{
				Field:   "default_workflow",
				Message: fmt.Sprintf("workflow %q not found", cfg.DefaultWorkflow),
			})
		}
	}

	// Check that the global backend is registered.
	globalBackend := resolveBackendName(cfg.Backend)
	if _, err := backend.New(globalBackend, cfg.BackendConfig(globalBackend)); err != nil {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "backend",
			Message: err.Error(),
		})
	}

	// Check that the judge backend is registered.
	judgeBackend := resolveBackendName(cfg.JudgeBackend)
	if _, err := backend.New(judgeBackend, cfg.BackendConfig(judgeBackend)); err != nil {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "judge_backend",
			Message: err.Error(),
		})
	}

	// Warn when a stage's effective backend is non-Claude but the stage still
	// references Claude slash-command paths (.claude/). Those stages will fail at
	// runtime because Codex (and other non-Claude backends) cannot execute
	// slash-command skills. Users should either keep those stages on Claude or
	// author backend-specific prompt files that do not rely on /slash-commands.
	for stageName, stage := range cfg.Stages {
		effectiveBackend := resolveBackendName(cfg.BackendForStage(&stage))
		if effectiveBackend == "claude" || !stageUsesClaudePaths(stage) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: fmt.Sprintf("stages.%s", stageName),
			Message: fmt.Sprintf(
				"stage uses .claude/ paths but will run under backend %q, which cannot execute Claude slash-command skills; the stage will fail at runtime",
				effectiveBackend,
			),
		})
	}

	// Check workflows
	for name, wf := range cfg.Workflows {
		if len(wf.Stages) == 0 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("workflows.%s", name),
				Message: "workflow has no stages",
			})
			continue
		}

		for i, stageName := range wf.Stages {
			if _, ok := cfg.Stages[stageName]; !ok {
				result.Errors = append(result.Errors, ValidationError{
					Field:   fmt.Sprintf("workflows.%s.stages[%d]", name, i),
					Message: fmt.Sprintf("stage %q not found", stageName),
				})
			}
		}
	}

	// Check stages
	for name, stage := range cfg.Stages {
		// Stage must have either prompt_file or skill
		if stage.PromptFile == "" && stage.Skill == "" {
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("stages.%s", name),
				Message: "stage must have either prompt_file or skill",
			})
		}

		// Per-stage backend override must also be registered.
		if stage.Backend != "" {
			if _, err := backend.New(stage.Backend, cfg.BackendConfig(stage.Backend)); err != nil {
				result.Errors = append(result.Errors, ValidationError{
					Field:   fmt.Sprintf("stages.%s.backend", name),
					Message: err.Error(),
				})
			}
		}

		// Note: model-name validation is intentionally NOT enforced here.
		// Model names are delegated to the backend at invocation time so that
		// new model releases don't require fastflow config changes.

		// Check maxBudgetUsd is non-negative if set
		if stage.MaxBudgetUsd != nil && *stage.MaxBudgetUsd < 0 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("stages.%s.maxBudgetUsd", name),
				Message: "maxBudgetUsd must be non-negative",
			})
		}
	}

	// Check defaults
	if cfg.Defaults.MaxBudgetUsd < 0 {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "defaults.maxBudgetUsd",
			Message: "maxBudgetUsd must be non-negative",
		})
	}

	return result
}

func stageUsesClaudePaths(stage Stage) bool {
	if strings.Contains(stage.PromptFile, ".claude/") {
		return true
	}
	for _, req := range stage.Requires {
		if strings.Contains(req, ".claude/") {
			return true
		}
	}
	return false
}

// ValidateDependencies checks that all required files exist.
// This is separate from Validate() because it requires filesystem access.
func ValidateDependencies(cfg *Config) *ValidationResult {
	result := &ValidationResult{}

	for name, stage := range cfg.Stages {
		// Check prompt file exists (if specified)
		if stage.PromptFile != "" {
			if _, err := os.Stat(stage.PromptFile); os.IsNotExist(err) {
				result.Errors = append(result.Errors, ValidationError{
					Field:   fmt.Sprintf("stages.%s.prompt_file", name),
					Message: fmt.Sprintf("file not found: %s", stage.PromptFile),
				})
			}
		}

		// Check required dependencies exist
		for i, req := range stage.Requires {
			if _, err := os.Stat(req); os.IsNotExist(err) {
				result.Errors = append(result.Errors, ValidationError{
					Field:   fmt.Sprintf("stages.%s.requires[%d]", name, i),
					Message: fmt.Sprintf("required file not found: %s", req),
				})
			}
		}
	}

	return result
}
