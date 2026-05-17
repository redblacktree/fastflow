package monitor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/redblacktree/fastflow/internal/runner"
	"github.com/redblacktree/fastflow/internal/state"
	"github.com/redblacktree/fastflow/internal/worktree"
)

// StaleRunRepair is the persisted result of detecting a dead process behind a
// state.json file that still claimed to be running.
type StaleRunRepair struct {
	Ticket         string `json:"ticket"`
	Workflow       string `json:"workflow,omitempty"`
	Stage          string `json:"stage,omitempty"`
	WorkDir        string `json:"work_dir,omitempty"`
	RunDir         string `json:"run_dir"`
	StatusBefore   string `json:"status_before"`
	StatusAfter    string `json:"status_after"`
	StateUpdatedAt string `json:"state_updated_at,omitempty"`
	Pid            int    `json:"pid,omitempty"`
	PIDSource      string `json:"pid_source,omitempty"`
	Reason         string `json:"reason"`
	RepairedAt     string `json:"repaired_at"`
	DryRun         bool   `json:"dry_run,omitempty"`
	Changed        bool   `json:"changed"`
}

type runCandidate struct {
	ticket  string
	workDir string
	runDir  string
}

// RepairStaleRuns marks running runs with no live process as stale. It scans
// both worktree-backed runs and no-worktree runs under the current repo.
func RepairStaleRuns(cwd, prefix string, dryRun bool) ([]StaleRunRepair, error) {
	candidates := collectRunCandidates(cwd, prefix)
	repairs := []StaleRunRepair{}
	for _, candidate := range candidates {
		st, err := state.Load(candidate.runDir)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", candidate.runDir, err)
		}
		if st == nil {
			continue
		}
		liveness := state.AssessRunLiveness(candidate.runDir, st)
		if !liveness.Stale {
			continue
		}

		now := time.Now().UTC()
		ticket := candidate.ticket
		if ticket == "" {
			ticket = st.Ticket
		}
		reason := "process exited without finalizing state: " + liveness.Reason
		repair := StaleRunRepair{
			Ticket:         ticket,
			Workflow:       st.Workflow,
			Stage:          st.Stage,
			WorkDir:        firstNonEmpty(st.WorkDir, candidate.workDir),
			RunDir:         candidate.runDir,
			StatusBefore:   st.Status,
			StatusAfter:    state.StatusStale,
			StateUpdatedAt: st.UpdatedAt,
			Pid:            liveness.Pid,
			PIDSource:      liveness.PIDSource,
			Reason:         reason,
			RepairedAt:     now.Format(time.RFC3339),
			DryRun:         dryRun,
		}
		if !dryRun {
			if err := st.SetFinalStatus(candidate.runDir, state.StatusStale, 1, reason); err != nil {
				return nil, fmt.Errorf("mark %s stale: %w", candidate.runDir, err)
			}
			state.RemovePID(candidate.runDir)
			repair.Changed = true
		}
		repairs = append(repairs, repair)
	}
	return repairs, nil
}

func collectRunCandidates(cwd, prefix string) []runCandidate {
	seen := map[string]bool{}
	candidates := []runCandidate{}

	add := func(candidate runCandidate) {
		if candidate.runDir == "" || seen[candidate.runDir] {
			return
		}
		if prefix != "" && !strings.HasPrefix(candidate.ticket, prefix) {
			return
		}
		seen[candidate.runDir] = true
		candidates = append(candidates, candidate)
	}

	if mgr, err := worktree.NewManager(cwd); err == nil {
		if worktrees, err := mgr.List(); err == nil {
			for _, wt := range worktrees {
				add(runCandidate{
					ticket:  wt.Ticket,
					workDir: wt.Path,
					runDir:  runner.GetRunDir(wt.Path, wt.Ticket),
				})
			}
		}
	}

	if runs, err := state.ScanRunDirs(cwd); err == nil {
		for _, run := range runs {
			workDir := cwd
			if run.State != nil && run.State.WorkDir != "" {
				workDir = run.State.WorkDir
			}
			add(runCandidate{ticket: run.Ticket, workDir: workDir, runDir: run.RunDir})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ticket == candidates[j].ticket {
			return filepath.Clean(candidates[i].runDir) < filepath.Clean(candidates[j].runDir)
		}
		return candidates[i].ticket < candidates[j].ticket
	})
	return candidates
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
