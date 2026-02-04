// Package templates provides embedded template files for fastflow init.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:files
var files embed.FS

// WriteOptions configures how templates are written.
type WriteOptions struct {
	// Force overwrites existing files if true.
	Force bool
	// DryRun prints what would be written without writing.
	DryRun bool
}

// WriteResult contains information about the write operation.
type WriteResult struct {
	Created     []string
	Skipped     []string
	Overwritten []string
}

// Write writes all template files to the target directory.
func Write(targetDir string, opts WriteOptions) (*WriteResult, error) {
	result := &WriteResult{}

	err := fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the root "files" directory
		if path == "files" {
			return nil
		}

		// Get relative path without "files/" prefix
		relPath, _ := filepath.Rel("files", path)
		targetPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			if !opts.DryRun {
				if err := os.MkdirAll(targetPath, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
				}
			}
			return nil
		}

		// Check if file exists
		if _, err := os.Stat(targetPath); err == nil {
			if !opts.Force {
				result.Skipped = append(result.Skipped, relPath)
				return nil
			}
			result.Overwritten = append(result.Overwritten, relPath)
		} else {
			result.Created = append(result.Created, relPath)
		}

		if opts.DryRun {
			return nil
		}

		// Read embedded file
		content, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", targetPath, err)
		}

		// Write file
		if err := os.WriteFile(targetPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}

		return nil
	})

	return result, err
}

// List returns all embedded file paths (relative to files/).
func List() ([]string, error) {
	var paths []string
	err := fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path != "files" {
			relPath, _ := filepath.Rel("files", path)
			paths = append(paths, relPath)
		}
		return nil
	})
	return paths, err
}
