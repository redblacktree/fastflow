//go:build !windows

package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackgroundReExecEndToEnd(t *testing.T) {
	tmp := t.TempDir()

	// Initialize a minimal git repo so workspace trust / origin lookups don't error.
	repo := filepath.Join(tmp, "repo")
	mustRun(t, tmp, "git", "init", repo)
	mustRun(t, repo, "git", "config", "user.email", "t@t")
	mustRun(t, repo, "git", "config", "user.name", "t")
	mustRun(t, repo, "git", "remote", "add", "origin", "https://github.com/x/y.git")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "add", ".")
	mustRun(t, repo, "git", "commit", "-m", "init")

	writeMinimalConfig(t, repo)

	// Build the binary into the temp dir.
	bin := filepath.Join(tmp, "fastflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/fastflow")
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Run parent with --background; parent should exit 0 immediately.
	// Explicitly clear FASTFLOW_DAEMONIZED so the parent actually forks even
	// when the test itself is running inside a fastflow background process.
	cmd := exec.Command(bin,
		"run",
		"--ticket", "BG-SMOKE",
		"--goal", "smoke",
		"--no-worktree",
		"--dry-run",
		"--background",
	)
	cmd.Dir = repo
	cmd.Env = stripEnv(os.Environ(), "FASTFLOW_DAEMONIZED")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("parent exited non-zero: %v\n%s", err, out)
	}

	pid := parseChildPID(t, string(out))
	t.Logf("child pid = %d", pid)

	// The parent creates fastflow.log before spawning the child, so it should
	// appear very quickly after the parent exits.
	logPath := filepath.Join(repo, "thoughts", "shared", "runs", "BG-SMOKE", "fastflow.log")
	waitForFile(t, logPath, 5*time.Second)

	// Wait for the dry-run child to finish (completes in well under 10s).
	waitForExit(t, pid, 10*time.Second)
}

// TestRunRefusesBlockedWorkspace verifies the workspace-trust guardrail added
// in Phase 2: fastflow run exits non-zero from a blocked prefix and succeeds
// with --allow-untrusted-workspace.
func TestRunRefusesBlockedWorkspace(t *testing.T) {
	tmp := t.TempDir()

	// Create a git repo inside a "blocked" directory.
	blocked := filepath.Join(tmp, "blocked")
	repo := filepath.Join(blocked, "fastflow")
	mustRun(t, tmp, "git", "init", repo)
	mustRun(t, repo, "git", "config", "user.email", "t@t")
	mustRun(t, repo, "git", "config", "user.name", "t")
	mustRun(t, repo, "git", "remote", "add", "origin", "https://github.com/x/y.git")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "add", ".")
	mustRun(t, repo, "git", "commit", "-m", "init")

	writeMinimalConfig(t, repo)

	bin := filepath.Join(tmp, "fastflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/fastflow")
	build.Dir = projectRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Run from inside the blocked prefix — should fail.
	cmd := exec.Command(bin,
		"run",
		"--ticket", "BLOCK-TEST",
		"--goal", "smoke",
		"--no-worktree",
		"--dry-run",
		"--blocked-workspace-prefix", blocked,
	)
	cmd.Dir = repo
	cmd.Env = stripEnv(os.Environ(), "FASTFLOW_DAEMONIZED")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for blocked workspace, got success\n%s", out)
	}
	if !regexp.MustCompile(`blocked prefix`).Match(out) {
		t.Errorf("expected 'blocked prefix' in output, got:\n%s", out)
	}

	// Same command with --allow-untrusted-workspace should pass the trust check.
	cmd2 := exec.Command(bin,
		"run",
		"--ticket", "BLOCK-TEST",
		"--goal", "smoke",
		"--no-worktree",
		"--dry-run",
		"--blocked-workspace-prefix", blocked,
		"--allow-untrusted-workspace",
	)
	cmd2.Dir = repo
	cmd2.Env = stripEnv(os.Environ(), "FASTFLOW_DAEMONIZED")
	out2, err2 := cmd2.CombinedOutput()
	if err2 != nil {
		t.Fatalf("expected success with --allow-untrusted-workspace, got: %v\n%s", err2, out2)
	}
}

// --- helpers ---

func parseChildPID(t *testing.T, out string) int {
	t.Helper()
	re := regexp.MustCompile(`PID (\d+)`)
	m := re.FindStringSubmatch(out)
	if len(m) != 2 {
		t.Fatalf("could not find PID in output:\n%s", out)
	}
	pid, _ := strconv.Atoi(m[1])
	if pid <= 0 {
		t.Fatalf("invalid pid: %d", pid)
	}
	return pid
}

func waitForExit(t *testing.T, pid int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	t.Fatalf("child %d did not exit within %s", pid, d)
}

func waitForFile(t *testing.T, path string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within %s", path, d)
}

func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %v failed: %v\n%s", args, err, out)
	}
}

func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	const cfg = `{
  "default_workflow": "smoke",
  "workflows": {
    "smoke": {
      "description": "Smoke test workflow",
      "stages": ["noop"],
      "skip_goal": true
    }
  },
  "stages": {
    "noop": {
      "skill": "noop"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "orchestrator.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	// Tests run with working dir set to the package directory (cmd/fastflow).
	// The project root is two levels up.
	abs, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// stripEnv returns os.Environ() with the named key(s) removed. Used to ensure
// test subprocesses are not contaminated by vars set by a parent fastflow run.
func stripEnv(env []string, keys ...string) []string {
	skip := make(map[string]bool, len(keys))
	for _, k := range keys {
		skip[k] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if i := strings.Index(kv, "="); i >= 0 {
			key = kv[:i]
		}
		if !skip[key] {
			out = append(out, kv)
		}
	}
	return out
}
