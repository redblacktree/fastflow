// Package worktree provides git worktree management for fastflow.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// DefaultWorktreeBase is the default base directory for worktrees.
	DefaultWorktreeBase = "~/wt"
)

// Manager handles git worktree operations.
type Manager struct {
	// BaseDir is the base directory for worktrees (e.g., ~/wt).
	BaseDir string
	// RepoName is the name of the repository.
	RepoName string
	// RepoPath is the path to the main repository.
	RepoPath string
}

// NewManager creates a new worktree manager.
func NewManager(repoPath string) (*Manager, error) {
	// Get the repository name from the remote URL or directory name
	repoName, err := getRepoName(repoPath)
	if err != nil {
		return nil, err
	}

	baseDir := expandPath(DefaultWorktreeBase)

	return &Manager{
		BaseDir:  baseDir,
		RepoName: repoName,
		RepoPath: repoPath,
	}, nil
}

// WorktreePath returns the path where a worktree would be created for the given ticket.
func (m *Manager) WorktreePath(ticket string) string {
	return filepath.Join(m.BaseDir, m.RepoName, ticket)
}

// Exists checks if a worktree already exists for the given ticket.
func (m *Manager) Exists(ticket string) bool {
	wtPath := m.WorktreePath(ticket)
	_, err := os.Stat(wtPath)
	return err == nil
}

// Create creates a new worktree for the given ticket.
// It creates a new branch from the main branch.
func (m *Manager) Create(ticket string) (string, error) {
	wtPath := m.WorktreePath(ticket)

	// Check if worktree already exists
	if _, err := os.Stat(wtPath); err == nil {
		return wtPath, nil // Already exists
	}

	// Get the main branch name
	mainBranch, err := m.getMainBranch()
	if err != nil {
		return "", fmt.Errorf("failed to determine main branch: %w", err)
	}

	// Ensure the base directory exists
	baseDir := filepath.Dir(wtPath)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create the worktree with a new branch
	branchName := ticket
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, wtPath, mainBranch)
	cmd.Dir = m.RepoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try without -b if branch already exists
		cmd = exec.Command("git", "worktree", "add", wtPath, branchName)
		cmd.Dir = m.RepoPath
		output2, err2 := cmd.CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("failed to create worktree: %s\n%s", string(output), string(output2))
		}
	}
	_ = output // Used for error message if needed

	return wtPath, nil
}

// Remove removes a worktree for the given ticket.
func (m *Manager) Remove(ticket string) error {
	wtPath := m.WorktreePath(ticket)

	cmd := exec.Command("git", "worktree", "remove", wtPath)
	cmd.Dir = m.RepoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove worktree: %s", string(output))
	}

	return nil
}

// getMainBranch returns the name of the main branch (main or master).
func (m *Manager) getMainBranch() (string, error) {
	// Try to get the default branch from remote
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = m.RepoPath
	output, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(output))
		// Extract branch name from refs/remotes/origin/main
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	// Fall back to checking if main or master exists
	for _, branch := range []string{"main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = m.RepoPath
		if err := cmd.Run(); err == nil {
			return branch, nil
		}
	}

	return "", fmt.Errorf("could not determine main branch")
}

// getRepoName extracts the repository name from the git remote or directory.
func getRepoName(repoPath string) (string, error) {
	// Try to get from remote URL
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err == nil {
		url := strings.TrimSpace(string(output))
		// Extract name from URLs like:
		// https://github.com/user/repo.git
		// git@github.com:user/repo.git
		url = strings.TrimSuffix(url, ".git")
		parts := strings.Split(url, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	// Fall back to directory name
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Base(absPath), nil
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
