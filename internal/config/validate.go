package config

import (
	"fmt"
	"os"
	"strings"
)

// ValidationError represents a configuration validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationResult contains all validation errors found.
type ValidationResult struct {
	Errors []ValidationError
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

// Validate checks the configuration for errors.
// It validates that:
// - A default workflow exists
// - All workflows reference valid stages
// - All stages have either a prompt_file or skill defined
// - All required dependencies (files) exist
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

	// Check judge model is valid
	validModels := map[string]bool{"opus": true, "sonnet": true, "haiku": true}
	if cfg.JudgeModel != "" && !validModels[cfg.JudgeModel] {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "judge_model",
			Message: fmt.Sprintf("invalid model %q (must be opus, sonnet, or haiku)", cfg.JudgeModel),
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

		// Check model is valid
		if stage.Model != "" && !validModels[stage.Model] {
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("stages.%s.model", name),
				Message: fmt.Sprintf("invalid model %q (must be opus, sonnet, or haiku)", stage.Model),
			})
		}

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
