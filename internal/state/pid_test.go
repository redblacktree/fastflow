package state

import (
	"errors"
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

func TestCheckActiveRun_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	result, err := CheckActiveRun(dir)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.StalePID != 0 {
		t.Fatalf("expected no stale PID, got %d", result.StalePID)
	}
}

func TestCheckActiveRun_ActiveProcess(t *testing.T) {
	dir := t.TempDir()
	if err := WritePID(dir); err != nil {
		t.Fatal(err)
	}
	_, err := CheckActiveRun(dir)
	var activeErr *ActiveRunError
	if !errors.As(err, &activeErr) {
		t.Fatalf("expected ActiveRunError, got %v", err)
	}
	if activeErr.Pid != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), activeErr.Pid)
	}
}

func TestCheckActiveRun_StaleProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PidFileName)
	os.WriteFile(path, []byte("999999999"), 0644) //nolint:errcheck

	result, err := CheckActiveRun(dir)
	if err != nil {
		t.Fatalf("expected nil error for stale PID, got %v", err)
	}
	if result.StalePID != 999999999 {
		t.Fatalf("expected stale PID 999999999, got %d", result.StalePID)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("expected stale PID file to be removed")
	}
}

func TestCheckActiveRun_RunningStatusNoPid(t *testing.T) {
	dir := t.TempDir()
	st := &PipelineState{Status: StatusRunning}
	st.Save(dir) //nolint:errcheck

	result, err := CheckActiveRun(dir)
	if err != nil {
		t.Fatalf("expected nil error for stale running status, got %v", err)
	}
	if result.StalePID != 0 {
		t.Fatalf("expected no stale PID, got %d", result.StalePID)
	}
}

func TestActiveRunError_Message(t *testing.T) {
	err := &ActiveRunError{Pid: 12345}
	want := "run already active (pid 12345)"
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
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
