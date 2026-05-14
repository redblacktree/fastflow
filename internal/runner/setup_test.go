package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a git repo with one commit on main in a temp dir,
// and chdir's into it. Returns (baseDir for worktrees, cleanup func).
func setupTestRepo(t *testing.T) (string, func()) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	baseDir := filepath.Join(tmp, "wt")

	run(t, tmp, "git", "init", repoPath)
	run(t, repoPath, "git", "config", "user.email", "test@test.com")
	run(t, repoPath, "git", "config", "user.name", "Test")
	run(t, repoPath, "git", "checkout", "-b", "main")
	writeFile(t, filepath.Join(repoPath, "README.md"), "hello")
	run(t, repoPath, "git", "add", ".")
	run(t, repoPath, "git", "commit", "-m", "init")

	if err := os.Chdir(repoPath); err != nil {
		t.Fatal(err)
	}

	return baseDir, func() {
		os.Chdir(origDir)
	}
}

func TestSetupWorktree_HooksCalledOnCreate(t *testing.T) {
	baseDir, cleanup := setupTestRepo(t)
	defer cleanup()

	var hookCalledWith string
	opts := SetupWorktreeOpts{
		NoFetch: true, // no remote to fetch from
		BaseDir: baseDir,
		AfterCreate: []func(string) error{
			func(wtPath string) error {
				hookCalledWith = wtPath
				// Create a marker file to prove we ran
				return os.WriteFile(filepath.Join(wtPath, ".hook-marker"), []byte("ran"), 0644)
			},
		},
	}

	wtPath, existed, err := SetupWorktree("HOOK-001", opts)
	if err != nil {
		t.Fatalf("SetupWorktree failed: %v", err)
	}

	if existed {
		t.Error("expected existed=false for fresh worktree")
	}

	if hookCalledWith != wtPath {
		t.Errorf("hook called with %q, want %q", hookCalledWith, wtPath)
	}

	// Verify marker file exists in the worktree
	marker := filepath.Join(wtPath, ".hook-marker")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file not found: %v", err)
	}
}

func TestSetupWorktree_HooksSkippedWhenExists(t *testing.T) {
	baseDir, cleanup := setupTestRepo(t)
	defer cleanup()

	hookCallCount := 0
	opts := SetupWorktreeOpts{
		NoFetch: true,
		BaseDir: baseDir,
		AfterCreate: []func(string) error{
			func(wtPath string) error {
				hookCallCount++
				return nil
			},
		},
	}

	// First call — creates worktree, hook runs
	_, _, err := SetupWorktree("HOOK-002", opts)
	if err != nil {
		t.Fatalf("first SetupWorktree failed: %v", err)
	}
	if hookCallCount != 1 {
		t.Fatalf("expected hook called once, got %d", hookCallCount)
	}

	// Second call — worktree exists, hook should NOT run
	_, existed, err := SetupWorktree("HOOK-002", opts)
	if err != nil {
		t.Fatalf("second SetupWorktree failed: %v", err)
	}
	if !existed {
		t.Error("expected existed=true on second call")
	}
	if hookCallCount != 1 {
		t.Errorf("hook called %d times, want 1 (should not run on existing worktree)", hookCallCount)
	}
}

func TestSetupWorktree_HookFailure(t *testing.T) {
	baseDir, cleanup := setupTestRepo(t)
	defer cleanup()

	hookErr := fmt.Errorf("hook exploded")
	opts := SetupWorktreeOpts{
		NoFetch: true,
		BaseDir: baseDir,
		AfterCreate: []func(string) error{
			func(wtPath string) error {
				return hookErr
			},
		},
	}

	wtPath, _, err := SetupWorktree("HOOK-003", opts)
	if err == nil {
		t.Fatal("expected error from hook, got nil")
	}

	// Error should mention the hook failure
	if err.Error() != "post-create hook failed: hook exploded" {
		t.Errorf("unexpected error: %v", err)
	}

	// Worktree should still exist on disk (not cleaned up)
	if _, statErr := os.Stat(wtPath); statErr != nil {
		t.Errorf("worktree should still exist after hook failure: %v", statErr)
	}
}

func TestEnsureThoughtsExcluded(t *testing.T) {
	baseDir, cleanup := setupTestRepo(t)
	defer cleanup()

	opts := SetupWorktreeOpts{
		NoFetch: true,
		BaseDir: baseDir,
		AfterCreate: []func(string) error{
			thoughtsExcludeHook(),
		},
	}

	wtPath, _, err := SetupWorktree("HOOK-004", opts)
	if err != nil {
		t.Fatalf("SetupWorktree failed: %v", err)
	}

	out := runOutput(t, wtPath, "git", "check-ignore", "thoughts/example.md")
	if strings.TrimSpace(out) != "thoughts/example.md" {
		t.Fatalf("git check-ignore output = %q, want thoughts/example.md", out)
	}

	if err := EnsureThoughtsExcluded(wtPath); err != nil {
		t.Fatalf("EnsureThoughtsExcluded second call failed: %v", err)
	}
}

// helpers

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, out)
	}
}

func runOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
