package state

import "fmt"

// RunLiveness describes whether a state file still has a live fastflow process.
type RunLiveness struct {
	Running   bool
	Stale     bool
	Pid       int
	PIDSource string
	Reason    string
}

// AssessRunLiveness checks whether a run marked as running still has a live
// process. Non-running states are neither running nor stale.
func AssessRunLiveness(runDir string, st *PipelineState) RunLiveness {
	if st == nil || st.Status != StatusRunning {
		return RunLiveness{}
	}

	pid, err := ReadPID(runDir)
	if err != nil {
		return RunLiveness{
			Stale:     true,
			PIDSource: "pid_file",
			Reason:    fmt.Sprintf("pid file is unreadable: %v", err),
		}
	}
	if pid > 0 {
		if IsProcessAlive(pid) {
			return RunLiveness{Running: true, Pid: pid, PIDSource: "pid_file"}
		}
		return RunLiveness{
			Stale:     true,
			Pid:       pid,
			PIDSource: "pid_file",
			Reason:    fmt.Sprintf("pid %d is not running", pid),
		}
	}

	if st.Pid > 0 {
		if IsProcessAlive(st.Pid) {
			return RunLiveness{Running: true, Pid: st.Pid, PIDSource: "state"}
		}
		return RunLiveness{
			Stale:     true,
			Pid:       st.Pid,
			PIDSource: "state",
			Reason:    fmt.Sprintf("state pid %d is not running", st.Pid),
		}
	}

	return RunLiveness{
		Stale:  true,
		Reason: "state is running but no pid is recorded",
	}
}
