package claude

import "github.com/redblacktree/fastflow/internal/harness"

func init() {
	harness.Register("claude", func(cfg harness.Config) (harness.Harness, error) {
		return New(cfg), nil
	})
}
