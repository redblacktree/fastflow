package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/harness"
)

// ---------- stripFrontmatter ----------

func TestStripFrontmatter_NoFrontmatter(t *testing.T) {
	in := "Hello, world!\n"
	if got := stripFrontmatter(in); got != in {
		t.Errorf("got %q, want %q", got, in)
	}
}

func TestStripFrontmatter_WithFrontmatter(t *testing.T) {
	in := "---\nmodel: opus\ntools: all\n---\nBody content here\n"
	want := "Body content here\n"
	if got := stripFrontmatter(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripFrontmatter_UnclosedFrontmatter(t *testing.T) {
	// If there is no closing ---, return the original string unchanged.
	in := "---\nmodel: opus\n"
	if got := stripFrontmatter(in); got != in {
		t.Errorf("got %q, want %q (should return original when unclosed)", got, in)
	}
}

// ---------- resolveSkill ----------

func TestResolveSkill_CodexTakesPriority(t *testing.T) {
	dir := t.TempDir()
	codexPath := filepath.Join(dir, ".codex", "commands", "foo.md")
	os.MkdirAll(filepath.Dir(codexPath), 0755)
	os.WriteFile(codexPath, []byte("codex skill body"), 0644)

	claudePath := filepath.Join(dir, ".claude", "commands", "foo.md")
	os.MkdirAll(filepath.Dir(claudePath), 0755)
	os.WriteFile(claudePath, []byte("claude skill body"), 0644)

	body, err := resolveSkill(dir, "foo")
	if err != nil {
		t.Fatalf("resolveSkill: %v", err)
	}
	if body != "codex skill body" {
		t.Errorf("body = %q, want \"codex skill body\"", body)
	}
}

func TestResolveSkill_FallsBackToClaude(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, ".claude", "commands", "bar.md")
	os.MkdirAll(filepath.Dir(claudePath), 0755)
	os.WriteFile(claudePath, []byte("---\ntools: all\n---\nClaude skill content\n"), 0644)

	body, err := resolveSkill(dir, "bar")
	if err != nil {
		t.Fatalf("resolveSkill: %v", err)
	}
	if body != "Claude skill content\n" {
		t.Errorf("body = %q, want stripped Claude skill content", body)
	}
}

func TestResolveSkill_ErrorWhenNeitherExists(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveSkill(dir, "nonexistent")
	if err == nil {
		t.Error("expected error when skill not found")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention skill name, got: %v", err)
	}
}

// ---------- classifyAuthError ----------

func TestClassifyAuthError_NotLoggedIn(t *testing.T) {
	cases := []string{
		"Error: not logged in",
		"run `codex login` to authenticate",
		"OPENAI_API_KEY is not set",
	}
	for _, c := range cases {
		if err := classifyAuthError(c); err != harness.ErrNotLoggedIn {
			t.Errorf("classifyAuthError(%q) = %v, want ErrNotLoggedIn", c, err)
		}
	}
}

func TestClassifyAuthError_InvalidAPIKey(t *testing.T) {
	cases := []string{
		"401 Unauthorized",
		"invalid api key provided",
		"incorrect api key",
	}
	for _, c := range cases {
		if err := classifyAuthError(c); err != harness.ErrInvalidAPIKey {
			t.Errorf("classifyAuthError(%q) = %v, want ErrInvalidAPIKey", c, err)
		}
	}
}

func TestClassifyAuthError_RateLimit(t *testing.T) {
	cases := []string{
		"rate limit exceeded",
		"429 Too Many Requests",
	}
	for _, c := range cases {
		if err := classifyAuthError(c); err != harness.ErrRateLimited {
			t.Errorf("classifyAuthError(%q) = %v, want ErrRateLimited", c, err)
		}
	}
}

func TestClassifyAuthError_NoError(t *testing.T) {
	if err := classifyAuthError("Task completed successfully."); err != nil {
		t.Errorf("classifyAuthError(normal output) = %v, want nil", err)
	}
}

// ---------- parseCodexStream ----------

func TestParseCodexStream_CollectsSessionID(t *testing.T) {
	ndjson := `{"type":"session_started","session_id":"sess-abc123"}` + "\n" +
		`{"type":"assistant_message","content":"Hello"}` + "\n"

	sessionID, _ := parseCodexStream(strings.NewReader(ndjson), false, false)
	if sessionID != "sess-abc123" {
		t.Errorf("sessionID = %q, want sess-abc123", sessionID)
	}
}

func TestParseCodexStream_IgnoresUnknownEventTypes(t *testing.T) {
	ndjson := `{"type":"unknown_future_event","data":{"foo":"bar"}}` + "\n" +
		`{"type":"session_started","session_id":"sess-xyz"}` + "\n"

	sessionID, raw := parseCodexStream(strings.NewReader(ndjson), false, false)
	if sessionID != "sess-xyz" {
		t.Errorf("sessionID = %q, want sess-xyz", sessionID)
	}
	if !strings.Contains(raw, "unknown_future_event") {
		t.Error("raw output should include all lines")
	}
}

func TestParseCodexStream_HandlesInvalidJSON(t *testing.T) {
	ndjson := "not json at all\n" +
		`{"type":"session_started","session_id":"ok"}` + "\n"

	// Should not panic; should still collect the valid session_id line
	sessionID, _ := parseCodexStream(strings.NewReader(ndjson), false, false)
	if sessionID != "ok" {
		t.Errorf("sessionID = %q, want ok", sessionID)
	}
}

func TestParseCodexStream_VerboseToolActivity(t *testing.T) {
	ndjson := `{"type":"function_call","name":"write_file","arguments":{"path":"main.go","content":"package main"}}` + "\n"

	// Verbose=true should not panic; we just verify it runs without error
	_, raw := parseCodexStream(strings.NewReader(ndjson), true, false)
	if !strings.Contains(raw, "function_call") {
		t.Error("raw output should contain function_call event")
	}
}

// ---------- harness interface ----------

func TestCodexHarness_Interface(t *testing.T) {
	b := New(harness.Config{})
	if b.Name() != "codex" {
		t.Errorf("Name() = %q, want codex", b.Name())
	}
	caps := b.Capabilities()
	if caps.SupportsBudget {
		t.Error("SupportsBudget should be false")
	}
	if caps.SupportsMaxTurns {
		t.Error("SupportsMaxTurns should be false")
	}
	if caps.SupportsSlashCommands {
		t.Error("SupportsSlashCommands should be false")
	}
	if !caps.SupportsResume {
		t.Error("SupportsResume should be true")
	}
	if !caps.SupportsStreaming {
		t.Error("SupportsStreaming should be true")
	}
}

func TestCodexHarness_BinaryAndModelOverride(t *testing.T) {
	b := New(harness.Config{Binary: "/opt/codex", DefaultModel: "gpt-4o"})
	if b.binary != "/opt/codex" {
		t.Errorf("binary = %q, want /opt/codex", b.binary)
	}
	if b.DefaultModel() != "gpt-4o" {
		t.Errorf("DefaultModel() = %q, want gpt-4o", b.DefaultModel())
	}
}
