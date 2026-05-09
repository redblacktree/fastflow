package codex

import (
	"strings"

	"github.com/redblacktree/fastflow/internal/harness"
)

// classifyAuthError detects Codex CLI auth failures from stderr/stdout output.
// Strings sourced from codex CLI 0.x error output; kept centralized for easy updates.
func classifyAuthError(raw string) error {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "not logged in"),
		strings.Contains(lower, "run `codex login`"),
		strings.Contains(lower, "openai_api_key is not set"):
		return harness.ErrNotLoggedIn
	case strings.Contains(lower, "401"),
		strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "incorrect api key"):
		return harness.ErrInvalidAPIKey
	case strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "429"):
		return harness.ErrRateLimited
	}
	return nil
}
