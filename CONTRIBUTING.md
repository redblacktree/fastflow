# Contributing to fastflow

Thank you for your interest in contributing to fastflow!

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/fastflow.git`
3. Create a branch: `git checkout -b feature/your-feature`
4. Make your changes
5. Run tests: `go test ./...`
6. Commit your changes
7. Push and open a Pull Request

## Development Setup

```bash
# Clone the repository
git clone https://github.com/redblacktree/fastflow.git
cd fastflow

# Build
go build ./cmd/fastflow

# Run tests
go test ./...

# Run validation
./fastflow validate
```

## Working directories

fastflow's workspace-trust guardrail (`internal/workspace/trust.go`) checks the resolved working directory against a list of blocked path prefixes before any run starts. This prevents accidental runs against stale workspace mirrors (e.g. `~/.openclaw/workspace/*`).

When writing tests or tooling that invokes `fastflow run`:

- **Always use the canonical repo worktree** (e.g. `/Users/you/repos/fastflow` or a worktree under `/Users/you/wt/fastflow/`). Do not invoke fastflow from a path inside a stale mirror.
- If you need to test the guardrail itself, see `internal/workspace/trust_test.go` for examples of how to construct a temporary git repo and verify that blocked prefixes are refused.
- The `--allow-untrusted-workspace` flag (or `FASTFLOW_ALLOW_UNTRUSTED_WORKSPACE=1`) exists for integration tests that must run from an arbitrary directory; avoid using it in production scripts.

## Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Run `go vet` to check for issues
- Keep commits focused and atomic

## Pull Request Guidelines

- Provide a clear description of the changes
- Reference any related issues
- Ensure all tests pass
- Keep PRs focused on a single feature or fix

## Reporting Issues

When reporting issues, please include:

- Go version (`go version`)
- Operating system
- Steps to reproduce
- Expected vs actual behavior

## Questions?

Open an issue with the "question" label if you have any questions about contributing.
