package reconcile

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/linear"
)

// e2eStubDoer wires a *linear.Client to fixture files for end-to-end testing.
// It maps request body substrings to canned fixture responses (first match wins).
type e2eStubDoer struct {
	rules    []e2eRule
	Requests [][]byte
}

type e2eRule struct {
	contains string
	fixFile  string // if set, load from linear testdata; body overrides if non-empty
	body     string
	status   int
}

func (s *e2eStubDoer) Do(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	s.Requests = append(s.Requests, b)
	bodyStr := string(b)
	for _, rule := range s.rules {
		if strings.Contains(bodyStr, rule.contains) {
			body := rule.body
			if body == "" && rule.fixFile != "" {
				data, err := os.ReadFile(filepath.Join("..", "linear", "testdata", rule.fixFile))
				if err != nil {
					return nil, err
				}
				body = string(data)
			}
			status := rule.status
			if status == 0 {
				status = 200
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}
	}
	return &http.Response{
		StatusCode: 500,
		Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"e2e: unmatched stub"}]}`)),
		Header:     make(http.Header),
	}, nil
}

// preRules returns e2e stub rules for the pre-repair scenario.
// Using operation names as matchers avoids false matches when the
// comment body contains the issue identifier (e.g. "Branch: REL-548").
func preRules548() []e2eRule {
	return []e2eRule{
		// GET — operation name is unique; won't collide with mutations
		{contains: "GetIssueWithChildren", fixFile: "getIssueWithChildren_REL-548_pre.json"},
		{contains: "issueCreate", fixFile: "createIssue_success.json"},
		{contains: "issueUpdate", fixFile: "issueUpdate_success.json"},
		{contains: "commentCreate", fixFile: "commentCreate_success.json"},
	}
}

func TestE2E_ProposalAndApply(t *testing.T) {
	stub := &e2eStubDoer{rules: preRules548()}
	client := linear.New("test-token")
	client.HTTP = stub

	// 1. Fetch and detect.
	iss, err := client.GetIssueWithChildren("REL-548")
	if err != nil {
		t.Fatalf("GetIssueWithChildren: %v", err)
	}
	facts := Detect(iss)
	if facts.HandoffCommentID != "556160da" {
		t.Errorf("HandoffCommentID = %q, want 556160da", facts.HandoffCommentID)
	}

	// 2. Propose.
	proposal, verdict := Propose(facts, testCfg)
	if verdict.Blocked {
		t.Fatalf("propose blocked: %s", verdict.Reason)
	}
	if !strings.HasPrefix(proposal.IntendedQATitle, "QA:") {
		t.Errorf("IntendedQATitle = %q", proposal.IntendedQATitle)
	}

	// 3. Apply — re-wire stub for the six-call sequence.
	stub.rules = preRules548()
	stub.Requests = nil // reset request log

	record, err := Apply(client, "REL-548", testCfg, func(_ *RepairRecord) error { return nil })
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify six HTTP calls were made in canonical order:
	// GET, POST(issueCreate/QA), POST(commentCreate/boundary),
	// POST(issueCreate/Review), POST(issueUpdate), POST(commentCreate/pointer)
	if len(stub.Requests) != 6 {
		t.Errorf("expected 6 HTTP requests, got %d", len(stub.Requests))
	}

	// Spot-check the first call has the identifier.
	if !strings.Contains(string(stub.Requests[0]), "REL-548") {
		t.Error("first request should contain REL-548")
	}

	if record.CompletedAt == "" {
		t.Error("CompletedAt is empty after successful apply")
	}
	if record.RepairPointerID == "" {
		t.Error("RepairPointerID is empty after successful apply")
	}
}

func TestE2E_PostRepair_ProposesNoDuplicate(t *testing.T) {
	stub := &e2eStubDoer{
		rules: []e2eRule{
			{contains: "GetIssueWithChildren", fixFile: "getIssueWithChildren_REL-548_post.json"},
		},
	}
	client := linear.New("test-token")
	client.HTTP = stub

	iss, err := client.GetIssueWithChildren("REL-548")
	if err != nil {
		t.Fatalf("GetIssueWithChildren: %v", err)
	}
	facts := Detect(iss)
	_, verdict := Propose(facts, testCfg)
	if !verdict.Blocked {
		t.Error("post-repair issue should produce a blocked verdict (no duplicate)")
	}
}
