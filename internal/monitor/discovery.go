// Package monitor provides run discovery and web server for fastflow monitoring.
package monitor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/redblacktree/fastflow/internal/runner"
	"github.com/redblacktree/fastflow/internal/state"
	"github.com/redblacktree/fastflow/internal/worktree"
)

// RunEntry represents a single run for display in both CLI and web UI.
type RunEntry struct {
	Ticket    string `json:"ticket"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Created   string `json:"created"`
	UpdatedAt string `json:"updated_at"`
	Summary   string `json:"summary"`
	Pid       int    `json:"pid,omitempty"`
	LogPath   string `json:"log_path,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
	RunDir    string `json:"run_dir,omitempty"`
}

// DiscoverRuns finds all runs across worktrees and state directories,
// applies stale PID detection, and returns a unified list.
// The prefix parameter filters entries by ticket prefix (empty = no filter).
func DiscoverRuns(cwd, prefix string) ([]RunEntry, error) {
	seen := make(map[string]bool)
	var entries []RunEntry

	// Source 1: Worktree-based runs via worktree.Manager.List()
	mgr, err := worktree.NewManager(cwd)
	if err == nil {
		worktrees, err := mgr.List()
		if err == nil {
			for _, wt := range worktrees {
				status := "active"
				if wt.IsOrphan {
					status = "orphaned"
				}

				created := ""
				summary := "(no goal file)"
				stage := ""
				updatedAt := ""
				workDir := wt.Path

				// Try to read ticket info from goal.md
				for _, loc := range []string{wt.Path, cwd} {
					if info := worktree.ReadTicketInfo(loc, wt.Ticket); info != nil {
						if info.Created != "" {
							created = formatTimestamp(info.Created)
						}
						if info.Goal != "" {
							summary = info.Goal
						}
						break
					}
				}

				// Try to read state.json for richer status
				runDir := runner.GetRunDir(wt.Path, wt.Ticket)
				var entryPid int
				if st, loadErr := state.Load(runDir); loadErr == nil && st != nil && st.Status != "" {
					status = st.Status
					stage = st.Stage
					updatedAt = st.UpdatedAt
					if st.WorkDir != "" {
						workDir = st.WorkDir
					}
					if status == state.StatusRunning {
						liveness := state.AssessRunLiveness(runDir, st)
						if liveness.Stale {
							status = state.StatusStale
						} else if liveness.Running {
							entryPid = liveness.Pid
						}
					}
				}

				var entryLogPath string
				logPath := filepath.Join(runDir, "fastflow.log")
				if _, statErr := os.Stat(logPath); statErr == nil {
					entryLogPath = logPath
				}

				entries = append(entries, RunEntry{
					Ticket:    wt.Ticket,
					Status:    status,
					Stage:     stage,
					Created:   created,
					UpdatedAt: updatedAt,
					Summary:   summary,
					Pid:       entryPid,
					LogPath:   entryLogPath,
					WorkDir:   workDir,
					RunDir:    runDir,
				})
				seen[wt.Ticket] = true
			}
		}
	}

	// Source 2: State-based runs (non-worktree) from current repo
	stateRuns, _ := state.ScanRunDirs(cwd)
	for _, run := range stateRuns {
		if seen[run.Ticket] {
			continue // Already listed via worktree
		}

		status := run.State.Status
		if status == "" {
			status = "unknown"
		}
		var entryPid int
		if status == state.StatusRunning {
			liveness := state.AssessRunLiveness(run.RunDir, run.State)
			if liveness.Stale {
				status = state.StatusStale
			} else if liveness.Running {
				entryPid = liveness.Pid
			}
		}
		stage := run.State.Stage
		updatedAt := run.State.UpdatedAt
		workDir := run.State.WorkDir
		created := ""
		summary := "(no goal file)"

		// Read goal info
		if info := worktree.ReadTicketInfo(cwd, run.Ticket); info != nil {
			if info.Created != "" {
				created = formatTimestamp(info.Created)
			}
			if info.Goal != "" {
				summary = info.Goal
			}
		}

		var entryLogPath string
		logPath := filepath.Join(run.RunDir, "fastflow.log")
		if _, statErr := os.Stat(logPath); statErr == nil {
			entryLogPath = logPath
		}

		entries = append(entries, RunEntry{
			Ticket:    run.Ticket,
			Status:    status,
			Stage:     stage,
			Created:   created,
			UpdatedAt: updatedAt,
			Summary:   summary,
			Pid:       entryPid,
			LogPath:   entryLogPath,
			WorkDir:   workDir,
			RunDir:    run.RunDir,
		})
		seen[run.Ticket] = true
	}

	// Filter by prefix if specified
	if prefix != "" {
		var filtered []RunEntry
		for _, e := range entries {
			if strings.HasPrefix(e.Ticket, prefix) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	return entries, nil
}

// formatTimestamp normalizes a timestamp to "YYYY-MM-DD HH:MM:SS" for display.
func formatTimestamp(ts string) string {
	if len(ts) > 19 {
		ts = ts[:19]
	}
	return strings.Replace(ts, "T", " ", 1)
}
