package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadPID(t *testing.T) {
	dir := t.TempDir()
	if err := WritePID(dir); err != nil {
		t.Fatalf("WritePID failed: %v", err)
	}
	pid, err := ReadPID(dir)
	if err != nil {
		t.Fatalf("ReadPID failed: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("PID = %d, want %d", pid, os.Getpid())
	}
}

func TestReadPID_NotFound(t *testing.T) {
	dir := t.TempDir()
	pid, err := ReadPID(dir)
	if err != nil {
		t.Fatalf("ReadPID failed: %v", err)
	}
	if pid != 0 {
		t.Errorf("PID = %d, want 0", pid)
	}
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()
	WritePID(dir) //nolint:errcheck
	RemovePID(dir)
	if _, err := os.Stat(filepath.Join(dir, PidFileName)); !os.IsNotExist(err) {
		t.Error("PID file should be removed")
	}
}

func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !IsProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	// PID 0 should not be alive
	if IsProcessAlive(0) {
		t.Error("PID 0 should not be alive")
	}
	// Very high PID unlikely to exist
	if IsProcessAlive(999999999) {
		t.Error("PID 999999999 should not be alive")
	}
}
