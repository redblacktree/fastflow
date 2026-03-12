package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const PidFileName = "pid"

// WritePID writes the current process ID to the run directory.
func WritePID(runDir string) error {
	path := filepath.Join(runDir, PidFileName)
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ReadPID reads the PID from the run directory. Returns 0 if not found.
func ReadPID(runDir string) (int, error) {
	path := filepath.Join(runDir, PidFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// RemovePID removes the PID file from the run directory.
func RemovePID(runDir string) {
	os.Remove(filepath.Join(runDir, PidFileName)) //nolint:errcheck
}

// ActiveRunError is returned when a run is already active for a ticket.
type ActiveRunError struct {
	Pid int
}

func (e *ActiveRunError) Error() string {
	return fmt.Sprintf("run already active (pid %d)", e.Pid)
}

// CheckResult contains the result of an active run check.
type CheckResult struct {
	// StalePID is non-zero if a stale PID file was found and cleaned up.
	StalePID int
}

// CheckActiveRun checks if another process is already running in the given run
// directory. Returns an *ActiveRunError if an active run is found. If a stale
// PID file is found (process dead), it cleans up the PID file and returns the
// stale PID in the result. Returns nil error if safe to proceed.
func CheckActiveRun(runDir string) (*CheckResult, error) {
	result := &CheckResult{}
	pid, err := ReadPID(runDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read PID file: %w", err)
	}
	if pid == 0 {
		// No PID file — safe to proceed regardless of status
		return result, nil
	}

	if IsProcessAlive(pid) {
		return nil, &ActiveRunError{Pid: pid}
	}

	// Process is dead — stale PID file, clean it up
	RemovePID(runDir)
	result.StalePID = pid
	return result, nil
}

// IsProcessAlive checks if a process with the given PID is still running.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check liveness.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
