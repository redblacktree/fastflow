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

func persistFailure(record *RepairRecord, completedStep string, err error) error {
	nextStep := record.NextStep
	if nextStep == "" {
		nextStep = "inspect-completed-state"
	}
	return fmt.Errorf(
		"apply: persist repair record after %s: %w (next_step=%s; recover by persisting the returned repair record and inspecting completed Linear mutations before any further --apply)",
		completedStep,
		err,
		nextStep,
	)
}

func persistRecord(persist func(*RepairRecord) error, record *RepairRecord, completedStep string) error {
	if err := persist(record); err != nil {
		return persistFailure(record, completedStep, err)
	}
	return nil
}

// Apply executes the legal repair in the order: re-check idempotence, create QA
// child, post boundary on QA child, create Review child, move parent to In Review,
// post repair pointer on parent.
//
// After each successful step persist(record) is called so partial failures leave
// an auditable trail. Persist failures are fatal and are surfaced explicitly with
// the completed mutation state and recovery guidance. On failure, the partial
// record is returned alongside the error. Apply never rolls back completed steps.
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
		if perr := persistRecord(persist, record, "recording re-fetch failure"); perr != nil {
			return record, fmt.Errorf("apply: re-fetch %s: %v; additionally %w", parentIdentifier, err, perr)
		}
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
		if perr := persistRecord(persist, record, "recording create QA child failure"); perr != nil {
			return record, fmt.Errorf("apply: create QA child: %v; additionally %w", err, perr)
		}
		return record, fmt.Errorf("apply: create QA child: %w", err)
	}
	record.QAChildID = qaIssue.ID
	record.QAChildIdentifier = qaIssue.Identifier
	record.NextStep = "post-qa-boundary"
	record.LastError = ""
	if err := persistRecord(persist, record, "create QA child"); err != nil {
		return record, err
	}

	// Step 3: Post boundary comment on QA child.
	qaComment, err := m.CreateComment(qaIssue.ID, facts.HandoffCommentBody)
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "post-qa-boundary"
		if perr := persistRecord(persist, record, "recording post QA boundary failure"); perr != nil {
			return record, fmt.Errorf("apply: post QA boundary comment: %v; additionally %w", err, perr)
		}
		return record, fmt.Errorf("apply: post QA boundary comment: %w", err)
	}
	record.QABoundaryCommentID = qaComment.ID
	record.NextStep = "create-review-child"
	record.LastError = ""
	if err := persistRecord(persist, record, "post QA boundary comment"); err != nil {
		return record, err
	}

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
		if perr := persistRecord(persist, record, "recording create Review child failure"); perr != nil {
			return record, fmt.Errorf("apply: create Review child: %v; additionally %w", err, perr)
		}
		return record, fmt.Errorf("apply: create Review child: %w", err)
	}
	record.ReviewChildID = reviewIssue.ID
	record.ReviewChildIdentifier = reviewIssue.Identifier
	record.NextStep = "move-parent-to-in-review"
	record.LastError = ""
	if err := persistRecord(persist, record, "create Review child"); err != nil {
		return record, err
	}

	// Step 5: Move parent to In Review.
	if err := m.UpdateIssueState(facts.ParentID, cfg.InReviewStateID); err != nil {
		record.LastError = err.Error()
		record.NextStep = "move-parent-to-in-review"
		if perr := persistRecord(persist, record, "recording move parent to In Review failure"); perr != nil {
			return record, fmt.Errorf("apply: move parent to In Review: %v; additionally %w", err, perr)
		}
		return record, fmt.Errorf("apply: move parent to In Review: %w", err)
	}
	record.ParentStateChanged = true
	record.NextStep = "post-repair-pointer"
	record.LastError = ""
	if err := persistRecord(persist, record, "move parent to In Review"); err != nil {
		return record, err
	}

	// Step 6: Post repair pointer on parent.
	pointerBody := formatRepairPointer(record)
	pointer, err := m.CreateComment(facts.ParentID, pointerBody)
	if err != nil {
		record.LastError = err.Error()
		record.NextStep = "post-repair-pointer"
		if perr := persistRecord(persist, record, "recording post repair pointer failure"); perr != nil {
			return record, fmt.Errorf("apply: post repair pointer: %v; additionally %w", err, perr)
		}
		return record, fmt.Errorf("apply: post repair pointer: %w", err)
	}
	record.RepairPointerID = pointer.ID
	record.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	record.NextStep = ""
	record.LastError = ""
	if err := persistRecord(persist, record, "post repair pointer"); err != nil {
		return record, err
	}

	return record, nil
}
