package claude

import "github.com/redblacktree/fastflow/internal/backend"

func init() {
	backend.Register("claude", func(cfg backend.Config) (backend.Backend, error) {
		return New(cfg), nil
	})
}
