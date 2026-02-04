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
// If force is true, it will remove even with uncommitted changes.
func (m *Manager) Remove(ticket string) error {
	wtPath := m.WorktreePath(ticket)

	// First try normal removal
	cmd := exec.Command("git", "worktree", "remove", wtPath)
	cmd.Dir = m.RepoPath
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// If normal removal fails, check if it's an orphaned directory
	// (exists on filesystem but not registered as git worktree)
	if _, statErr := os.Stat(wtPath); statErr == nil {
		// Check if it's a registered git worktree
		listCmd := exec.Command("git", "worktree", "list", "--porcelain")
		listCmd.Dir = m.RepoPath
		listOutput, _ := listCmd.Output()
		if !strings.Contains(string(listOutput), wtPath) {
			// It's an orphaned directory, just remove it
			if err := os.RemoveAll(wtPath); err != nil {
				return fmt.Errorf("failed to remove orphaned directory: %w", err)
			}
			return nil
		}
	}

	// Try force removal
	cmd = exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = m.RepoPath
	if output2, err2 := cmd.CombinedOutput(); err2 != nil {
		return fmt.Errorf("failed to remove worktree: %s\n%s", string(output), string(output2))
	}

	return nil
}

// WorktreeInfo contains information about a discovered worktree.
type WorktreeInfo struct {
	Ticket   string // Ticket/branch name (directory name under repo)
	Path     string // Full filesystem path to the worktree
	Branch   string // Git branch name
	IsOrphan bool   // True if directory exists but not registered as git worktree
}

// List returns all worktrees for this repository.
// It combines git worktree information with filesystem discovery.
func (m *Manager) List() ([]WorktreeInfo, error) {
	var results []WorktreeInfo
	seen := make(map[string]bool)

	// First, get all registered git worktrees
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = m.RepoPath
	output, err := cmd.Output()
	if err == nil {
		// Parse porcelain output: "worktree <path>\nHEAD <sha>\nbranch <ref>\n\n"
		lines := strings.Split(string(output), "\n")
		var currentPath, currentBranch string
		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				currentPath = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch ") {
				ref := strings.TrimPrefix(line, "branch ")
				// Extract branch name from refs/heads/FAS-006
				parts := strings.Split(ref, "/")
				currentBranch = parts[len(parts)-1]
			} else if line == "" && currentPath != "" {
				// End of worktree entry
				// Only include worktrees under our base directory
				repoBase := filepath.Join(m.BaseDir, m.RepoName)
				if strings.HasPrefix(currentPath, repoBase) {
					ticket := filepath.Base(currentPath)
					results = append(results, WorktreeInfo{
						Ticket:   ticket,
						Path:     currentPath,
						Branch:   currentBranch,
						IsOrphan: false,
					})
					seen[ticket] = true
				}
				currentPath = ""
				currentBranch = ""
			}
		}
	}

	// Second, check for orphaned directories (exist but not git worktrees)
	repoDir := filepath.Join(m.BaseDir, m.RepoName)
	entries, err := os.ReadDir(repoDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !seen[entry.Name()] {
				ticket := entry.Name()
				results = append(results, WorktreeInfo{
					Ticket:   ticket,
					Path:     filepath.Join(repoDir, ticket),
					Branch:   ticket, // Assume branch matches ticket
					IsOrphan: true,
				})
			}
		}
	}

	return results, nil
}

// TicketInfo contains metadata about a ticket from its goal file.
type TicketInfo struct {
	Ticket  string
	Goal    string
	Created string
}

// ReadTicketInfo reads ticket information from a goal.md file.
// Returns nil if the file doesn't exist or can't be parsed.
func ReadTicketInfo(workDir, ticket string) *TicketInfo {
	goalPath := filepath.Join(workDir, "thoughts", "shared", "runs", ticket, "goal.md")
	content, err := os.ReadFile(goalPath)
	if err != nil {
		return nil
	}

	text := string(content)
	if !strings.HasPrefix(text, "---") {
		return nil
	}

	lines := strings.Split(text, "\n")
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return nil
	}

	info := &TicketInfo{Ticket: ticket}
	for i := 1; i < endIdx; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "goal:") {
			info.Goal = strings.TrimSpace(strings.TrimPrefix(line, "goal:"))
		} else if strings.HasPrefix(line, "created:") {
			info.Created = strings.TrimSpace(strings.TrimPrefix(line, "created:"))
		}
	}

	return info
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
