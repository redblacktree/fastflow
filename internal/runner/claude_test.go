package runner

import (
	"os"
	"strings"
	"testing"
)

func TestIsInvalidAPIKey(t *testing.T) {
	// Should detect API key rejection from JSON error body
	if !isInvalidAPIKey(`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`) {
		t.Error("should detect 'invalid x-api-key'")
	}

	// Should detect generic authentication_error
	if !isInvalidAPIKey(`{"type":"error","error":{"type":"authentication_error","message":"some other message"}}`) {
		t.Error("should detect 'authentication_error'")
	}

	// Should detect plain "Invalid API key" text
	if !isInvalidAPIKey("Invalid API key") {
		t.Error("should detect 'Invalid API key'")
	}

	// Should NOT trigger on normal output
	if isInvalidAPIKey("Hello, I'm Claude. How can I help you?") {
		t.Error("should not trigger on normal output")
	}

	// Should NOT trigger on "Not logged in" (different error type)
	if isInvalidAPIKey("Not logged in. Please run /login") {
		t.Error("should not trigger on 'not logged in'")
	}
}

func TestIsNotLoggedIn(t *testing.T) {
	if !isNotLoggedIn("Not logged in") {
		t.Error("should detect 'Not logged in'")
	}
	if !isNotLoggedIn("Please run /login") {
		t.Error("should detect 'Please run /login'")
	}
	if isNotLoggedIn("Hello world") {
		t.Error("should not trigger on normal output")
	}
}

func TestIsRateLimited(t *testing.T) {
	if !isRateLimited("You've hit your limit for today") {
		t.Error("should detect \"You've hit your limit\"")
	}
	if !isRateLimited("rate limit exceeded, please slow down") {
		t.Error("should detect \"rate limit\"")
	}
	// Case-insensitive
	if !isRateLimited("Rate Limit Exceeded") {
		t.Error("should detect \"Rate Limit\" case-insensitively")
	}
	// Should NOT trigger on normal output
	if isRateLimited("Hello, I'm Claude. How can I help you?") {
		t.Error("should not trigger on normal output")
	}
}

func TestIsMaxTurnsError(t *testing.T) {
	if !isMaxTurnsError("Reached max turns") {
		t.Error("should detect \"Reached max turns\"")
	}
	if !isMaxTurnsError("Error: Reached max turns (50)") {
		t.Error("should detect \"Error: Reached max turns\"")
	}
	if isMaxTurnsError("Task completed successfully") {
		t.Error("should not trigger on normal output")
	}
}

func TestEnvWithoutAPIKey(t *testing.T) {
	// Set a test API key
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	filtered := envWithoutAPIKey()

	for _, env := range filtered {
		if strings.HasPrefix(env, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY should be filtered out")
		}
	}

	// Verify other env vars are preserved
	os.Setenv("OTHER_VAR", "preserved")
	defer os.Unsetenv("OTHER_VAR")

	filtered = envWithoutAPIKey()
	found := false
	for _, env := range filtered {
		if env == "OTHER_VAR=preserved" {
			found = true
			break
		}
	}
	if !found {
		t.Error("other env vars should be preserved")
	}
}
