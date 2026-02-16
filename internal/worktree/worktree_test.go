package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupBareOriginAndClone creates a bare "origin" repo with one commit on main,
// and a clone of it. Returns (clonePath, originPath, cleanup).
func setupBareOriginAndClone(t *testing.T) (string, string, func()) {
	t.Helper()
	tmp := t.TempDir()

	originPath := filepath.Join(tmp, "origin.git")
	clonePath := filepath.Join(tmp, "clone")

	// Create bare origin
	run(t, tmp, "git", "init", "--bare", originPath)

	// Create a temporary working copy to make the initial commit
	seedPath := filepath.Join(tmp, "seed")
	run(t, tmp, "git", "clone", originPath, seedPath)
	run(t, seedPath, "git", "config", "user.email", "test@test.com")
	run(t, seedPath, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(seedPath, "README.md"), "initial")
	run(t, seedPath, "git", "add", ".")
	run(t, seedPath, "git", "commit", "-m", "initial commit")
	run(t, seedPath, "git", "push", "origin", "main")

	// Clone for the test
	run(t, tmp, "git", "clone", originPath, clonePath)
	run(t, clonePath, "git", "config", "user.email", "test@test.com")
	run(t, clonePath, "git", "config", "user.name", "Test")

	return clonePath, originPath, func() {}
}

// pushNewCommitToOrigin adds a commit to origin via a temp clone, so the
// test clone's local main is behind.
func pushNewCommitToOrigin(t *testing.T, originPath string) string {
	t.Helper()
	tmp := t.TempDir()
	pusher := filepath.Join(tmp, "pusher")
	run(t, tmp, "git", "clone", originPath, pusher)
	run(t, pusher, "git", "config", "user.email", "test@test.com")
	run(t, pusher, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(pusher, "new.txt"), "new content")
	run(t, pusher, "git", "add", ".")
	run(t, pusher, "git", "commit", "-m", "second commit")
	run(t, pusher, "git", "push", "origin", "main")

	// Return the new HEAD sha
	out := runOutput(t, pusher, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

func TestCreate_FetchEnabled(t *testing.T) {
	clonePath, originPath, cleanup := setupBareOriginAndClone(t)
	defer cleanup()

	// Push a new commit to origin that the clone doesn't have locally
	newSHA := pushNewCommitToOrigin(t, originPath)

	mgr := &Manager{
		BaseDir:           t.TempDir(),
		RepoName:          "testrepo",
		RepoPath:          clonePath,
		FetchBeforeCreate: true,
	}

	wtPath, err := mgr.Create("TEST-001")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// The worktree should be branched from the fetched commit
	wtSHA := strings.TrimSpace(runOutput(t, wtPath, "git", "rev-parse", "HEAD"))
	if wtSHA != newSHA {
		t.Errorf("worktree HEAD = %s, want %s (fetched origin commit)", wtSHA, newSHA)
	}
}

func TestCreate_FetchFallback_NoRemote(t *testing.T) {
	// Create a standalone repo with no remote
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "norepo")
	run(t, tmp, "git", "init", repoPath)
	run(t, repoPath, "git", "config", "user.email", "test@test.com")
	run(t, repoPath, "git", "config", "user.name", "Test")
	run(t, repoPath, "git", "checkout", "-b", "main")
	writeFile(t, filepath.Join(repoPath, "README.md"), "hello")
	run(t, repoPath, "git", "add", ".")
	run(t, repoPath, "git", "commit", "-m", "init")

	localSHA := strings.TrimSpace(runOutput(t, repoPath, "git", "rev-parse", "HEAD"))

	mgr := &Manager{
		BaseDir:           t.TempDir(),
		RepoName:          "testrepo",
		RepoPath:          repoPath,
		FetchBeforeCreate: true,
	}

	wtPath, err := mgr.Create("TEST-002")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Should fall back to local main branch
	wtSHA := strings.TrimSpace(runOutput(t, wtPath, "git", "rev-parse", "HEAD"))
	if wtSHA != localSHA {
		t.Errorf("worktree HEAD = %s, want %s (local main)", wtSHA, localSHA)
	}
}

func TestCreate_FetchDisabled(t *testing.T) {
	clonePath, originPath, cleanup := setupBareOriginAndClone(t)
	defer cleanup()

	// Get local main SHA before any fetch
	localSHA := strings.TrimSpace(runOutput(t, clonePath, "git", "rev-parse", "main"))

	// Push a new commit to origin
	pushNewCommitToOrigin(t, originPath)

	mgr := &Manager{
		BaseDir:           t.TempDir(),
		RepoName:          "testrepo",
		RepoPath:          clonePath,
		FetchBeforeCreate: false,
	}

	wtPath, err := mgr.Create("TEST-003")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Should use local main, not the new origin commit
	wtSHA := strings.TrimSpace(runOutput(t, wtPath, "git", "rev-parse", "HEAD"))
	if wtSHA != localSHA {
		t.Errorf("worktree HEAD = %s, want %s (local main, no fetch)", wtSHA, localSHA)
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	clonePath, _, cleanup := setupBareOriginAndClone(t)
	defer cleanup()

	mgr := &Manager{
		BaseDir:           t.TempDir(),
		RepoName:          "testrepo",
		RepoPath:          clonePath,
		FetchBeforeCreate: true,
	}

	// Create once
	wtPath1, err := mgr.Create("TEST-004")
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Create again — should return early
	wtPath2, err := mgr.Create("TEST-004")
	if err != nil {
		t.Fatalf("second Create failed: %v", err)
	}

	if wtPath1 != wtPath2 {
		t.Errorf("paths differ: %s vs %s", wtPath1, wtPath2)
	}
}

func TestNewManager_Defaults(t *testing.T) {
	// Create a minimal git repo
	tmp := t.TempDir()
	run(t, tmp, "git", "init")

	mgr, err := NewManager(tmp)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if !mgr.FetchBeforeCreate {
		t.Error("FetchBeforeCreate should default to true")
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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command %s %v failed: %v", name, args, err)
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
