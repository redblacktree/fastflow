package judge

import (
	"os"
	"strings"
	"testing"
)

func TestIsInvalidAPIKey(t *testing.T) {
	if !isInvalidAPIKey(`authentication_error`) {
		t.Error("should detect 'authentication_error'")
	}
	if !isInvalidAPIKey(`invalid x-api-key`) {
		t.Error("should detect 'invalid x-api-key'")
	}
	if isInvalidAPIKey("Normal judge output YES: looks good") {
		t.Error("should not trigger on normal output")
	}
}

func TestEnvWithoutAPIKey(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	filtered := envWithoutAPIKey()
	for _, env := range filtered {
		if strings.HasPrefix(env, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY should be filtered out")
		}
	}
}
