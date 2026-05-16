package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
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

// resolveHarnessName returns the name treating empty as the default "claude".
func resolveHarnessName(name string) string {
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

	// Check that the global harness is registered.
	globalHarness := resolveHarnessName(firstNonEmpty(cfg.Harness, cfg.Backend))
	if _, err := harness.New(globalHarness, cfg.HarnessConfig(globalHarness)); err != nil {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "harness",
			Message: err.Error(),
		})
	}

	// Check that the judge harness is registered.
	judgeHarness := resolveHarnessName(firstNonEmpty(cfg.JudgeHarness, cfg.JudgeBackend, globalHarness))
	if _, err := harness.New(judgeHarness, cfg.HarnessConfig(judgeHarness)); err != nil {
		result.Errors = append(result.Errors, ValidationError{
			Field:   "judge_harness",
			Message: err.Error(),
		})
	}

	validateHarnessConfigs(result, cfg)

	// Warn when a stage's effective harness is non-Claude but the stage still
	// references Claude slash-command paths (.claude/). Those stages will fail at
	// runtime because Codex (and other non-Claude harnesses) cannot execute
	// slash-command skills. Users should either keep those stages on Claude or
	// author harness-specific prompt files that do not rely on /slash-commands.
	for stageName, stage := range cfg.Stages {
		effectiveHarness := resolveHarnessName(cfg.HarnessForStage(&stage))
		if effectiveHarness == "claude" || !stageUsesClaudePaths(stage) {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: fmt.Sprintf("stages.%s", stageName),
			Message: fmt.Sprintf(
				"stage uses .claude/ paths but will run under harness %q, which cannot execute Claude slash-command skills; the stage will fail at runtime",
				effectiveHarness,
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

		// Per-stage harness override must also be registered.
		stageHarness := firstNonEmpty(stage.Harness, stage.Backend)
		if stageHarness != "" {
			validateHarness(result, cfg, fmt.Sprintf("stages.%s.harness", name), stageHarness)
		}

		// Note: model-name validation is intentionally NOT enforced here.
		// Model names are delegated to the harness at invocation time so that
		// new model releases don't require fastflow config changes.
		validateAttempts(result, cfg, name, "backup", stage.BackupAttempts())
		validateAttempts(result, cfg, name, "escalation", stage.EscalationAttempts())

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

func validateHarnessConfigs(result *ValidationResult, cfg *Config) {
	seen := map[string]harness.Config{}
	for name, hcfg := range cfg.Backends {
		seen[name] = hcfg
	}
	for name, hcfg := range cfg.Harnesses {
		seen[name] = hcfg
	}

	for name, hcfg := range seen {
		prefix := fmt.Sprintf("harnesses.%s.codex", name)
		if hcfg.Codex.ModelContextWindow < 0 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix + ".model_context_window",
				Message: "model_context_window must be non-negative",
			})
		}
		if hcfg.Codex.ModelAutoCompactTokenLimit < 0 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix + ".model_auto_compact_token_limit",
				Message: "model_auto_compact_token_limit must be non-negative",
			})
		}
		if hcfg.Codex.ToolOutputTokenLimit < 0 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix + ".tool_output_token_limit",
				Message: "tool_output_token_limit must be non-negative",
			})
		}
		if hcfg.Codex.ContextHandoffThresholdPct < 0 || hcfg.Codex.ContextHandoffThresholdPct > 100 {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix + ".context_handoff_threshold_percent",
				Message: "context_handoff_threshold_percent must be between 0 and 100",
			})
		}
	}
}

func validateAttempts(result *ValidationResult, cfg *Config, stageName, field string, attempts []ModelAttempt) {
	for i, attempt := range attempts {
		prefix := fmt.Sprintf("stages.%s.%s[%d]", stageName, field, i)
		if strings.TrimSpace(attempt.Model) == "" {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix + ".model",
				Message: "model name must not be empty",
			})
		}
		if attempt.Harness != "" || attempt.Backend != "" {
			validateHarness(result, cfg, prefix+".harness", attempt.HarnessName(""))
		}
		if attempt.PromptFile == "" && attempt.Skill == "" {
			continue
		}
		if attempt.PromptFile != "" && attempt.Skill != "" {
			result.Errors = append(result.Errors, ValidationError{
				Field:   prefix,
				Message: "attempt must not set both prompt_file and skill",
			})
		}
	}
}

func validateHarness(result *ValidationResult, cfg *Config, field, name string) {
	if _, err := harness.New(name, cfg.HarnessConfig(name)); err != nil {
		result.Errors = append(result.Errors, ValidationError{
			Field:   field,
			Message: err.Error(),
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

		validateAttemptDependencies(result, name, "backup", stage.BackupAttempts())
		validateAttemptDependencies(result, name, "escalation", stage.EscalationAttempts())
	}

	return result
}

func validateAttemptDependencies(result *ValidationResult, stageName, field string, attempts []ModelAttempt) {
	for i, attempt := range attempts {
		prefix := fmt.Sprintf("stages.%s.%s[%d]", stageName, field, i)
		if attempt.PromptFile != "" {
			if _, err := os.Stat(attempt.PromptFile); os.IsNotExist(err) {
				result.Errors = append(result.Errors, ValidationError{
					Field:   prefix + ".prompt_file",
					Message: fmt.Sprintf("file not found: %s", attempt.PromptFile),
				})
			}
		}
		for j, req := range attempt.Requires {
			if _, err := os.Stat(req); os.IsNotExist(err) {
				result.Errors = append(result.Errors, ValidationError{
					Field:   fmt.Sprintf("%s.requires[%d]", prefix, j),
					Message: fmt.Sprintf("required file not found: %s", req),
				})
			}
		}
	}
}
