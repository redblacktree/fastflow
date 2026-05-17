package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/state"
)

func TestRepairStaleRuns_MarksStateStaleAndRemovesPID(t *testing.T) {
	root := t.TempDir()
	runDir := makeRunDir(t, root, "REL-100")
	st := state.NewState("default", []string{"implement"}, "REL-100", root)
	st.Status = state.StatusRunning
	st.Stage = "implement"
	writeState(t, runDir, st)
	writePID(t, runDir, 999999999)

	repairs, err := RepairStaleRuns(root, "REL-", false)
	if err != nil {
		t.Fatalf("RepairStaleRuns failed: %v", err)
	}
	if len(repairs) != 1 {
		t.Fatalf("expected 1 repair, got %d", len(repairs))
	}
	repair := repairs[0]
	if !repair.Changed {
		t.Fatal("expected repair to report Changed=true")
	}
	if repair.StatusBefore != state.StatusRunning || repair.StatusAfter != state.StatusStale {
		t.Fatalf("unexpected status transition: %s -> %s", repair.StatusBefore, repair.StatusAfter)
	}
	if repair.Pid != 999999999 {
		t.Fatalf("pid = %d, want 999999999", repair.Pid)
	}
	if !strings.Contains(repair.Reason, "pid 999999999 is not running") {
		t.Fatalf("reason %q does not explain dead pid", repair.Reason)
	}

	loaded, err := state.Load(runDir)
	if err != nil {
		t.Fatalf("load repaired state: %v", err)
	}
	if loaded.Status != state.StatusStale {
		t.Fatalf("state status = %q, want %q", loaded.Status, state.StatusStale)
	}
	if loaded.ExitCode != 1 {
		t.Fatalf("exit_code = %d, want 1", loaded.ExitCode)
	}
	if loaded.Error == "" {
		t.Fatal("expected repaired state to record error")
	}
	if _, err := os.Stat(filepath.Join(runDir, state.PidFileName)); !os.IsNotExist(err) {
		t.Fatal("expected stale pid file to be removed")
	}
}

func TestRepairStaleRuns_DryRunDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	runDir := makeRunDir(t, root, "REL-101")
	st := state.NewState("default", []string{"validate"}, "REL-101", root)
	st.Status = state.StatusRunning
	st.Stage = "validate"
	writeState(t, runDir, st)
	writePID(t, runDir, 999999999)

	repairs, err := RepairStaleRuns(root, "REL-", true)
	if err != nil {
		t.Fatalf("RepairStaleRuns failed: %v", err)
	}
	if len(repairs) != 1 {
		t.Fatalf("expected 1 repair, got %d", len(repairs))
	}
	if !repairs[0].DryRun || repairs[0].Changed {
		t.Fatalf("expected dry-run repair without mutation, got dry_run=%v changed=%v", repairs[0].DryRun, repairs[0].Changed)
	}

	loaded, err := state.Load(runDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.Status != state.StatusRunning {
		t.Fatalf("state status = %q, want %q", loaded.Status, state.StatusRunning)
	}
	if _, err := os.Stat(filepath.Join(runDir, state.PidFileName)); err != nil {
		t.Fatalf("expected pid file to remain, got %v", err)
	}
}

func TestRepairStaleRuns_SkipsLiveAndPrefixMismatch(t *testing.T) {
	root := t.TempDir()

	liveDir := makeRunDir(t, root, "REL-LIVE")
	live := state.NewState("default", []string{"implement"}, "REL-LIVE", root)
	live.Status = state.StatusRunning
	writeState(t, liveDir, live)
	writePID(t, liveDir, os.Getpid())

	otherDir := makeRunDir(t, root, "ENG-STALE")
	other := state.NewState("default", []string{"implement"}, "ENG-STALE", root)
	other.Status = state.StatusRunning
	writeState(t, otherDir, other)
	writePID(t, otherDir, 999999999)

	repairs, err := RepairStaleRuns(root, "REL-", false)
	if err != nil {
		t.Fatalf("RepairStaleRuns failed: %v", err)
	}
	if len(repairs) != 0 {
		t.Fatalf("expected no repairs, got %d", len(repairs))
	}
}
