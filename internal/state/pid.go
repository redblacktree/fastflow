package state

import (
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
