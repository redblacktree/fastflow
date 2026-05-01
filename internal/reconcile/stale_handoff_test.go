package reconcile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/linear"
)

// issueFixture mirrors the flat fixture format stored in testdata/.
type issueFixture struct {
	ID         string               `json:"id"`
	Identifier string               `json:"identifier"`
	Title      string               `json:"title"`
	URL        string               `json:"url"`
	State      linear.WorkflowState `json:"state"`
	Children   []issueFixture       `json:"children"`
	Comments   []linear.Comment     `json:"comments"`
}

func fixtureToIssue(f issueFixture) linear.Issue {
	iss := linear.Issue{
		ID:         f.ID,
		Identifier: f.Identifier,
		Title:      f.Title,
		URL:        f.URL,
		State:      f.State,
		Comments:   f.Comments,
	}
	for _, child := range f.Children {
		c := fixtureToIssue(child)
		iss.Children = append(iss.Children, c)
	}
	return iss
}

func loadIssueFixture(t *testing.T, name string) *linear.Issue {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("loadIssueFixture %s: %v", name, err)
	}
	var f issueFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("loadIssueFixture %s: unmarshal: %v", name, err)
	}
	iss := fixtureToIssue(f)
	return &iss
}

var testCfg = Config{
	TeamID:            "test-team-id",
	InReviewStateID:   "state-in-review-id",
	InReviewStateName: "In Review",
	QAAssigneeID:      "test-qa-assignee-id",
	ReviewAssigneeID:  "test-review-assignee-id",
}

func TestDetect_REL548_pre_capturesHandoffCommentID(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-548_pre.json")
	facts := Detect(iss)
	if facts.HandoffCommentID != "556160da" {
		t.Errorf("HandoffCommentID = %q, want 556160da", facts.HandoffCommentID)
	}
	if facts.HasQAChild {
		t.Error("HasQAChild = true, want false")
	}
	if facts.HasReviewChild {
		t.Error("HasReviewChild = true, want false")
	}
	if facts.DerivedPRURL != "https://github.com/redblacktree/fastflow/pull/36" {
		t.Errorf("DerivedPRURL = %q", facts.DerivedPRURL)
	}
	if facts.DerivedBranch != "REL-548" {
		t.Errorf("DerivedBranch = %q, want REL-548", facts.DerivedBranch)
	}
}

func TestDetect_REL549_pre_capturesHandoffCommentID(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-549_pre.json")
	facts := Detect(iss)
	if facts.HandoffCommentID != "e0a0b191" {
		t.Errorf("HandoffCommentID = %q, want e0a0b191", facts.HandoffCommentID)
	}
	if facts.HasQAChild || facts.HasReviewChild {
		t.Error("pre-repair fixture should have no QA/Review children")
	}
}

func TestDetect_post_repair_setsHasQAandReview(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-548_post.json")
	facts := Detect(iss)
	if !facts.HasQAChild {
		t.Error("HasQAChild = false, want true")
	}
	if !facts.HasReviewChild {
		t.Error("HasReviewChild = false, want true")
	}
	if facts.QAChildIdentifier != "REL-558" {
		t.Errorf("QAChildIdentifier = %q, want REL-558", facts.QAChildIdentifier)
	}
	if facts.ReviewChildIdentifier != "REL-559" {
		t.Errorf("ReviewChildIdentifier = %q, want REL-559", facts.ReviewChildIdentifier)
	}
	if !facts.HasRepairPointer {
		t.Error("HasRepairPointer = false, want true")
	}
}

func TestIdempotenceCheck_pre_repair_unblocked(t *testing.T) {
	for _, name := range []string{"issue_REL-548_pre.json", "issue_REL-549_pre.json"} {
		t.Run(name, func(t *testing.T) {
			iss := loadIssueFixture(t, name)
			facts := Detect(iss)
			verdict := IdempotenceCheck(facts)
			if verdict.Blocked {
				t.Errorf("Blocked = true, want false; reason: %s", verdict.Reason)
			}
		})
	}
}

func TestIdempotenceCheck_post_repair_blocked_with_specific_reason(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-548_post.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	// Repair pointer is found first in precedence order.
	if !strings.Contains(verdict.Reason, "repair-ptr-548") {
		t.Errorf("Reason %q does not mention repair-ptr-548", verdict.Reason)
	}
}

func TestIdempotenceCheck_no_handoff_blocked(t *testing.T) {
	iss := loadIssueFixture(t, "issue_no_handoff.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if !strings.HasPrefix(verdict.Reason, "no Q HANDOFF") {
		t.Errorf("Reason %q does not start with 'no Q HANDOFF'", verdict.Reason)
	}
}

