package codex

import (
	"os"
	"os/exec"
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
	codexPath := filepath.Join(dir, ".codex", "skills", "foo", "SKILL.md")
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

	sessionID, _, _ := parseCodexStream(strings.NewReader(ndjson), false, false)
	if sessionID != "sess-abc123" {
		t.Errorf("sessionID = %q, want sess-abc123", sessionID)
	}
}

func TestParseCodexStream_IgnoresUnknownEventTypes(t *testing.T) {
	ndjson := `{"type":"unknown_future_event","data":{"foo":"bar"}}` + "\n" +
		`{"type":"session_started","session_id":"sess-xyz"}` + "\n"

	sessionID, raw, _ := parseCodexStream(strings.NewReader(ndjson), false, false)
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
	sessionID, _, _ := parseCodexStream(strings.NewReader(ndjson), false, false)
	if sessionID != "ok" {
		t.Errorf("sessionID = %q, want ok", sessionID)
	}
}

func TestParseCodexStream_VerboseToolActivity(t *testing.T) {
	ndjson := `{"type":"function_call","name":"write_file","arguments":{"path":"main.go","content":"package main"}}` + "\n"

	// Verbose=true should not panic; we just verify it runs without error
	_, raw, _ := parseCodexStream(strings.NewReader(ndjson), true, false)
	if !strings.Contains(raw, "function_call") {
		t.Error("raw output should contain function_call event")
	}
}

