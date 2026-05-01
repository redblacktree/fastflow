package reconcile

import (
	"fmt"
	"strings"
	"time"

	"github.com/redblacktree/fastflow/internal/linear"
)

// Mutator is the subset of *linear.Client that Apply needs.
// Stubbable for tests without touching the network.
type Mutator interface {
	GetIssueWithChildren(identifier string) (*linear.Issue, error)
	CreateIssue(in linear.CreateIssueInput) (*linear.Issue, error)
	UpdateIssueState(issueID, stateID string) error
	CreateComment(issueID, body string) (*linear.Comment, error)
}

// RepairRecord is the audit trail written to disk after Apply (and on failure).
type RepairRecord struct {
	ParentIdentifier      string `json:"parent_identifier"`
	ParentID              string `json:"parent_id"`
	SourceCommentID       string `json:"source_comment_id"`
	QAChildID             string `json:"qa_child_id,omitempty"`
	QAChildIdentifier     string `json:"qa_child_identifier,omitempty"`
	QABoundaryCommentID   string `json:"qa_boundary_comment_id,omitempty"`
	ReviewChildID         string `json:"review_child_id,omitempty"`
	ReviewChildIdentifier string `json:"review_child_identifier,omitempty"`
	ParentStateChanged    bool   `json:"parent_state_changed"`
	RepairPointerID       string `json:"repair_pointer_id,omitempty"`
	StartedAt             string `json:"started_at"`
	CompletedAt           string `json:"completed_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	NextStep              string `json:"next_step,omitempty"`
}

// ErrAlreadyApplied is returned by Apply when the idempotence check blocks.
// The CLI can distinguish this from a plain network/API failure.
type ErrAlreadyApplied struct {
	Reason string
}

func (e *ErrAlreadyApplied) Error() string { return e.Reason }

// formatRepairPointer produces the canonical repair pointer comment body.
// The first line is the unique idempotence marker.
func formatRepairPointer(r *RepairRecord) string {
	var b strings.Builder
	b.WriteString(RepairPointerMarker + "\n")
	b.WriteString("- QA child: " + r.QAChildIdentifier + "\n")
	b.WriteString("- Review child: " + r.ReviewChildIdentifier + "\n")
	b.WriteString("- Source comment id: " + r.SourceCommentID + "\n")
	b.WriteString("- Tool: fastflow reconcile stale-handoff (REL-546)")
	return b.String()
}

// Apply executes the legal repair in the order: re-check idempotence, create QA
// child, post boundary on QA child, create Review child, move parent to In Review,
// post repair pointer on parent.
//
// After each successful step persist(record) is called so partial failures leave
// an auditable trail. On failure, the partial record is returned alongside the error.
// Apply never rolls back completed steps.
func Apply(
	m Mutator,
	parentIdentifier string,
	cfg Config,
	persist func(*RepairRecord) error,
) (*RepairRecord, error) {
	record := &RepairRecord{
		ParentIdentifier: parentIdentifier,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	// Step 1: re-fetch and re-run idempotence check.
	iss, err := m.GetIssueWithChildren(parentIdentifier)
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "re-fetch"
		_ = persist(record)
		return record, fmt.Errorf("apply: re-fetch %s: %w", parentIdentifier, err)
	}
	facts := Detect(iss)
	record.ParentID = facts.ParentID
	record.SourceCommentID = facts.HandoffCommentID

	if verdict := IdempotenceCheck(facts); verdict.Blocked {
		return record, &ErrAlreadyApplied{Reason: verdict.Reason}
	}

	// Step 2: Create QA child.
	qaIssue, err := m.CreateIssue(linear.CreateIssueInput{
		TeamID:     cfg.TeamID,
		Title:      childTitleQAPrefix + facts.ParentTitle,
		AssigneeID: cfg.QAAssigneeID,
		ParentID:   facts.ParentID,
	})
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "create-qa-child"
		_ = persist(record)
		return record, fmt.Errorf("apply: create QA child: %w", err)
	}
	record.QAChildID = qaIssue.ID
	record.QAChildIdentifier = qaIssue.Identifier
	record.NextStep = "post-qa-boundary"
	_ = persist(record)

	// Step 3: Post boundary comment on QA child.
	qaComment, err := m.CreateComment(qaIssue.ID, facts.HandoffCommentBody)
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "post-qa-boundary"
		_ = persist(record)
		return record, fmt.Errorf("apply: post QA boundary comment: %w", err)
	}
	record.QABoundaryCommentID = qaComment.ID
	record.NextStep = "create-review-child"
	_ = persist(record)

	// Step 4: Create Review child.
	reviewIssue, err := m.CreateIssue(linear.CreateIssueInput{
		TeamID:     cfg.TeamID,
		Title:      childTitleReviewPrefix + facts.ParentTitle,
		AssigneeID: cfg.ReviewAssigneeID,
		ParentID:   facts.ParentID,
	})
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "create-review-child"
		_ = persist(record)
		return record, fmt.Errorf("apply: create Review child: %w", err)
	}
	record.ReviewChildID = reviewIssue.ID
	record.ReviewChildIdentifier = reviewIssue.Identifier
	record.NextStep = "move-parent-to-in-review"
	_ = persist(record)

	// Step 5: Move parent to In Review.
	if err := m.UpdateIssueState(facts.ParentID, cfg.InReviewStateID); err != nil {
		record.LastError = err.Error()
		record.NextStep = "move-parent-to-in-review"
		_ = persist(record)
		return record, fmt.Errorf("apply: move parent to In Review: %w", err)
	}
	record.ParentStateChanged = true
	record.NextStep = "post-repair-pointer"
	_ = persist(record)

	// Step 6: Post repair pointer on parent.
	pointerBody := formatRepairPointer(record)
	pointer, err := m.CreateComment(facts.ParentID, pointerBody)
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "post-repair-pointer"
		_ = persist(record)
		return record, fmt.Errorf("apply: post repair pointer: %w", err)
	}
	record.RepairPointerID = pointer.ID
	record.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	record.NextStep = ""
	record.LastError = ""
	_ = persist(record)

	return record, nil
}
