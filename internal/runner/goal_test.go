package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGoalField(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "valid frontmatter with goal",
			content: "---\nticket: T-1\ngoal: Fix the bug\ncreated: 2024-01-01\n---\n\n# Goal\n\nFix the bug\n",
			want:    "Fix the bug",
		},
		{
			name:    "empty goal field",
			content: "---\nticket: T-1\ngoal: \ncreated: 2024-01-01\n---\n",
			want:    "",
		},
		{
			name:    "no frontmatter",
			content: "# Goal\n\nFix the bug\n",
			want:    "",
		},
		{
			name:    "no goal field in frontmatter",
			content: "---\nticket: T-1\ncreated: 2024-01-01\n---\n",
			want:    "",
		},
		{
			name:    "double-quoted goal value",
			content: "---\ngoal: \"Fix the bug\"\n---\n",
			want:    "Fix the bug",
		},
		{
			name:    "single-quoted goal value",
			content: "---\ngoal: 'Fix the bug'\n---\n",
			want:    "Fix the bug",
		},
		{
			name:    "unclosed frontmatter",
			content: "---\ngoal: Fix the bug\n",
			want:    "",
		},
		{
			name:    "empty content",
			content: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGoalField(tt.content)
			if got != tt.want {
				t.Errorf("parseGoalField() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadExistingGoal(t *testing.T) {
	t.Run("file exists with goal", func(t *testing.T) {
		dir := t.TempDir()
		goalPath := filepath.Join(dir, "goal.md")
		os.WriteFile(goalPath, []byte("---\ngoal: Fix the bug\n---\n"), 0644)

		got := ReadExistingGoal(dir)
		if got != "Fix the bug" {
			t.Errorf("ReadExistingGoal() = %q, want %q", got, "Fix the bug")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		got := ReadExistingGoal(t.TempDir())
		if got != "" {
			t.Errorf("ReadExistingGoal() = %q, want empty", got)
		}
	})

	t.Run("file exists with empty goal", func(t *testing.T) {
		dir := t.TempDir()
		goalPath := filepath.Join(dir, "goal.md")
		os.WriteFile(goalPath, []byte("---\ngoal: \n---\n"), 0644)

		got := ReadExistingGoal(dir)
		if got != "" {
			t.Errorf("ReadExistingGoal() = %q, want empty", got)
		}
	})
}

func TestWriteGoalFile_PreservesExisting(t *testing.T) {
	r := &Runner{}

	t.Run("creates new file when none exists", func(t *testing.T) {
		dir := t.TempDir()
		ctx := &RunContext{
			Goal: "Fix the bug", Ticket: "T-1",
			RunDir: dir, RepoPath: "/repo",
			BranchName: "main", WorkDir: "/work",
		}

		if err := r.writeGoalFile(ctx); err != nil {
			t.Fatalf("writeGoalFile() error: %v", err)
		}

		content, _ := os.ReadFile(filepath.Join(dir, "goal.md"))
		if got := parseGoalField(string(content)); got != "Fix the bug" {
			t.Errorf("goal field = %q, want %q", got, "Fix the bug")
		}
	})

	t.Run("preserves existing file when goal matches", func(t *testing.T) {
		dir := t.TempDir()
		goalPath := filepath.Join(dir, "goal.md")
		original := "---\nticket: T-1\ngoal: Fix the bug\ncreated: 2024-01-01T00:00:00Z\n---\n\n# Goal\n\nFix the bug\n\n# Context\n\nCustom user-edited context\n"
		os.WriteFile(goalPath, []byte(original), 0644)

		ctx := &RunContext{
			Goal: "Fix the bug", Ticket: "T-1",
			RunDir: dir, RepoPath: "/repo",
			BranchName: "main", WorkDir: "/work",
		}

		if err := r.writeGoalFile(ctx); err != nil {
			t.Fatalf("writeGoalFile() error: %v", err)
		}

		after, _ := os.ReadFile(goalPath)
		if string(after) != original {
			t.Error("existing file was modified when goal matched")
		}
	})

	t.Run("overwrites when goal changes", func(t *testing.T) {
		dir := t.TempDir()
		goalPath := filepath.Join(dir, "goal.md")
		os.WriteFile(goalPath, []byte("---\ngoal: Old goal\n---\n"), 0644)

		ctx := &RunContext{
			Goal: "New goal", Ticket: "T-1",
			RunDir: dir, RepoPath: "/repo",
			BranchName: "main", WorkDir: "/work",
		}

		if err := r.writeGoalFile(ctx); err != nil {
			t.Fatalf("writeGoalFile() error: %v", err)
		}

		after, _ := os.ReadFile(goalPath)
		if got := parseGoalField(string(after)); got != "New goal" {
			t.Errorf("goal field = %q, want %q", got, "New goal")
		}
	})

	t.Run("overwrites when existing goal is empty", func(t *testing.T) {
		dir := t.TempDir()
		goalPath := filepath.Join(dir, "goal.md")
		os.WriteFile(goalPath, []byte("---\ngoal: \n---\n"), 0644)

		ctx := &RunContext{
			Goal: "New goal", Ticket: "T-1",
			RunDir: dir, RepoPath: "/repo",
			BranchName: "main", WorkDir: "/work",
		}

		if err := r.writeGoalFile(ctx); err != nil {
			t.Fatalf("writeGoalFile() error: %v", err)
		}

		after, _ := os.ReadFile(goalPath)
		if !strings.Contains(string(after), "goal: New goal") {
			t.Error("expected goal.md to be written with new goal")
		}
	})
}
