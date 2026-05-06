package reconcile

import (
	"regexp"
	"strings"

	"github.com/redblacktree/fastflow/internal/linear"
)

// RepairPointerMarker is the first line of the repair pointer comment and the
// unique idempotence marker.
const RepairPointerMarker = "[fastflow-reconcile] stale-handoff repair applied"

var (
	qHandoffMarkerHandoff  = "Q HANDOFF"
	qHandoffMarkerRetest   = "RETEST READY"
	childTitleQAPrefix     = "QA: "
	childTitleReviewPrefix = "Review: "

	parentTerminalStateNames = map[string]bool{
		"Done":      true,
		"Canceled":  true,
		"Duplicate": true,
	}

	prURLRegex  = regexp.MustCompile(`https?://(?:www\.)?github\.com/[^\s)]+/pull/\d+`)
	branchRegex = regexp.MustCompile(`(?mi)^\s*(?:[-*•]\s*)?branch\s*[:=]\s*(\S+)`)
)

// StaleHandoffFacts is what Detect produces — all evidence used by Propose,
// IdempotenceCheck, and Apply. Fully serializable for audit.
type StaleHandoffFacts struct {
	ParentID               string `json:"parent_id"`
	ParentIdentifier       string `json:"parent_identifier"`
	ParentTitle            string `json:"parent_title"`
	ParentStateName        string `json:"parent_state_name"`
	ParentStateID          string `json:"parent_state_id"`
	HandoffCommentID       string `json:"handoff_comment_id"`
	HandoffCommentBody     string `json:"handoff_comment_body"`
	HasQAChild             bool   `json:"has_qa_child"`
	HasReviewChild         bool   `json:"has_review_child"`
	QAChildIdentifier      string `json:"qa_child_identifier,omitempty"`
	ReviewChildIdentifier  string `json:"review_child_identifier,omitempty"`
	HasRepairPointer       bool   `json:"has_repair_pointer"`
	RepairPointerCommentID string `json:"repair_pointer_comment_id,omitempty"`
	DerivedPRURL           string `json:"derived_pr_url,omitempty"`
	DerivedBranch          string `json:"derived_branch,omitempty"`
}

// Detect inspects an already-fetched issue and returns the facts needed by
// proposal/idempotence/apply. Pure function, no I/O.
func Detect(issue *linear.Issue) *StaleHandoffFacts {
	facts := &StaleHandoffFacts{
		ParentID:         issue.ID,
		ParentIdentifier: issue.Identifier,
		ParentTitle:      issue.Title,
		ParentStateName:  issue.State.Name,
		ParentStateID:    issue.State.ID,
	}

	// Scan comments: find the first handoff comment and any repair pointer.
	for _, c := range issue.Comments {
		trimmed := strings.TrimSpace(c.Body)

		if facts.HandoffCommentID == "" {
			if strings.Contains(trimmed, qHandoffMarkerHandoff) ||
				strings.Contains(trimmed, qHandoffMarkerRetest) {
				facts.HandoffCommentID = c.ID
				facts.HandoffCommentBody = trimmed
			}
		}

		// The first line of the repair pointer is the unique marker.
		firstLine := trimmed
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			firstLine = trimmed[:idx]
		}
		if firstLine == RepairPointerMarker && !facts.HasRepairPointer {
			facts.HasRepairPointer = true
			facts.RepairPointerCommentID = c.ID
		}
	}

	// Scan children for QA / Review lanes.
	for _, child := range issue.Children {
		if strings.HasPrefix(child.Title, childTitleQAPrefix) && !facts.HasQAChild {
			facts.HasQAChild = true
			facts.QAChildIdentifier = child.Identifier
		}
		if strings.HasPrefix(child.Title, childTitleReviewPrefix) && !facts.HasReviewChild {
			facts.HasReviewChild = true
			facts.ReviewChildIdentifier = child.Identifier
		}
	}

	// Optional enrichment from the handoff comment body.
	if facts.HandoffCommentBody != "" {
		if m := prURLRegex.FindString(facts.HandoffCommentBody); m != "" {
			facts.DerivedPRURL = m
		}
		if m := branchRegex.FindStringSubmatch(facts.HandoffCommentBody); len(m) > 1 {
			facts.DerivedBranch = m[1]
		}
	}

	return facts
}

// IdempotenceVerdict expresses whether a repair would be a no-op and why.
type IdempotenceVerdict struct {
	Blocked bool
	// Reason is human-readable and stable enough to assert on in tests.
	Reason string
}

