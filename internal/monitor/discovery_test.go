package monitor

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/redblacktree/fastflow/internal/state"
)

// makeRunDir creates a run directory under thoughts/shared/runs/<ticket>/ and returns its path.
func makeRunDir(t *testing.T, root, ticket string) string {
	t.Helper()
	runDir := filepath.Join(root, "thoughts", "shared", "runs", ticket)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("makeRunDir: %v", err)
	}
	return runDir
}

// writeState creates a state.json in runDir using the provided PipelineState.
func writeState(t *testing.T, runDir string, st *state.PipelineState) {
	t.Helper()
	if err := st.Save(runDir); err != nil {
		t.Fatalf("writeState: %v", err)
	}
}

// writePID writes a PID file into runDir.
func writePID(t *testing.T, runDir string, pid int) {
	t.Helper()
	path := filepath.Join(runDir, "pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644); err != nil {
		t.Fatalf("writePID: %v", err)
	}
}

// writeGoal writes a minimal goal.md file with the given goal and created timestamp.
func writeGoal(t *testing.T, root, ticket, goal, created string) {
	t.Helper()
	runDir := filepath.Join(root, "thoughts", "shared", "runs", ticket)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("writeGoal mkdir: %v", err)
	}
	content := "---\nticket: " + ticket + "\ngoal: " + goal + "\ncreated: " + created + "\n---\n\n# Goal\n\n" + goal + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "goal.md"), []byte(content), 0644); err != nil {
		t.Fatalf("writeGoal: %v", err)
	}
}

func TestDiscoverRuns_Empty(t *testing.T) {
	root := t.TempDir()
	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestDiscoverRuns_SingleRun(t *testing.T) {
	root := t.TempDir()
	runDir := makeRunDir(t, root, "ENG-001")
	st := state.NewState("plan-first", []string{"research", "plan", "implement"}, "ENG-001", root)
	st.Status = state.StatusRunning
	st.Stage = "plan"
	writeState(t, runDir, st)

	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Ticket != "ENG-001" {
		t.Errorf("ticket: got %q, want %q", e.Ticket, "ENG-001")
	}
	if e.Status != state.StatusRunning {
		t.Errorf("status: got %q, want %q", e.Status, state.StatusRunning)
	}
	if e.Stage != "plan" {
		t.Errorf("stage: got %q, want %q", e.Stage, "plan")
	}
	if e.RunDir != runDir {
		t.Errorf("run_dir: got %q, want %q", e.RunDir, runDir)
	}
}

func TestDiscoverRuns_MultipleRuns(t *testing.T) {
	root := t.TempDir()

	tickets := []struct {
		ticket string
		status string
	}{
		{"ENG-001", state.StatusRunning},
		{"ENG-002", state.StatusComplete},
		{"ENG-003", state.StatusFailed},
	}

	for _, tc := range tickets {
		runDir := makeRunDir(t, root, tc.ticket)
		st := state.NewState("default", []string{"implement"}, tc.ticket, root)
		st.Status = tc.status
		writeState(t, runDir, st)
	}

	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	byTicket := make(map[string]RunEntry)
	for _, e := range entries {
		byTicket[e.Ticket] = e
	}
	for _, tc := range tickets {
		e, ok := byTicket[tc.ticket]
		if !ok {
			t.Errorf("missing entry for %s", tc.ticket)
			continue
		}
		if e.Status != tc.status {
			t.Errorf("%s: status got %q, want %q", tc.ticket, e.Status, tc.status)
		}
	}
}

func TestDiscoverRuns_StaleDetection(t *testing.T) {
	root := t.TempDir()
	runDir := makeRunDir(t, root, "ENG-010")
	st := state.NewState("default", []string{"implement"}, "ENG-010", root)
	st.Status = state.StatusRunning
	writeState(t, runDir, st)

	// Write a PID that is guaranteed to be dead (PID 1 is init on Linux but
	// sending signal 0 to it will fail from an unprivileged process on most
	// systems; using a very high PID that almost certainly doesn't exist is
	// safer and more portable).
	writePID(t, runDir, 99999999)

	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Status != "stale" {
		t.Errorf("status: got %q, want %q", entries[0].Status, "stale")
	}
	if entries[0].Pid != 0 {
		t.Errorf("pid should be 0 for stale run, got %d", entries[0].Pid)
	}
}

func TestDiscoverRuns_PrefixFilter(t *testing.T) {
	root := t.TempDir()

	for _, ticket := range []string{"ENG-001", "ENG-002", "REL-100"} {
		runDir := makeRunDir(t, root, ticket)
		st := state.NewState("default", []string{"implement"}, ticket, root)
		st.Status = state.StatusComplete
		writeState(t, runDir, st)
	}

	entries, err := DiscoverRuns(root, "ENG-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with prefix ENG-, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Ticket == "REL-100" {
			t.Errorf("REL-100 should be filtered out")
		}
	}
}

func TestDiscoverRuns_MissingStateFile(t *testing.T) {
	root := t.TempDir()

	// Create a run directory with NO state.json
	noState := filepath.Join(root, "thoughts", "shared", "runs", "ENG-NOSTATE")
	if err := os.MkdirAll(noState, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a valid one alongside it
	runDir := makeRunDir(t, root, "ENG-VALID")
	st := state.NewState("default", []string{"implement"}, "ENG-VALID", root)
	st.Status = state.StatusComplete
	writeState(t, runDir, st)

	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (skipping dir without state.json), got %d", len(entries))
	}
	if entries[0].Ticket != "ENG-VALID" {
		t.Errorf("ticket: got %q, want ENG-VALID", entries[0].Ticket)
	}
}

func TestDiscoverRuns_GoalParsing(t *testing.T) {
	root := t.TempDir()
	runDir := makeRunDir(t, root, "ENG-007")
	st := state.NewState("default", []string{"implement"}, "ENG-007", root)
	st.Status = state.StatusComplete
	writeState(t, runDir, st)
	writeGoal(t, root, "ENG-007", "Add OAuth login support", "2026-04-09T10:00:00-04:00")

	entries, err := DiscoverRuns(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Summary != "Add OAuth login support" {
		t.Errorf("summary: got %q, want %q", e.Summary, "Add OAuth login support")
	}
	if e.Created == "" {
		t.Errorf("created should be set from goal.md")
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-04-09T10:00:00-04:00", "2026-04-09 10:00:00"},
		{"2026-04-09T10:00:00", "2026-04-09 10:00:00"},
		{"2026-04-09", "2026-04-09"},
		{"", ""},
	}
	for _, tc := range tests {
		got := formatTimestamp(tc.input)
		if got != tc.want {
			t.Errorf("formatTimestamp(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
