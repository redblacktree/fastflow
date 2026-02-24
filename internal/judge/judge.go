// Package judge provides LLM-as-judge evaluation for stage success.
package judge

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNotLoggedIn is returned when Claude CLI reports the user is not authenticated.
var ErrNotLoggedIn = fmt.Errorf("claude is not logged in: please run 'claude' interactively and use /login to authenticate")

// isNotLoggedIn checks if the output indicates an authentication failure.
func isNotLoggedIn(output string) bool {
	return strings.Contains(output, "Not logged in") ||
		strings.Contains(output, "Please run /login")
}

// ErrInvalidAPIKey is returned when the API key is set but rejected by the API.
var ErrInvalidAPIKey = fmt.Errorf("ANTHROPIC_API_KEY is set but was rejected by the API")

// isInvalidAPIKey checks if the output indicates an API key was rejected.
func isInvalidAPIKey(output string) bool {
	return strings.Contains(output, "invalid x-api-key") ||
		strings.Contains(output, "authentication_error") ||
		strings.Contains(output, "Invalid API key")
}

// envWithoutAPIKey returns a copy of the current environment with ANTHROPIC_API_KEY removed.
func envWithoutAPIKey() []string {
	var filtered []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "ANTHROPIC_API_KEY=") {
			filtered = append(filtered, env)
		}
	}
	return filtered
}

// Result represents the outcome of a judge evaluation.
type Result struct {
	Success     bool
	Interactive bool   // True if stage is waiting for user input
	Reasoning   string
}

// Judge evaluates whether a stage completed successfully.
type Judge struct {
	// Model is the Claude model to use (default: haiku).
	Model string
	// MaxTurns is the maximum turns for evaluation (default: 5).
	MaxTurns int
	// Debug enables verbose output logging.
	Debug bool
}

// NewJudge creates a new judge with the given model.
func NewJudge(model string) *Judge {
	if model == "" {
		model = "haiku"
	}
	return &Judge{
		Model:    model,
		MaxTurns: 5,
		Debug:    false,
	}
}

// Evaluate runs the judge evaluation for a completed stage.
// If ANTHROPIC_API_KEY is set but rejected, it retries without the key.
func (j *Judge) Evaluate(ctx *EvaluationContext) (*Result, error) {
	result, outputStr, err := j.evaluate(ctx, nil)
	if err != nil {
		return nil, err
	}

	// If API key was rejected and is set in env, retry without it
	if isInvalidAPIKey(outputStr) && os.Getenv("ANTHROPIC_API_KEY") != "" {
		fmt.Fprintf(os.Stderr, "WARNING: ANTHROPIC_API_KEY is set but was rejected (judge). Falling back to claude.ai session.\n")
		retryResult, _, retryErr := j.evaluate(ctx, envWithoutAPIKey())
		if retryErr != nil {
			return nil, fmt.Errorf("judge fallback also failed: %w (original: ANTHROPIC_API_KEY rejected)", retryErr)
		}
		return retryResult, nil
	}

	return result, nil
}

// evaluate is the internal implementation that runs the judge with an optional env override.
func (j *Judge) evaluate(ctx *EvaluationContext, env []string) (*Result, string, error) {
	prompt := j.buildPrompt(ctx)

	args := []string{
		"--model", j.Model,
		"--print",
		"--max-turns", fmt.Sprintf("%d", j.MaxTurns),
	}
	args = append(args, prompt)

	if j.Debug {
		fmt.Printf("[DEBUG] Judge command: claude %v\n", args[:len(args)-1]) // Don't print full prompt
		fmt.Printf("[DEBUG] Judge prompt length: %d chars\n", len(prompt))
	}

	// Run Claude with the judge prompt
	cmd := exec.Command("claude", args...)
	if env != nil {
		cmd.Env = env
	}

	output, err := cmd.CombinedOutput()

	if j.Debug {
		fmt.Printf("[DEBUG] Judge raw output (%d chars):\n%s\n", len(output), string(output))
		if err != nil {
			fmt.Printf("[DEBUG] Judge error: %v\n", err)
		}
	}

	outputStr := string(output)

	// Detect authentication failure before treating as generic error
	if isNotLoggedIn(outputStr) {
		return nil, outputStr, ErrNotLoggedIn
	}

	if err != nil {
		return nil, outputStr, fmt.Errorf("judge evaluation failed: %w\nOutput: %s", err, outputStr)
	}

	result, parseErr := j.parseResponse(outputStr)
	return result, outputStr, parseErr
}

