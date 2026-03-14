package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/state"
)

func TestExecuteOnComplete_Success(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "on-complete-output.txt")

	ctx := &RunContext{
		WorkDir: "/tmp/test-worktree",
	}
	pState := &state.PipelineState{
		Ticket:     "TEST-001",
		Status:     state.StatusComplete,
		Workflow:   "full",
		OnComplete: "env | grep FASTFLOW > " + outFile,
	}

	executeOnComplete(ctx, pState)

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	out := string(data)

	checks := map[string]string{
		"FASTFLOW_TICKET=":   "TEST-001",
		"FASTFLOW_STATUS=":   "complete",
		"FASTFLOW_WORKTREE=": "/tmp/test-worktree",
		"FASTFLOW_WORKFLOW=": "full",
	}
	for prefix, want := range checks {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, prefix) {
				got := strings.TrimPrefix(line, prefix)
				if got != want {
					t.Errorf("%s: got %q, want %q", prefix, got, want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing env var %s%s", prefix, want)
		}
	}
}

func TestExecuteOnComplete_Failure(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "on-complete-output.txt")

	ctx := &RunContext{
		WorkDir: "/tmp/test-worktree",
	}
	pState := &state.PipelineState{
		Ticket:     "TEST-002",
		Status:     state.StatusFailed,
		Error:      "stage evaluation failed",
		Workflow:   "quick",
		OnComplete: "env | grep FASTFLOW > " + outFile,
	}

	executeOnComplete(ctx, pState)

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, "FASTFLOW_STATUS=failed") {
		t.Error("expected FASTFLOW_STATUS=failed")
	}
	if !strings.Contains(out, "FASTFLOW_ERROR=stage evaluation failed") {
		t.Error("expected FASTFLOW_ERROR=stage evaluation failed")
	}
}

func TestExecuteOnComplete_EmptyCommand(t *testing.T) {
	// Should be a no-op, not panic or error
	ctx := &RunContext{WorkDir: "/tmp"}
	pState := &state.PipelineState{OnComplete: ""}
	executeOnComplete(ctx, pState) // must not panic
}

func TestExecuteOnComplete_CommandFailure(t *testing.T) {
	// Command that fails — should not panic, just log
	ctx := &RunContext{WorkDir: "/tmp"}
	pState := &state.PipelineState{
		Ticket:     "TEST-003",
		Status:     state.StatusComplete,
		OnComplete: "exit 1",
	}
	// Should not panic or propagate the error
	executeOnComplete(ctx, pState)
}