// IdempotenceCheck returns Blocked=true with a fact-grounded reason whenever
// the repair cannot or should not be applied. Conditions evaluated in order:
//  1. No Q HANDOFF comment found
//  2. Parent in terminal state
//  3. Repair pointer already present
//  4. Both QA and Review children already exist
//  5. Only QA lane missing (Review exists but QA doesn't)
//  6. Only Review lane missing (QA exists but Review doesn't)
//  7. Handoff comment body is empty after trim
func IdempotenceCheck(facts *StaleHandoffFacts) IdempotenceVerdict {
	if facts.HandoffCommentID == "" {
		return IdempotenceVerdict{Blocked: true,
			Reason: "no Q HANDOFF / RETEST READY comment found on parent"}
	}
	if parentTerminalStateNames[facts.ParentStateName] {
		return IdempotenceVerdict{Blocked: true,
			Reason: "parent is in terminal state " + facts.ParentStateName + "; will not propose repair"}
	}
	if facts.HasRepairPointer {
		return IdempotenceVerdict{Blocked: true,
			Reason: "parent already has repair pointer comment " + facts.RepairPointerCommentID}
	}
	if facts.HasQAChild && facts.HasReviewChild {
		return IdempotenceVerdict{Blocked: true,
			Reason: "parent already has QA child " + facts.QAChildIdentifier +
				" and Review child " + facts.ReviewChildIdentifier}
	}
	if facts.HasQAChild {
		return IdempotenceVerdict{Blocked: true,
			Reason: "parent already has QA child " + facts.QAChildIdentifier +
				"; only Review lane missing — out of scope for this slice"}
	}
	if facts.HasReviewChild {
		return IdempotenceVerdict{Blocked: true,
			Reason: "parent already has Review child " + facts.ReviewChildIdentifier +
				"; only QA lane missing — out of scope for this slice"}
	}
	if strings.TrimSpace(facts.HandoffCommentBody) == "" {
		return IdempotenceVerdict{Blocked: true,
			Reason: "Q HANDOFF comment is empty after trim; cannot derive boundary"}
	}
	return IdempotenceVerdict{Blocked: false}
}

// Config is the operator-supplied identity bundle.
type Config struct {
	TeamID            string
	InReviewStateID   string
	InReviewStateName string // for display; defaults to "In Review"
	QAAssigneeID      string
	ReviewAssigneeID  string
}

// Proposal is the operator-readable repair plan.
type Proposal struct {
	ParentIdentifier         string   `json:"parent_identifier"`
	ParentID                 string   `json:"parent_id"`
	ParentTitle              string   `json:"parent_title"`
	ParentStateName          string   `json:"parent_state_name"`
	SourceCommentID          string   `json:"source_comment_id"`
	MissingLanes             []string `json:"missing_lanes"`
	IntendedQATitle          string   `json:"intended_qa_title"`
	IntendedQAAssigneeID     string   `json:"intended_qa_assignee_id"`
	IntendedReviewTitle      string   `json:"intended_review_title"`
	IntendedReviewAssigneeID string   `json:"intended_review_assignee_id"`
	IntendedParentStateID    string   `json:"intended_parent_state_id"`
	IntendedParentStateName  string   `json:"intended_parent_state_name"`
	Boundary                 string   `json:"boundary"`
	DerivedPRURL             string   `json:"derived_pr_url,omitempty"`
	DerivedBranch            string   `json:"derived_branch,omitempty"`
}

// Propose returns a Proposal iff IdempotenceCheck says not blocked.
// If blocked, returns (nil, verdict).
func Propose(facts *StaleHandoffFacts, cfg Config) (*Proposal, IdempotenceVerdict) {
	verdict := IdempotenceCheck(facts)
	if verdict.Blocked {
		return nil, verdict
	}

	stateName := cfg.InReviewStateName
	if stateName == "" {
		stateName = "In Review"
	}

	p := &Proposal{
		ParentIdentifier:         facts.ParentIdentifier,
		ParentID:                 facts.ParentID,
		ParentTitle:              facts.ParentTitle,
		ParentStateName:          facts.ParentStateName,
		SourceCommentID:          facts.HandoffCommentID,
		MissingLanes:             []string{"QA", "Review"},
		IntendedQATitle:          childTitleQAPrefix + facts.ParentTitle,
		IntendedQAAssigneeID:     cfg.QAAssigneeID,
		IntendedReviewTitle:      childTitleReviewPrefix + facts.ParentTitle,
		IntendedReviewAssigneeID: cfg.ReviewAssigneeID,
		IntendedParentStateID:    cfg.InReviewStateID,
		IntendedParentStateName:  stateName,
		Boundary:                 facts.HandoffCommentBody,
		DerivedPRURL:             facts.DerivedPRURL,
		DerivedBranch:            facts.DerivedBranch,
	}
	return p, IdempotenceVerdict{Blocked: false}
}
