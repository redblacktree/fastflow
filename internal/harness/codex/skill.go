package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveSkill returns the body of a skill definition for inlining into a Codex prompt.
// Looks up native Codex skills/commands first, then falls back to Claude
// command files (stripping frontmatter). Returns an error if none exists.
func resolveSkill(workDir, skill string) (string, error) {
	candidates := []string{
		filepath.Join(workDir, ".codex", "skills", skill, "SKILL.md"),
		filepath.Join(workDir, ".codex", "commands", skill+".md"),
		filepath.Join(workDir, ".claude", "commands", skill+".md"),
	}
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			return stripFrontmatter(string(data)), nil
		}
	}
	return "", fmt.Errorf("skill %q not found in .codex/skills/, .codex/commands/, or .claude/commands/", skill)
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return s
	}
	body := rest[end+4:]
	return strings.TrimLeft(body, "\n")
}
