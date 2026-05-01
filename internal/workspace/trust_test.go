package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a git repo at dir with the given origin URL (may be empty).
func initGitRepo(t *testing.T, dir, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	if originURL != "" {
		run("git", "remote", "add", "origin", originURL)
	}
	// Commit so the repo has a HEAD.
	if err := os.WriteFile(filepath.Join(dir, ".keep"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "init")
}

func TestVerifyTrustedWorkspace_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "https://github.com/x/y.git")

	info, err := VerifyTrustedWorkspace(tmp, TrustOpts{})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if info.OriginURL != "https://github.com/x/y.git" {
		t.Errorf("want origin URL, got %q", info.OriginURL)
	}
	if info.RepoRoot == "" {
		t.Error("RepoRoot should be set")
	}
}

func TestVerifyTrustedWorkspace_NoRemote(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "") // no origin

	_, err := VerifyTrustedWorkspace(tmp, TrustOpts{})
	if !errors.Is(err, ErrNoOrigin) {
		t.Fatalf("want ErrNoOrigin, got %v", err)
	}
}

func TestVerifyTrustedWorkspace_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	// Plain directory, not a git repo.
	_, err := VerifyTrustedWorkspace(tmp, TrustOpts{})
	if !errors.Is(err, ErrNoOrigin) {
		t.Fatalf("want ErrNoOrigin for non-git dir, got %v", err)
	}
}

func TestVerifyTrustedWorkspace_BlockedPrefix(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "blocked", "fastflow")
	initGitRepo(t, repo, "https://github.com/x/y.git")

	info, err := VerifyTrustedWorkspace(repo, TrustOpts{
		BlockedPrefixes: []string{filepath.Join(tmp, "blocked")},
	})
	if !errors.Is(err, ErrBlockedPrefix) {
		t.Fatalf("want ErrBlockedPrefix, got %v", err)
	}
	if info.BlockedBy == "" {
		t.Error("BlockedBy should be set")
	}
}

func TestVerifyTrustedWorkspace_SiblingNotBlocked(t *testing.T) {
	// /a/bcd should NOT be blocked by prefix /a/b.
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "a", "b")
	repo := filepath.Join(tmp, "a", "bcd")
	initGitRepo(t, repo, "https://github.com/x/y.git")

	_, err := VerifyTrustedWorkspace(repo, TrustOpts{
		BlockedPrefixes: []string{prefix},
	})
	if err != nil {
		t.Fatalf("sibling path should not be blocked, got %v", err)
	}
}

func TestVerifyTrustedWorkspace_AllowBypassesBothChecks(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo at all — but Allow=true should skip all checks.
	info, err := VerifyTrustedWorkspace(tmp, TrustOpts{
		BlockedPrefixes: []string{tmp},
		Allow:           true,
	})
	if err != nil {
		t.Fatalf("Allow=true should bypass all checks, got %v", err)
	}
	if info.BlockedBy != "" {
		t.Error("BlockedBy should not be set when Allow=true")
	}
}

func TestPathHasPrefix(t *testing.T) {
	cases := []struct {
		p, prefix string
		want      bool
	}{
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/bcd", "/a/b", false},
		{"/a/b", "/a/b/c", false},
		{"/", "/a", false},
	}
	for _, c := range cases {
		got := pathHasPrefix(c.p, c.prefix)
		if got != c.want {
			t.Errorf("pathHasPrefix(%q, %q) = %v, want %v", c.p, c.prefix, got, c.want)
		}
	}
}