// EvaluationContext contains all the information the judge needs to evaluate a stage.
type EvaluationContext struct {
	// Goal is the original goal for the pipeline run.
	Goal string
	// Ticket is the ticket identifier.
	Ticket string
	// StageName is the name of the stage being evaluated.
	StageName string
	// JudgePrompt is the custom evaluation criteria (or default).
	JudgePrompt string
	// CLIOutput is the full output from the Claude CLI for this stage.
	CLIOutput string
	// OutputFilePath is the path to the stage's output file.
	OutputFilePath string
}

// buildPrompt constructs the evaluation prompt for the judge.
func (j *Judge) buildPrompt(ctx *EvaluationContext) string {
	var sb strings.Builder

	sb.WriteString("You are evaluating whether a pipeline stage completed successfully.\n\n")

	sb.WriteString("## Context\n\n")
	sb.WriteString(fmt.Sprintf("**Goal**: %s\n", ctx.Goal))
	sb.WriteString(fmt.Sprintf("**Ticket**: %s\n", ctx.Ticket))
	sb.WriteString(fmt.Sprintf("**Stage**: %s\n\n", ctx.StageName))

	sb.WriteString("## Evaluation Criteria\n\n")
	sb.WriteString(ctx.JudgePrompt)
	sb.WriteString("\n\n")

	sb.WriteString("## Stage CLI Output\n\n")
	sb.WriteString("```\n")
	// Truncate output if too long
	cliOutput := ctx.CLIOutput
	if len(cliOutput) > 10000 {
		cliOutput = cliOutput[:5000] + "\n\n... [truncated] ...\n\n" + cliOutput[len(cliOutput)-5000:]
	}
	sb.WriteString(cliOutput)
	sb.WriteString("\n```\n\n")

	// Include output file contents if it exists
	if ctx.OutputFilePath != "" {
		if content, err := os.ReadFile(ctx.OutputFilePath); err == nil {
			sb.WriteString("## Stage Output File\n\n")
			sb.WriteString("```\n")
			fileContent := string(content)
			if len(fileContent) > 10000 {
				fileContent = fileContent[:5000] + "\n\n... [truncated] ...\n\n" + fileContent[len(fileContent)-5000:]
			}
			sb.WriteString(fileContent)
			sb.WriteString("\n```\n\n")
		}
	}

	sb.WriteString("## Your Evaluation\n\n")
	sb.WriteString("Based on the above, did this stage complete successfully?\n\n")
	sb.WriteString("Respond with EXACTLY one of:\n")
	sb.WriteString("- `YES: <brief explanation of why it succeeded>`\n")
	sb.WriteString("- `NO: <brief explanation of what failed or is missing>`\n")
	sb.WriteString("- `INTERACTIVE: <the question or input the stage is waiting for>`\n")
	sb.WriteString("\nUse INTERACTIVE when the output shows Claude asking a question, ")
	sb.WriteString("presenting options for the user to choose, or explicitly waiting for input.\n")

	return sb.String()
}

// parseResponse extracts the success/failure and reasoning from the judge's response.
func (j *Judge) parseResponse(response string) (*Result, error) {
	response = strings.TrimSpace(response)

	// Look for YES or NO at the start of the response (case-insensitive)
	upper := strings.ToUpper(response)

	if strings.HasPrefix(upper, "YES") {
		reasoning := response
		if idx := strings.Index(response, ":"); idx != -1 {
			reasoning = strings.TrimSpace(response[idx+1:])
		}
		return &Result{Success: true, Reasoning: reasoning}, nil
	}

	if strings.HasPrefix(upper, "NO") {
		reasoning := response
		if idx := strings.Index(response, ":"); idx != -1 {
			reasoning = strings.TrimSpace(response[idx+1:])
		}
		return &Result{Success: false, Reasoning: reasoning}, nil
	}

	if strings.HasPrefix(upper, "INTERACTIVE") {
		reasoning := response
		if idx := strings.Index(response, ":"); idx != -1 {
			reasoning = strings.TrimSpace(response[idx+1:])
		}
		return &Result{Interactive: true, Reasoning: reasoning}, nil
	}

	// If we can't parse a clear YES/NO/INTERACTIVE, treat as failure
	return &Result{
		Success:   false,
		Reasoning: fmt.Sprintf("Could not parse judge response: %s", response),
	}, nil
}