func TestParseCodexStream_CapturesTurnUsage(t *testing.T) {
	ndjson := `{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"reasoning_output_tokens":10}}` + "\n"

	_, _, usage := parseCodexStream(strings.NewReader(ndjson), false, false)
	if usage.InputTokens != 100 || usage.OutputTokens != 30 {
		t.Fatalf("usage = %+v, want input=100 output=30", usage)
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

func TestCodexHarness_BuildArgsEnablesMultiAgent(t *testing.T) {
	b := New(harness.Config{})
	args := b.buildArgs(harness.InvokeOptions{
		Model:   "gpt-5.3-codex-spark",
		WorkDir: "/tmp/work",
	}, "/tmp/last.txt", "do work")

	if !containsAdjacent(args, "--enable", "multi_agent") {
		t.Fatalf("args = %v, want --enable multi_agent", args)
	}
}

func TestCodexHarness_BuildResumeArgsEnablesMultiAgent(t *testing.T) {
	b := New(harness.Config{})
	args := b.buildArgs(harness.InvokeOptions{
		Continue: true,
		Model:    "gpt-5.3-codex-spark",
		WorkDir:  "/tmp/work",
	}, "/tmp/last.txt", "continue work")

	if !containsAdjacent(args, "--enable", "multi_agent") {
		t.Fatalf("args = %v, want --enable multi_agent", args)
	}
}

func TestCodexHarness_BuildArgsAddsConfiguredCompactionLimit(t *testing.T) {
	b := New(harness.Config{
		Codex: harness.CodexConfig{
			ModelAutoCompactTokenLimit: 64000,
			ToolOutputTokenLimit:       4000,
		},
	})
	args := b.buildArgs(harness.InvokeOptions{
		Model:   "gpt-5.3-codex-spark",
		WorkDir: "/tmp/work",
	}, "/tmp/last.txt", "do work")

	if !containsAdjacent(args, "-c", "model_auto_compact_token_limit=64000") {
		t.Fatalf("args = %v, want model_auto_compact_token_limit config", args)
	}
	if !containsAdjacent(args, "-c", "tool_output_token_limit=4000") {
		t.Fatalf("args = %v, want tool_output_token_limit config", args)
	}
}

func TestCodexHarness_BuildArgsAddsBypassSandboxFlag(t *testing.T) {
	b := New(harness.Config{
		Codex: harness.CodexConfig{
			BypassApprovalsAndSandbox: true,
		},
	})
	args := b.buildArgs(harness.InvokeOptions{
		Model:   "gpt-5.3-codex-spark",
		WorkDir: "/tmp/work",
	}, "/tmp/last.txt", "do work")

	if !contains(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %v, want bypass flag", args)
	}
	if contains(args, "--full-auto") {
		t.Fatalf("args = %v, did not expect --full-auto when bypassing sandbox", args)
	}
}

func TestCodexHarness_BuildArgsAddsSandboxFlag(t *testing.T) {
	b := New(harness.Config{
		Codex: harness.CodexConfig{
			Sandbox: "danger-full-access",
		},
	})
	args := b.buildArgs(harness.InvokeOptions{
		Model:   "gpt-5.3-codex-spark",
		WorkDir: "/tmp/work",
	}, "/tmp/last.txt", "do work")

	if !containsAdjacent(args, "--sandbox", "danger-full-access") {
		t.Fatalf("args = %v, want sandbox flag", args)
	}
	if !contains(args, "--full-auto") {
		t.Fatalf("args = %v, want --full-auto with explicit sandbox", args)
	}
}

func TestCodexHarness_ContextHandoffThreshold(t *testing.T) {
	b := New(harness.Config{
		Codex: harness.CodexConfig{
			ModelContextWindow:         1000,
			ContextHandoffThresholdPct: 50,
		},
	})

	percent := b.contextPercent(codexUsage{InputTokens: 450, OutputTokens: 50})
	if percent != 50 {
		t.Fatalf("percent = %v, want 50", percent)
	}
	if !b.hitContextHandoffThreshold(percent) {
		t.Fatal("expected context handoff threshold to be hit")
	}
}

func TestCodexHarness_RunDoesNotClassifySuccessfulAssistantTextAsRateLimit(t *testing.T) {
	lastMsgPath := filepath.Join(t.TempDir(), "last-message.txt")
	cmd := exec.Command(os.Args[0], "-test.run=TestCodexHarnessHelperProcess", "--")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CODEX_HELPER=1",
		"CODEX_HELPER_MODE=success-rate-limit-text",
		"CODEX_LAST_MESSAGE="+lastMsgPath,
	)

	result, err := New(harness.Config{}).run(cmd, harness.InvokeOptions{}, lastMsgPath)
	if err != nil {
		t.Fatalf("run returned error for successful assistant text: %v", err)
	}
	if !strings.Contains(result.Output, "prior harness: rate limit reached") {
		t.Fatalf("output = %q, want assistant text", result.Output)
	}
}

func TestCodexHarness_RunClassifiesStderrRateLimit(t *testing.T) {
	lastMsgPath := filepath.Join(t.TempDir(), "last-message.txt")
	cmd := exec.Command(os.Args[0], "-test.run=TestCodexHarnessHelperProcess", "--")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CODEX_HELPER=1",
		"CODEX_HELPER_MODE=stderr-rate-limit",
		"CODEX_LAST_MESSAGE="+lastMsgPath,
	)

	_, err := New(harness.Config{}).run(cmd, harness.InvokeOptions{}, lastMsgPath)
	if err != harness.ErrRateLimited {
		t.Fatalf("run error = %v, want ErrRateLimited", err)
	}
}

func TestCodexHarnessHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_HELPER") != "1" {
		return
	}
	switch os.Getenv("CODEX_HELPER_MODE") {
	case "success-rate-limit-text":
		if err := os.WriteFile(os.Getenv("CODEX_LAST_MESSAGE"), []byte("prior harness: rate limit reached; current run succeeded"), 0644); err != nil {
			panic(err)
		}
		os.Stdout.WriteString(`{"type":"session_started","session_id":"sess-test"}` + "\n")
		os.Stdout.WriteString(`{"type":"assistant_message","content":"prior harness: rate limit reached; current run succeeded"}` + "\n")
		os.Exit(0)
	case "stderr-rate-limit":
		os.Stderr.WriteString("rate limit exceeded\n")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func containsAdjacent(args []string, first, second string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}

func contains(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}
