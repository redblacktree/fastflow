// Package workspace provides workspace-trust validation so fastflow
// refuses to operate inside ignored mirrors or directories with no git origin.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrNoOrigin      = errors.New("workspace has no git origin remote")
	ErrBlockedPrefix = errors.New("workspace is under a blocked prefix")
)

// TrustOpts controls how VerifyTrustedWorkspace behaves.
type TrustOpts struct {
	// BlockedPrefixes are raw path entries (may contain ~) from flag/env.
	// A run is refused when the cwd is under any of these prefixes.
	BlockedPrefixes []string
	// Allow bypasses all trust checks (--allow-untrusted-workspace).
	Allow bool
}

// TrustInfo is returned by VerifyTrustedWorkspace on success.
type TrustInfo struct {
	RepoRoot  string
	OriginURL string
	// BlockedBy is set when ErrBlockedPrefix is returned.
	BlockedBy string
}

// VerifyTrustedWorkspace confirms that cwd is a real git clone with an origin
// remote and is not under any operator-configured blocked prefix.
func VerifyTrustedWorkspace(cwd string, opts TrustOpts) (TrustInfo, error) {
	info := TrustInfo{}

	root, err := gitRevParseShowToplevel(cwd)
	if err == nil && root != "" {
		info.RepoRoot = root
	} else {
		info.RepoRoot = cwd
	}

	if opts.Allow {
		// Still populate origin for logging, but never fail.
		info.OriginURL, _ = gitOriginURL(info.RepoRoot)
		return info, nil
	}

	originURL, err := gitOriginURL(info.RepoRoot)
	if err != nil || originURL == "" {
		return info, fmt.Errorf("%w: %s (set --allow-untrusted-workspace to bypass)",
			ErrNoOrigin, info.RepoRoot)
	}
	info.OriginURL = originURL

	absCwd := resolvedAbs(info.RepoRoot)
	for _, raw := range opts.BlockedPrefixes {
		expanded := expandTilde(strings.TrimSpace(raw))
		if expanded == "" {
			continue
		}
		absPrefix := resolvedAbs(expanded)
		if absPrefix == "" {
			continue
		}
		if pathHasPrefix(absCwd, absPrefix) {
			info.BlockedBy = absPrefix
			return info, fmt.Errorf("%w: %s is under blocked prefix %s (set --allow-untrusted-workspace to bypass)",
				ErrBlockedPrefix, absCwd, absPrefix)
		}
	}

	return info, nil
}

func gitRevParseShowToplevel(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOriginURL(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// resolvedAbs returns the absolute, symlink-resolved form of path, or the
// cleaned absolute path if symlink resolution fails.
func resolvedAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// pathHasPrefix returns true if p is equal to or a descendant of prefix,
// comparing on clean segment boundaries so /a/bcd is not under /a/b.
func pathHasPrefix(p, prefix string) bool {
	p = filepath.Clean(p)
	prefix = filepath.Clean(prefix)
	if p == prefix {
		return true
	}
	return strings.HasPrefix(p, prefix+string(filepath.Separator))
}
