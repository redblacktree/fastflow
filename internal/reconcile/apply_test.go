package reconcile

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/linear"
)

// recordingMutator implements Mutator using fixture issues for reads and
// records all mutation calls for inspection.
type recordingMutator struct {
	issue       *linear.Issue // returned by GetIssueWithChildren
	calls       []string      // method names in order
	createCount int           // tracks CreateIssue invocation count
	failOn      string        // if set, fail when this method is called (nth-specific: "CreateIssue2")
	failErr     error
}

func (m *recordingMutator) GetIssueWithChildren(id string) (*linear.Issue, error) {
	m.calls = append(m.calls, "GetIssueWithChildren")
	if m.issue == nil {
		return nil, errors.New("no fixture issue set")
	}
	return m.issue, nil
}

func (m *recordingMutator) CreateIssue(in linear.CreateIssueInput) (*linear.Issue, error) {
	m.createCount++
	callName := "CreateIssue"
	nthName := "CreateIssue" + string(rune('0'+m.createCount))
	m.calls = append(m.calls, callName)
	if m.failOn == callName || m.failOn == nthName {
		return nil, m.failErr
	}
	var id, identifier string
	if strings.HasPrefix(in.Title, "QA:") {
		id = "stub-uuid-qa"
		identifier = "STUB-101"
	} else {
		id = "stub-uuid-review"
		identifier = "STUB-102"
	}
	return &linear.Issue{ID: id, Identifier: identifier, Title: in.Title}, nil
}

func (m *recordingMutator) UpdateIssueState(issueID, stateID string) error {
	m.calls = append(m.calls, "UpdateIssueState")
	if m.failOn == "UpdateIssueState" {
		return m.failErr
	}
	return nil
}

func (m *recordingMutator) CreateComment(issueID, body string) (*linear.Comment, error) {
	m.calls = append(m.calls, "CreateComment")
	if m.failOn == "CreateComment" {
		return nil, m.failErr
	}
	id := "stub-comment-" + issueID
	return &linear.Comment{ID: id, Body: body}, nil
}

var noPersist = func(_ *RepairRecord) error { return nil }

func applyWithPre548(t *testing.T, mut *recordingMutator) (*RepairRecord, error) {
	t.Helper()
	if mut.issue == nil {
		mut.issue = loadIssueFixture(t, "issue_REL-548_pre.json")
	}
	return Apply(mut, "REL-548", testCfg, noPersist)
}

func TestApply_REL548_pre_executesAllSixSteps_inOrder(t *testing.T) {
	mut := &recordingMutator{}
	record, err := applyWithPre548(t, mut)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	want := []string{
		"GetIssueWithChildren",
		"CreateIssue",   // QA child
		"CreateComment", // boundary on QA
		"CreateIssue",   // Review child
		"UpdateIssueState",
		"CreateComment", // repair pointer on parent
	}
	if len(mut.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", mut.calls, want)
	}
	for i, w := range want {
		if mut.calls[i] != w {
			t.Errorf("calls[%d] = %q, want %q", i, mut.calls[i], w)
		}
	}
	if record.QAChildIdentifier != "STUB-101" {
		t.Errorf("QAChildIdentifier = %q", record.QAChildIdentifier)
	}
	if record.ReviewChildIdentifier != "STUB-102" {
		t.Errorf("ReviewChildIdentifier = %q", record.ReviewChildIdentifier)
	}
	if !record.ParentStateChanged {
		t.Error("ParentStateChanged = false")
	}
	if record.RepairPointerID == "" {
		t.Error("RepairPointerID is empty")
	}
	if record.CompletedAt == "" {
		t.Error("CompletedAt is empty")
	}
}

func TestApply_REL549_pre_executesAllSixSteps(t *testing.T) {
	mut := &recordingMutator{issue: loadIssueFixture(t, "issue_REL-549_pre.json")}
	record, err := Apply(mut, "REL-549", testCfg, noPersist)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if len(mut.calls) != 6 {
		t.Errorf("len(calls) = %d, want 6: %v", len(mut.calls), mut.calls)
	}
	if record.SourceCommentID != "e0a0b191" {
		t.Errorf("SourceCommentID = %q, want e0a0b191", record.SourceCommentID)
	}
}

func TestApply_post_repair_returns_ErrAlreadyApplied_no_writes(t *testing.T) {
	mut := &recordingMutator{issue: loadIssueFixture(t, "issue_REL-548_post.json")}
	_, err := Apply(mut, "REL-548", testCfg, noPersist)
	var alreadyApplied *ErrAlreadyApplied
	if !errors.As(err, &alreadyApplied) {
		t.Fatalf("expected ErrAlreadyApplied, got %v", err)
	}
	// Only GetIssueWithChildren should have been called — no writes.
	if len(mut.calls) != 1 || mut.calls[0] != "GetIssueWithChildren" {
		t.Errorf("unexpected calls: %v", mut.calls)
	}
}

