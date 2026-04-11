package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	files, err := List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	// Should have 5 files: orchestrator.json + 4 .gitkeep files
	if len(files) != 5 {
		t.Errorf("Expected 5 files, got %d: %v", len(files), files)
	}

	// Check for expected files
	expected := []string{
		"orchestrator.json",
		"thoughts/shared/handoffs/.gitkeep",
		"thoughts/shared/plans/.gitkeep",
		"thoughts/shared/research/.gitkeep",
		"thoughts/shared/runs/.gitkeep",
	}

	fileMap := make(map[string]bool)
	for _, f := range files {
		fileMap[f] = true
	}

	for _, e := range expected {
		if !fileMap[e] {
			t.Errorf("Expected file %s not found in list", e)
		}
	}
}

func TestWrite(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "fastflow-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write templates
	result, err := Write(tmpDir, WriteOptions{})
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	// Should have created files
	if len(result.Created) == 0 {
		t.Error("Expected files to be created")
	}

	// Verify orchestrator.json exists
	configPath := filepath.Join(tmpDir, "orchestrator.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("orchestrator.json was not created")
	}

	// Verify .claude directory is NOT created (skills are installed separately)
	claudeDir := filepath.Join(tmpDir, ".claude")
	if _, err := os.Stat(claudeDir); err == nil {
		t.Error(".claude directory should not be created by templates.Write()")
	}
}

func TestWriteForce(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "fastflow-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// First write
	_, err = Write(tmpDir, WriteOptions{})
	if err != nil {
		t.Fatalf("First Write() error: %v", err)
	}

	// Second write without force - should skip
	result, err := Write(tmpDir, WriteOptions{Force: false})
	if err != nil {
		t.Fatalf("Second Write() error: %v", err)
	}

	if len(result.Skipped) == 0 {
		t.Error("Expected files to be skipped without force")
	}
	if len(result.Created) > 0 {
		t.Error("Expected no files to be created on second run")
	}

	// Third write with force - should overwrite
	result, err = Write(tmpDir, WriteOptions{Force: true})
	if err != nil {
		t.Fatalf("Third Write() error: %v", err)
	}

	if len(result.Overwritten) == 0 {
		t.Error("Expected files to be overwritten with force")
	}
}

func TestTemplateContainsBudgetConfig(t *testing.T) {
	dir := t.TempDir()
	_, err := Write(dir, WriteOptions{Force: true})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "orchestrator.json"))
	if err != nil {
		t.Fatalf("failed to read orchestrator.json: %v", err)
	}

	if !strings.Contains(string(content), "maxBudgetUsd") {
		t.Error("orchestrator.json template missing maxBudgetUsd")
	}
	if !strings.Contains(string(content), "_maxBudgetNote") {
		t.Error("orchestrator.json template missing _maxBudgetNote")
	}
}

func TestWriteDryRun(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "fastflow-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write with dry run
	result, err := Write(tmpDir, WriteOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	// Should report files that would be created
	if len(result.Created) == 0 {
		t.Error("Expected files to be reported as would-be-created")
	}

	// But no files should actually exist
	configPath := filepath.Join(tmpDir, "orchestrator.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("File should not exist in dry run mode")
	}
}
