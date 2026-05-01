package linear

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDoer maps request body substrings to canned HTTP responses. First match wins.
type stubDoer struct {
	rules    []stubRule
	Requests [][]byte
}

type stubRule struct {
	contains string
	status   int
	body     string
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	s.Requests = append(s.Requests, b)
	bodyStr := string(b)
	for _, rule := range s.rules {
		if strings.Contains(bodyStr, rule.contains) {
			return &http.Response{
				StatusCode: rule.status,
				Body:       io.NopCloser(strings.NewReader(rule.body)),
				Header:     make(http.Header),
			}, nil
		}
	}
	return &http.Response{
		StatusCode: 500,
		Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"unmatched stub"}]}`)),
		Header:     make(http.Header),
	}, nil
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(data)
}

func newStubClient(rules []stubRule) (*Client, *stubDoer) {
	stub := &stubDoer{rules: rules}
	c := New("test-token")
	c.HTTP = stub
	return c, stub
}

func TestGetIssueWithChildren_PopulatesIssueAndComments(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "REL-548", status: 200, body: fixture(t, "getIssueWithChildren_REL-548_pre.json")},
	})
	iss, err := c.GetIssueWithChildren("REL-548")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iss.Identifier != "REL-548" {
		t.Errorf("Identifier = %q, want REL-548", iss.Identifier)
	}
	if iss.State.Name != "In Progress" {
		t.Errorf("State.Name = %q, want In Progress", iss.State.Name)
	}
	if len(iss.Comments) != 1 {
		t.Fatalf("len(Comments) = %d, want 1", len(iss.Comments))
	}
	if iss.Comments[0].ID != "556160da" {
		t.Errorf("Comments[0].ID = %q, want 556160da", iss.Comments[0].ID)
	}
	if len(iss.Children) != 0 {
		t.Errorf("len(Children) = %d, want 0", len(iss.Children))
	}
}

func TestGetIssueWithChildren_PopulatesChildren(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "REL-548", status: 200, body: fixture(t, "getIssueWithChildren_REL-548_post.json")},
	})
	iss, err := c.GetIssueWithChildren("REL-548")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(iss.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(iss.Children))
	}
	if iss.Children[0].Identifier != "REL-558" {
		t.Errorf("Children[0].Identifier = %q, want REL-558", iss.Children[0].Identifier)
	}
	if iss.Children[1].Identifier != "REL-559" {
		t.Errorf("Children[1].Identifier = %q, want REL-559", iss.Children[1].Identifier)
	}
}

func TestGetIssueWithChildren_ErrorsOnHTTPFailure(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "REL-548", status: 500, body: `internal server error`},
	})
	_, err := c.GetIssueWithChildren("REL-548")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error %q does not mention http 500", err.Error())
	}
}

func TestCreateIssue_ReturnsIDAndIdentifier(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "issueCreate", status: 200, body: fixture(t, "createIssue_success.json")},
	})
	iss, err := c.CreateIssue(CreateIssueInput{
		TeamID:     "team-id",
		Title:      "QA: Test issue",
		AssigneeID: "user-id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iss.Identifier != "STUB-101" {
		t.Errorf("Identifier = %q, want STUB-101", iss.Identifier)
	}
	if iss.ID == "" {
		t.Error("ID is empty")
	}
}

func TestUpdateIssueState_SuccessReturnsNil(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "issueUpdate", status: 200, body: fixture(t, "issueUpdate_success.json")},
	})
	err := c.UpdateIssueState("issue-uuid", "new-state-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateComment_ReturnsCommentID(t *testing.T) {
	c, _ := newStubClient([]stubRule{
		{contains: "commentCreate", status: 200, body: fixture(t, "commentCreate_success.json")},
	})
	comment, err := c.CreateComment("issue-uuid", "test body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comment.ID != "stub-comment-id-0001" {
		t.Errorf("comment.ID = %q, want stub-comment-id-0001", comment.ID)
	}
}