func TestApply_failsAtStep4_writesPartialRecord_with_NextStep(t *testing.T) {
	var persisted []*RepairRecord
	persist := func(r *RepairRecord) error {
		cp := *r
		persisted = append(persisted, &cp)
		return nil
	}

	mut := &recordingMutator{
		issue:   loadIssueFixture(t, "issue_REL-548_pre.json"),
		failOn:  "CreateIssue2", // fail on second CreateIssue (Review child)
		failErr: errors.New("linear: create review child failed"),
	}
	record, err := Apply(mut, "REL-548", testCfg, persist)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Review child") {
		t.Errorf("error %q does not mention Review child", err.Error())
	}
	if record.QAChildID == "" {
		t.Error("QAChildID should be set after step 2 succeeded")
	}
	if record.ReviewChildID != "" {
		t.Error("ReviewChildID should be empty after step 4 failed")
	}
	// Check the last persisted record has the NextStep hint.
	if len(persisted) == 0 {
		t.Fatal("no persisted records")
	}
	last := persisted[len(persisted)-1]
	if last.NextStep != "create-review-child" {
		t.Errorf("NextStep = %q, want create-review-child", last.NextStep)
	}
}

func TestApply_persistFailureAfterCreateQAChild_returnsMutationState(t *testing.T) {
	persist := func(r *RepairRecord) error {
		if r.QAChildID != "" {
			return errors.New("disk full")
		}
		return nil
	}

	mut := &recordingMutator{issue: loadIssueFixture(t, "issue_REL-548_pre.json")}
	record, err := Apply(mut, "REL-548", testCfg, persist)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "persist repair record after create QA child") {
		t.Fatalf("error %q does not mention create QA child persist failure", err.Error())
	}
	if !strings.Contains(err.Error(), "next_step=post-qa-boundary") {
		t.Fatalf("error %q does not mention next_step=post-qa-boundary", err.Error())
	}
	if record.QAChildID == "" {
		t.Fatal("QAChildID should be set after successful QA creation")
	}
	if len(mut.calls) != 2 {
		t.Fatalf("calls = %v, want [GetIssueWithChildren CreateIssue]", mut.calls)
	}
}

func TestApply_persistFailureAfterPostRepairPointer_returnsErrorNotSuccess(t *testing.T) {
	persistCalls := 0
	persist := func(_ *RepairRecord) error {
		persistCalls++
		if persistCalls == 5 {
			return fmt.Errorf("write audit record")
		}
		return nil
	}

	mut := &recordingMutator{issue: loadIssueFixture(t, "issue_REL-548_pre.json")}
	record, err := Apply(mut, "REL-548", testCfg, persist)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "persist repair record after post repair pointer") {
		t.Fatalf("error %q does not mention post repair pointer persist failure", err.Error())
	}
	if record.RepairPointerID == "" {
		t.Fatal("RepairPointerID should be set after successful pointer creation")
	}
	if record.CompletedAt == "" {
		t.Fatal("CompletedAt should be set before final persist")
	}
	if len(mut.calls) != 6 {
		t.Fatalf("calls = %v, want 6 operations through final mutation", mut.calls)
	}
}

func TestApply_resumeAfterPartial_isIdempotent(t *testing.T) {
	// Simulate: QA child exists already (partial apply), Review child does not.
	// The idempotence check should return ErrAlreadyApplied because only one lane is present.
	mut := &recordingMutator{issue: loadIssueFixture(t, "issue_only_review_missing.json")}
	_, err := Apply(mut, "REL-603", testCfg, noPersist)
	var alreadyApplied *ErrAlreadyApplied
	if !errors.As(err, &alreadyApplied) {
		t.Fatalf("expected ErrAlreadyApplied, got %v", err)
	}
	if !strings.Contains(alreadyApplied.Reason, "only Review lane missing") {
		t.Errorf("reason %q does not mention 'only Review lane missing'", alreadyApplied.Reason)
	}
}

func TestFormatRepairPointer_pinsCanonicalText(t *testing.T) {
	r := &RepairRecord{
		QAChildIdentifier:     "REL-558",
		ReviewChildIdentifier: "REL-559",
		SourceCommentID:       "556160da",
	}
	got := formatRepairPointer(r)
	want := "[fastflow-reconcile] stale-handoff repair applied\n" +
		"- QA child: REL-558\n" +
		"- Review child: REL-559\n" +
		"- Source comment id: 556160da\n" +
		"- Tool: fastflow reconcile stale-handoff (REL-546)"
	if got != want {
		t.Errorf("formatRepairPointer mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
