// Package judge provides LLM-as-judge evaluation for stage success.
package judge

import (
	"fmt"
	"os"
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
)

// Result represents the outcome of a judge evaluation.
type Result struct {
	Success     bool
	Interactive bool // True if stage is waiting for user input
	Reasoning   string
}

// Judge evaluates whether a stage completed successfully.
type Judge struct {
	Harness  harness.Harness
	Model    string
	MaxTurns int
	Debug    bool
}

// NewJudge creates a new judge with the given harness and model.
func NewJudge(h harness.Harness, model string) *Judge {
	if model == "" {
		model = h.DefaultModel()
	}
	return &Judge{
		Harness:  h,
		Model:    model,
		MaxTurns: 5,
		Debug:    false,
	}
}

// Evaluate runs the judge evaluation for a completed stage.
func (j *Judge) Evaluate(ctx *EvaluationContext) (*Result, error) {
	prompt := j.buildPrompt(ctx)

	res, err := j.Harness.Invoke(harness.InvokeOptions{
		Prompt:   prompt,
		Model:    j.Model,
		WorkDir:  ctx.WorkDir,
		MaxTurns: j.MaxTurns,
		Debug:    j.Debug,
	})
	if err != nil {
		return nil, err
	}

	return j.parseResponse(res.Output)
}

// EvaluationContext contains all the information the judge needs to evaluate a stage.
type EvaluationContext struct {
	Goal           string
	Ticket         string
	StageName      string
	JudgePrompt    string
	CLIOutput      string
	OutputFilePath string
	WorkDir        string
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
	sb.WriteString("\nUse INTERACTIVE when the output shows the agent asking a question, ")
	sb.WriteString("presenting options for the user to choose, or explicitly waiting for input.\n")

	return sb.String()
}

// parseResponse extracts the success/failure and reasoning from the judge's response.
func (j *Judge) parseResponse(response string) (*Result, error) {
	response = strings.TrimSpace(response)

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

	return &Result{
		Success:   false,
		Reasoning: fmt.Sprintf("Could not parse judge response: %s", response),
	}, nil
}
