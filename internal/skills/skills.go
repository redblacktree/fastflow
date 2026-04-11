// Package skills embeds and manages fastflow Claude Code skill files.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:files
var files embed.FS

const installDir = ".claude/skills/fastflow"

// InstallDir returns the absolute path to the skills install directory.
func InstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, installDir), nil
}

// List returns the names of all available skills.
func List() ([]string, error) {
	entries, err := fs.ReadDir(files, "files")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Install extracts embedded skills to the install directory.
// If names is empty, all skills are installed.
// If force is true, existing files are overwritten.
func Install(names []string, force bool) (created, skipped, overwritten []string, err error) {
	dir, err := InstallDir()
	if err != nil {
		return nil, nil, nil, err
	}

	// If specific names requested, validate they exist
	if len(names) > 0 {
		available, listErr := List()
		if listErr != nil {
			return nil, nil, nil, listErr
		}
		avail := make(map[string]bool, len(available))
		for _, n := range available {
			avail[n] = true
		}
		for _, n := range names {
			if !avail[n] {
				return nil, nil, nil, fmt.Errorf("unknown skill: %s", n)
			}
		}
	}

	// Walk and install
	err = fs.WalkDir(files, "files", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "files" {
			return nil
		}

		relPath, _ := filepath.Rel("files", path)
		targetPath := filepath.Join(dir, relPath)

		// Filter by name if specific skills requested
		if len(names) > 0 {
			parts := strings.SplitN(relPath, string(filepath.Separator), 2)
			skillName := parts[0]
			matched := false
			for _, n := range names {
				if n == skillName {
					matched = true
					break
				}
			}
			if !matched {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		// Check existing
		_, statErr := os.Stat(targetPath)
		exists := statErr == nil

		if exists && !force {
			skipped = append(skipped, relPath)
			return nil
		}

		data, readErr := files.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		if mkErr := os.MkdirAll(filepath.Dir(targetPath), 0755); mkErr != nil {
			return mkErr
		}

		if writeErr := os.WriteFile(targetPath, data, 0644); writeErr != nil {
			return writeErr
		}

		if exists {
			overwritten = append(overwritten, relPath)
		} else {
			created = append(created, relPath)
		}
		return nil
	})

	return created, skipped, overwritten, err
}

// ValidateInstalled checks that all named skills exist at the install path.
// Returns a list of missing skill names.
func ValidateInstalled(names []string) (missing []string, err error) {
	dir, err := InstallDir()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		skillPath := filepath.Join(dir, name, "SKILL.md")
		if _, statErr := os.Stat(skillPath); os.IsNotExist(statErr) {
			missing = append(missing, name)
		}
	}
	return missing, nil
}
