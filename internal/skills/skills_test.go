package skills_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/redblacktree/fastflow/internal/skills"
)

func TestList(t *testing.T) {
	names, err := skills.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(names) != 18 {
		t.Errorf("List() returned %d skills, want 18; got: %v", len(names), names)
	}

	// Verify expected skill names
	expected := []string{
		"commit",
		"create_handoff",
		"create_plan",
		"create_worktree",
		"debug",
		"describe_pr",
		"fetch_feedback",
		"implement_plan",
		"iterate_plan",
		"linear",
		"local_review",
		"oneshot",
		"oneshot_plan",
		"reply_to_comments",
		"research_codebase",
		"resolve_conflicts",
		"resume_handoff",
		"validate_plan",
	}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range expected {
		if !nameSet[want] {
			t.Errorf("List() missing skill: %s", want)
		}
	}
}

func TestInstallDir(t *testing.T) {
	dir, err := skills.InstallDir()
	if err != nil {
		t.Fatalf("InstallDir() error: %v", err)
	}
	if dir == "" {
		t.Error("InstallDir() returned empty string")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() error: %v", err)
	}
	want := filepath.Join(home, ".claude/skills/fastflow")
	if dir != want {
		t.Errorf("InstallDir() = %q, want %q", dir, want)
	}
}

func TestInstall(t *testing.T) {
	// Use a temp dir for install
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	created, skipped, overwritten, err := skills.Install(nil, false)
	if err != nil {
		t.Fatalf("Install(nil, false) error: %v", err)
	}
	if len(created) != 18 {
		t.Errorf("Install() created %d files, want 18", len(created))
	}
	if len(skipped) != 0 {
		t.Errorf("Install() skipped %d files, want 0", len(skipped))
	}
	if len(overwritten) != 0 {
		t.Errorf("Install() overwrote %d files, want 0", len(overwritten))
	}

	// Verify a skill file was actually written
	skillPath := filepath.Join(tmpDir, ".claude/skills/fastflow/commit/SKILL.md")
	if _, statErr := os.Stat(skillPath); os.IsNotExist(statErr) {
		t.Errorf("Install() did not create %s", skillPath)
	}

	// Second install without force should skip everything
	created2, skipped2, overwritten2, err2 := skills.Install(nil, false)
	if err2 != nil {
		t.Fatalf("Install(nil, false) second call error: %v", err2)
	}
	if len(created2) != 0 {
		t.Errorf("second Install() created %d files, want 0", len(created2))
	}
	if len(skipped2) != 18 {
		t.Errorf("second Install() skipped %d files, want 18", len(skipped2))
	}
	if len(overwritten2) != 0 {
		t.Errorf("second Install() overwrote %d files, want 0", len(overwritten2))
	}

	// Third install with force should overwrite everything
	created3, skipped3, overwritten3, err3 := skills.Install(nil, true)
	if err3 != nil {
		t.Fatalf("Install(nil, true) error: %v", err3)
	}
	if len(created3) != 0 {
		t.Errorf("force Install() created %d files, want 0", len(created3))
	}
	if len(skipped3) != 0 {
		t.Errorf("force Install() skipped %d files, want 0", len(skipped3))
	}
	if len(overwritten3) != 18 {
		t.Errorf("force Install() overwrote %d files, want 18", len(overwritten3))
	}
}

func TestInstallSingle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	created, skipped, overwritten, err := skills.Install([]string{"commit"}, false)
	if err != nil {
		t.Fatalf("Install([commit], false) error: %v", err)
	}
	if len(created) != 1 {
		t.Errorf("Install([commit]) created %d files, want 1", len(created))
	}
	if len(skipped) != 0 {
		t.Errorf("Install([commit]) skipped %d files, want 0", len(skipped))
	}
	if len(overwritten) != 0 {
		t.Errorf("Install([commit]) overwrote %d files, want 0", len(overwritten))
	}

	// Only commit dir should exist, not others
	commitPath := filepath.Join(tmpDir, ".claude/skills/fastflow/commit/SKILL.md")
	if _, statErr := os.Stat(commitPath); os.IsNotExist(statErr) {
		t.Errorf("Install([commit]) did not create commit/SKILL.md")
	}
	otherPath := filepath.Join(tmpDir, ".claude/skills/fastflow/debug/SKILL.md")
	if _, statErr := os.Stat(otherPath); !os.IsNotExist(statErr) {
		t.Errorf("Install([commit]) should not have created debug/SKILL.md")
	}
}

func TestInstallUnknownSkill(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, _, _, err := skills.Install([]string{"nonexistent"}, false)
	if err == nil {
		t.Error("Install([nonexistent]) should return error")
	}
}

func TestValidateInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Nothing installed yet
	missing, err := skills.ValidateInstalled([]string{"commit", "debug"})
	if err != nil {
		t.Fatalf("ValidateInstalled error: %v", err)
	}
	if len(missing) != 2 {
		t.Errorf("ValidateInstalled() missing %d skills, want 2", len(missing))
	}

	// Install commit
	if _, _, _, installErr := skills.Install([]string{"commit"}, false); installErr != nil {
		t.Fatalf("Install error: %v", installErr)
	}

	// Now commit is installed, debug is not
	missing2, err2 := skills.ValidateInstalled([]string{"commit", "debug"})
	if err2 != nil {
		t.Fatalf("ValidateInstalled error: %v", err2)
	}
	if len(missing2) != 1 || missing2[0] != "debug" {
		t.Errorf("ValidateInstalled() missing = %v, want [debug]", missing2)
	}
}
