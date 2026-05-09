package codex

import "github.com/redblacktree/fastflow/internal/harness"

func init() {
	harness.Register("codex", func(cfg harness.Config) (harness.Harness, error) {
		return New(cfg), nil
	})
}
