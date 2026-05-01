package runner

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redblacktree/fastflow/internal/state"
)

func TestExecuteOnComplete_Success(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "on-complete-output.txt")

	ctx := &RunContext{
		WorkDir: "/tmp/test-worktree",
		RunDir:  "/tmp/test-rundir",
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
		"FASTFLOW_RUN_DIR=":  "/tmp/test-rundir",
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

func TestExecuteOnComplete_Timeout(t *testing.T) {
	orig := onCompleteTimeout
	onCompleteTimeout = 100 * time.Millisecond
	defer func() { onCompleteTimeout = orig }()

	ctx := &RunContext{WorkDir: "/tmp"}
	pState := &state.PipelineState{
		Ticket:     "TEST-TIMEOUT",
		Status:     state.StatusComplete,
		OnComplete: "sleep 5",
	}

	start := time.Now()
	executeOnComplete(ctx, pState)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("executeOnComplete did not return within timeout window: %s", elapsed)
	}
}

func TestHandleTerminationSignal_FiresOnComplete(t *testing.T) {
	runDir := t.TempDir()
	outFile := filepath.Join(runDir, "hook.out")

	ctx := &RunContext{WorkDir: "/tmp/test-wt", RunDir: runDir}
	pState := state.NewState("full", []string{"plan"}, "TEST-SIG", "/tmp/test-wt")
	pState.OnComplete = "env | grep FASTFLOW > " + outFile
	if err := pState.Save(runDir); err != nil {
		t.Fatalf("save: %v", err)
	}

	handleTerminationSignal(ctx, pState, syscall.SIGTERM)

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("hook output not written: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, "FASTFLOW_STATUS=failed") {
		t.Errorf("expected FASTFLOW_STATUS=failed, got: %s", out)
	}
	if !strings.Contains(out, "FASTFLOW_ERROR=received signal: terminated") {
		t.Errorf("expected FASTFLOW_ERROR with signal name, got: %s", out)
	}

	loaded, _ := state.Load(runDir)
	if loaded == nil || loaded.Status != state.StatusFailed {
		t.Errorf("expected persisted status=failed, got: %+v", loaded)
	}
}