func TestIdempotenceCheck_done_parent_blocked(t *testing.T) {
	iss := loadIssueFixture(t, "issue_done_parent.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if !strings.Contains(verdict.Reason, "Done") {
		t.Errorf("Reason %q does not mention Done", verdict.Reason)
	}
}

func TestIdempotenceCheck_only_qa_missing_blocked(t *testing.T) {
	// Review child present, QA missing → HasReviewChild=true → "only QA lane missing"
	iss := loadIssueFixture(t, "issue_only_qa_missing.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if !strings.Contains(verdict.Reason, "only QA lane missing") {
		t.Errorf("Reason %q does not mention 'only QA lane missing'", verdict.Reason)
	}
}

func TestIdempotenceCheck_only_review_missing_blocked(t *testing.T) {
	// QA child present, Review missing → HasQAChild=true → "only Review lane missing"
	iss := loadIssueFixture(t, "issue_only_review_missing.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if !strings.Contains(verdict.Reason, "only Review lane missing") {
		t.Errorf("Reason %q does not mention 'only Review lane missing'", verdict.Reason)
	}
}

func TestIdempotenceCheck_already_repaired_with_pointer_blocked(t *testing.T) {
	iss := loadIssueFixture(t, "issue_already_repaired_with_pointer.json")
	facts := Detect(iss)
	verdict := IdempotenceCheck(facts)
	if !verdict.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if !strings.Contains(verdict.Reason, "repair-ptr-605") {
		t.Errorf("Reason %q does not mention repair pointer comment id", verdict.Reason)
	}
}

func TestPropose_pre_repair_REL548_emits_QA_style_intent(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-548_pre.json")
	facts := Detect(iss)
	proposal, verdict := Propose(facts, testCfg)
	if verdict.Blocked {
		t.Fatalf("Propose returned blocked: %s", verdict.Reason)
	}
	if proposal == nil {
		t.Fatal("proposal is nil")
	}
	wantQATitle := "QA: Add --on-complete hook support for pipeline runs"
	if proposal.IntendedQATitle != wantQATitle {
		t.Errorf("IntendedQATitle = %q, want %q", proposal.IntendedQATitle, wantQATitle)
	}
	if proposal.IntendedQAAssigneeID != testCfg.QAAssigneeID {
		t.Errorf("IntendedQAAssigneeID = %q", proposal.IntendedQAAssigneeID)
	}
	if proposal.IntendedReviewAssigneeID != testCfg.ReviewAssigneeID {
		t.Errorf("IntendedReviewAssigneeID = %q", proposal.IntendedReviewAssigneeID)
	}
	if len(proposal.MissingLanes) != 2 {
		t.Errorf("MissingLanes = %v, want [QA Review]", proposal.MissingLanes)
	}
	if !strings.Contains(proposal.Boundary, "Q HANDOFF") {
		t.Errorf("Boundary %q missing Q HANDOFF", proposal.Boundary)
	}
	if proposal.SourceCommentID != "556160da" {
		t.Errorf("SourceCommentID = %q, want 556160da", proposal.SourceCommentID)
	}
}

func TestPropose_pre_repair_REL549_emits_REL560_style_intent(t *testing.T) {
	iss := loadIssueFixture(t, "issue_REL-549_pre.json")
	facts := Detect(iss)
	proposal, verdict := Propose(facts, testCfg)
	if verdict.Blocked {
		t.Fatalf("Propose returned blocked: %s", verdict.Reason)
	}
	if proposal.SourceCommentID != "e0a0b191" {
		t.Errorf("SourceCommentID = %q, want e0a0b191", proposal.SourceCommentID)
	}
	wantQATitle := "QA: Implement fastflow monitor web dashboard"
	if proposal.IntendedQATitle != wantQATitle {
		t.Errorf("IntendedQATitle = %q, want %q", proposal.IntendedQATitle, wantQATitle)
	}
}

func TestPropose_post_repair_returns_nil_proposal_with_blocked_verdict(t *testing.T) {
	for _, name := range []string{"issue_REL-548_post.json", "issue_REL-549_post.json"} {
		t.Run(name, func(t *testing.T) {
			iss := loadIssueFixture(t, name)
			facts := Detect(iss)
			proposal, verdict := Propose(facts, testCfg)
			if !verdict.Blocked {
				t.Error("verdict not blocked for post-repair fixture")
			}
			if proposal != nil {
				t.Error("proposal is non-nil for post-repair fixture")
			}
		})
	}
}
