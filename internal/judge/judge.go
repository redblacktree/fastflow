// Package judge provides LLM-as-judge evaluation for stage success.
package judge

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Result represents the outcome of a judge evaluation.
type Result struct {
	Success   bool
	Reasoning string
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
func (j *Judge) Evaluate(ctx *EvaluationContext) (*Result, error) {
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

	output, err := cmd.CombinedOutput()

	if j.Debug {
		fmt.Printf("[DEBUG] Judge raw output (%d chars):\n%s\n", len(output), string(output))
		if err != nil {
			fmt.Printf("[DEBUG] Judge error: %v\n", err)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("judge evaluation failed: %w\nOutput: %s", err, string(output))
	}

	return j.parseResponse(string(output))
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

	// If we can't parse a clear YES/NO, treat as failure
	return &Result{
		Success:   false,
		Reasoning: fmt.Sprintf("Could not parse judge response: %s", response),
	}, nil
}
