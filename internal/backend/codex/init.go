package codex

import "github.com/redblacktree/fastflow/internal/backend"

func init() {
	backend.Register("codex", func(cfg backend.Config) (backend.Backend, error) {
		return New(cfg), nil
	})
}
