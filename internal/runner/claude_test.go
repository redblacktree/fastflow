package runner

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestClaudeJSONResultParsing(t *testing.T) {
	tests := []struct {
		name          string
		jsonStr       string
		wantSubtype   string
		wantBudgetCap bool
		wantMaxTurns  bool
		wantResult    string
		wantSessionID string
		wantCost      float64
	}{
		{
			name:          "success",
			jsonStr:       `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"abc","total_cost_usd":0.50}`,
			wantSubtype:   "success",
			wantBudgetCap: false,
			wantMaxTurns:  false,
			wantResult:    "done",
			wantSessionID: "abc",
			wantCost:      0.50,
		},
		{
			name:          "budget exhausted",
			jsonStr:       `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"session_id":"def","total_cost_usd":5.01}`,
			wantSubtype:   "error_max_budget_usd",
			wantBudgetCap: true,
			wantMaxTurns:  false,
			wantResult:    "",
			wantSessionID: "def",
			wantCost:      5.01,
		},
		{
			name:          "max turns",
			jsonStr:       `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"partial","session_id":"ghi","total_cost_usd":2.00}`,
			wantSubtype:   "error_max_turns",
			wantBudgetCap: false,
			wantMaxTurns:  true,
			wantResult:    "partial",
			wantSessionID: "ghi",
			wantCost:      2.00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result claudeJSONResult
			if err := json.Unmarshal([]byte(tt.jsonStr), &result); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if result.Subtype != tt.wantSubtype {
				t.Errorf("Subtype = %q, want %q", result.Subtype, tt.wantSubtype)
			}
			if (result.Subtype == subtypeBudgetExhausted) != tt.wantBudgetCap {
				t.Errorf("budget cap detection = %v, want %v", result.Subtype == subtypeBudgetExhausted, tt.wantBudgetCap)
			}
			if (result.Subtype == subtypeMaxTurns) != tt.wantMaxTurns {
				t.Errorf("max turns detection = %v, want %v", result.Subtype == subtypeMaxTurns, tt.wantMaxTurns)
			}
			if result.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", result.Result, tt.wantResult)
			}
			if result.SessionID != tt.wantSessionID {
				t.Errorf("SessionID = %q, want %q", result.SessionID, tt.wantSessionID)
			}
			if result.TotalCostUsd != tt.wantCost {
				t.Errorf("TotalCostUsd = %v, want %v", result.TotalCostUsd, tt.wantCost)
			}
		})
	}
}

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
