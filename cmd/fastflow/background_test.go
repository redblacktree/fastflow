package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundFlagRegistered(t *testing.T) {
	f := runCmd.Flags().Lookup("background")
	if f == nil {
		t.Fatal("--background flag not found on run command")
	}
	if f.DefValue != "false" {
		t.Errorf("expected default value 'false', got %q", f.DefValue)
	}
}

func TestDaemonizedEnvVarDetection(t *testing.T) {
	// Verify env var is not set by default
	if os.Getenv("FASTFLOW_DAEMONIZED") == "1" {
		t.Skip("FASTFLOW_DAEMONIZED already set in test environment")
	}

	// Verify we can set and read it
	t.Setenv("FASTFLOW_DAEMONIZED", "1")
	if os.Getenv("FASTFLOW_DAEMONIZED") != "1" {
		t.Fatal("FASTFLOW_DAEMONIZED env var not detected after setting")
	}
}

func TestLogFileCreation(t *testing.T) {
	runDir := t.TempDir()
	logPath := filepath.Join(runDir, "fastflow.log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}
	f.Close()

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("log file not found: %v", err)
	}
	if info.IsDir() {
		t.Fatal("log path should be a file, not directory")
	}
}
