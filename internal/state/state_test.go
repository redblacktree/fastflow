package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewState(t *testing.T) {
	s := NewState("full", []string{"research", "plan"}, "TEST-001", "/tmp/work")

	if s.Ticket != "TEST-001" {
		t.Errorf("Ticket = %q, want %q", s.Ticket, "TEST-001")
	}
	if s.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", s.Status, StatusRunning)
	}
	if s.WorkDir != "/tmp/work" {
		t.Errorf("WorkDir = %q, want %q", s.WorkDir, "/tmp/work")
	}
	if s.StartedAt == "" {
		t.Error("StartedAt should not be empty")
	}
	if s.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
	if s.Workflow != "full" {
		t.Errorf("Workflow = %q, want %q", s.Workflow, "full")
	}
	if len(s.CompletedStages) != 0 {
		t.Errorf("CompletedStages = %v, want empty", s.CompletedStages)
	}
}

func TestSetStatus(t *testing.T) {
	dir := t.TempDir()
	s := NewState("full", []string{"research"}, "TEST-002", "/tmp/work")
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := s.SetStatus(dir, StatusCheckpoint); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Status != StatusCheckpoint {
		t.Errorf("Status = %q, want %q", loaded.Status, StatusCheckpoint)
	}
	if loaded.UpdatedAt == "" {
		t.Error("UpdatedAt should be set after SetStatus")
	}
}

func TestSetStage(t *testing.T) {
	dir := t.TempDir()
	s := NewState("full", []string{"research", "plan"}, "TEST-003", "/tmp/work")
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := s.SetStage(dir, "plan"); err != nil {
		t.Fatalf("SetStage failed: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Stage != "plan" {
		t.Errorf("Stage = %q, want %q", loaded.Stage, "plan")
	}
}

func TestBackwardCompatibility(t *testing.T) {
	// Simulate an old state.json without the new fields
	dir := t.TempDir()
	oldState := map[string]interface{}{
		"completed_stages": []string{"research"},
		"workflow":         "full",
		"config_hash":      "abc123",
	}
	data, err := json.MarshalIndent(oldState, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), data, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	// New fields should be zero values
	if loaded.Ticket != "" {
		t.Errorf("Ticket = %q, want empty", loaded.Ticket)
	}
	if loaded.Status != "" {
		t.Errorf("Status = %q, want empty", loaded.Status)
	}
	if loaded.Stage != "" {
		t.Errorf("Stage = %q, want empty", loaded.Stage)
	}
	// Old fields should still load
	if loaded.Workflow != "full" {
		t.Errorf("Workflow = %q, want %q", loaded.Workflow, "full")
	}
	if len(loaded.CompletedStages) != 1 || loaded.CompletedStages[0] != "research" {
		t.Errorf("CompletedStages = %v, want [research]", loaded.CompletedStages)
	}
}

func TestScanRunDirs(t *testing.T) {
	dir := t.TempDir()

	// Create two run directories with state.json
	for _, ticket := range []string{"SCAN-001", "SCAN-002"} {
		runDir := filepath.Join(dir, "thoughts", "shared", "runs", ticket)
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		s := NewState("full", []string{"plan"}, ticket, dir)
		if err := s.Save(runDir); err != nil {
			t.Fatalf("save failed: %v", err)
		}
	}

	// Create a directory without state.json (should be skipped)
	noStateDir := filepath.Join(dir, "thoughts", "shared", "runs", "NO-STATE")
	if err := os.MkdirAll(noStateDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	runs, err := ScanRunDirs(dir)
	if err != nil {
		t.Fatalf("ScanRunDirs failed: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}

	tickets := make(map[string]bool)
	for _, r := range runs {
		tickets[r.Ticket] = true
		if r.State == nil {
			t.Errorf("run %s has nil state", r.Ticket)
		}
		if r.State.Ticket != r.Ticket {
			t.Errorf("state ticket %q != dir ticket %q", r.State.Ticket, r.Ticket)
		}
	}
	if !tickets["SCAN-001"] {
		t.Error("SCAN-001 not found")
	}
	if !tickets["SCAN-002"] {
		t.Error("SCAN-002 not found")
	}
}

func TestScanRunDirs_NoDirectory(t *testing.T) {
	dir := t.TempDir()
	// No thoughts/shared/runs/ exists

	runs, err := ScanRunDirs(dir)
	if err != nil {
		t.Fatalf("ScanRunDirs failed: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs, want 0", len(runs))
	}
}
