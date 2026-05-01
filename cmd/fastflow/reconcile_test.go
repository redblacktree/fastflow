package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/redblacktree/fastflow/internal/linear"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/reconcile"
)

// fakeClient implements reconcile.Mutator using pre-set issue data.
type fakeClient struct {
	issue   *linear.Issue
	failGet bool
	// mutation results
	createCount   int
	createdIssues []*linear.Issue
	comments      []*linear.Comment
}

func (f *fakeClient) GetIssueWithChildren(id string) (*linear.Issue, error) {
	if f.failGet {
		return nil, errors.New("simulated get failure")
	}
	return f.issue, nil
}

func (f *fakeClient) CreateIssue(in linear.CreateIssueInput) (*linear.Issue, error) {
	f.createCount++
	iss := &linear.Issue{ID: "stub-id-" + in.Title[:3], Identifier: "STUB-20" + string(rune('0'+f.createCount)), Title: in.Title}
	f.createdIssues = append(f.createdIssues, iss)
	return iss, nil
}

func (f *fakeClient) UpdateIssueState(issueID, stateID string) error { return nil }

func (f *fakeClient) CreateComment(issueID, body string) (*linear.Comment, error) {
	c := &linear.Comment{ID: "stub-cmt-" + issueID, Body: body}
	f.comments = append(f.comments, c)
	return c, nil
}

// captureOutput temporarily redirects output.Writer and disables colors.
// Returns the captured bytes and a cleanup function.
func captureOutput(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := output.Writer
	prevNoColor := color.NoColor
	output.Writer = buf
	color.NoColor = true
	return buf, func() {
		output.Writer = prev
		color.NoColor = prevNoColor
	}
}

func makeREL548Pre() *linear.Issue {
	return &linear.Issue{
		ID:         "aaa00548-0000-0000-0000-000000000000",
		Identifier: "REL-548",
		Title:      "Add --on-complete hook support for pipeline runs",
		State:      linear.WorkflowState{ID: "state-in-progress-id", Name: "In Progress", Type: "started"},
		Comments: []linear.Comment{
			{
				ID:        "556160da",
				Body:      "Q HANDOFF / RETEST READY\n\nBranch: REL-548\nPR: https://github.com/redblacktree/fastflow/pull/36\n\nImplementation complete. --on-complete flag added and tested.",
				CreatedAt: "2026-04-28T14:00:00.000Z",
			},
		},
	}
}

func makeREL548Post() *linear.Issue {
	return &linear.Issue{
		ID:         "aaa00548-0000-0000-0000-000000000000",
		Identifier: "REL-548",
		Title:      "Add --on-complete hook support for pipeline runs",
		State:      linear.WorkflowState{ID: "state-in-review-id", Name: "In Review", Type: "started"},
		Children: []linear.Issue{
			{ID: "aaa00558-0000-0000-0000-000000000000", Identifier: "REL-558", Title: "QA: Add --on-complete hook support for pipeline runs"},
			{ID: "aaa00559-0000-0000-0000-000000000000", Identifier: "REL-559", Title: "Review: Add --on-complete hook support for pipeline runs"},
		},
		Comments: []linear.Comment{
			{
				ID:   "556160da",
				Body: "Q HANDOFF / RETEST READY\n\nBranch: REL-548\nPR: https://github.com/redblacktree/fastflow/pull/36\n\nImplementation complete. --on-complete flag added and tested.",
			},
			{
				ID:   "repair-ptr-548",
				Body: "[fastflow-reconcile] stale-handoff repair applied\n- QA child: REL-558\n- Review child: REL-559\n- Source comment id: 556160da\n- Tool: fastflow reconcile stale-handoff (REL-546)",
			},
		},
	}
}

var testReconcileCfg = reconcile.Config{
	TeamID:            "test-team-id",
	InReviewStateID:   "state-in-review-id",
	InReviewStateName: "In Review",
	QAAssigneeID:      "test-qa-assignee-id",
	ReviewAssigneeID:  "test-review-assignee-id",
}

func TestRunReconcileStaleHandoff_dryRun_REL548_pre_printsExpectedProposal(t *testing.T) {
	buf, cleanup := captureOutput(t)
	defer cleanup()

	client := &fakeClient{issue: makeREL548Pre()}
	runDir := t.TempDir()

	if err := runReconcileStaleHandoffWith("REL-548", false, testReconcileCfg, client, runDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	goldenPath := filepath.Join("testdata", "proposal_REL-548_pre.txt")
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	want := string(wantBytes)

	if got != want {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRunReconcileStaleHandoff_dryRun_REL548_post_prints_no_op(t *testing.T) {
	buf, cleanup := captureOutput(t)
	defer cleanup()

	client := &fakeClient{issue: makeREL548Post()}
	if err := runReconcileStaleHandoffWith("REL-548", false, testReconcileCfg, client, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, ">>> No-op:") {
		t.Errorf("output should start with '>>> No-op:', got: %q", got)
	}
}

func TestRunReconcileStaleHandoff_apply_REL548_pre_writesRecordFile(t *testing.T) {
	_, cleanup := captureOutput(t)
	defer cleanup()

	runDir := t.TempDir()
	client := &fakeClient{issue: makeREL548Pre()}

	if err := runReconcileStaleHandoffWith("REL-548", true, testReconcileCfg, client, runDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	recordPath := filepath.Join(runDir, "reconcile-REL-548.json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("record file not created: %v", err)
	}
	var record reconcile.RepairRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("record JSON invalid: %v", err)
	}
	if record.QAChildIdentifier == "" {
		t.Error("QAChildIdentifier is empty in record")
	}
	if record.ReviewChildIdentifier == "" {
		t.Error("ReviewChildIdentifier is empty in record")
	}
	if record.RepairPointerID == "" {
		t.Error("RepairPointerID is empty in record")
	}
}

func TestRunReconcileStaleHandoff_applyMissingConfigRefusesBeforeMutation(t *testing.T) {
	_, cleanup := captureOutput(t)
	defer cleanup()

	tests := []struct {
		name    string
		mutate  func(*reconcile.Config)
		wantErr string
	}{
		{
			name: "missing team",
			mutate: func(cfg *reconcile.Config) {
				cfg.TeamID = ""
			},
			wantErr: "LINEAR_TEAM_ID/--team",
		},
		{
			name: "missing in review state",
			mutate: func(cfg *reconcile.Config) {
				cfg.InReviewStateID = ""
			},
			wantErr: "LINEAR_IN_REVIEW_STATE_ID/--in-review-state",
		},
		{
			name: "missing qa assignee",
			mutate: func(cfg *reconcile.Config) {
				cfg.QAAssigneeID = ""
			},
			wantErr: "LINEAR_QA_ASSIGNEE_ID/--qa-assignee",
		},
		{
			name: "missing review assignee",
			mutate: func(cfg *reconcile.Config) {
				cfg.ReviewAssigneeID = ""
			},
			wantErr: "LINEAR_REVIEW_ASSIGNEE_ID/--review-assignee",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testReconcileCfg
			tt.mutate(&cfg)
			client := &fakeClient{issue: makeREL548Pre()}

			err := runReconcileStaleHandoffWith("REL-548", true, cfg, client, t.TempDir())
			if err == nil {
				t.Fatal("expected missing config error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not mention %q", err.Error(), tt.wantErr)
			}
			if client.createCount != 0 {
				t.Fatalf("expected no issue mutations, got %d CreateIssue calls", client.createCount)
			}
			if len(client.comments) != 0 {
				t.Fatalf("expected no comment mutations, got %d CreateComment calls", len(client.comments))
			}
		})
	}
}

func TestRunReconcileStaleHandoff_apply_post_repair_exits_zero_with_no_op(t *testing.T) {
	buf, cleanup := captureOutput(t)
	defer cleanup()

	client := &fakeClient{issue: makeREL548Post()}
	err := runReconcileStaleHandoffWith("REL-548", true, testReconcileCfg, client, t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error (ErrAlreadyApplied → no-op), got %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, ">>> No-op:") {
		t.Errorf("output should start with '>>> No-op:', got: %q", got)
	}
}

func TestRunReconcileStaleHandoff_missing_LINEAR_API_KEY_returns_error(t *testing.T) {
	// Save and restore env + flag
	prevToken := flagReconcileLinearToken
	prevEnv := os.Getenv("LINEAR_API_KEY")
	flagReconcileLinearToken = ""
	os.Unsetenv("LINEAR_API_KEY")
	defer func() {
		flagReconcileLinearToken = prevToken
		if prevEnv != "" {
			os.Setenv("LINEAR_API_KEY", prevEnv)
		}
	}()

	// Also ensure --issue is set so cobra flag validation doesn't interfere
	prevIssue := flagReconcileIssue
	flagReconcileIssue = "REL-548"
	defer func() { flagReconcileIssue = prevIssue }()

	err := runReconcileStaleHandoff(staleHandoffCmd, nil)
	if err == nil {
		t.Fatal("expected error for missing LINEAR_API_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "LINEAR_API_KEY") {
		t.Errorf("error %q does not mention LINEAR_API_KEY", err.Error())
	}
}

// Test that the proposal output for REL-549 matches its golden file.
func TestRunReconcileStaleHandoff_dryRun_REL549_pre_printsExpectedProposal(t *testing.T) {
	buf, cleanup := captureOutput(t)
	defer cleanup()

	client := &fakeClient{issue: &linear.Issue{
		ID:         "aaa00549-0000-0000-0000-000000000000",
		Identifier: "REL-549",
		Title:      "Implement fastflow monitor web dashboard",
		State:      linear.WorkflowState{ID: "state-in-progress-id", Name: "In Progress", Type: "started"},
		Comments: []linear.Comment{
			{
				ID:        "e0a0b191",
				Body:      "Q HANDOFF / RETEST READY\n\nBranch: REL-549\nPR: https://github.com/redblacktree/fastflow/pull/41\n\nMonitor command wired and documented. Dashboard functional.",
				CreatedAt: "2026-04-29T10:00:00.000Z",
			},
		},
	}}

	if err := runReconcileStaleHandoffWith("REL-549", false, testReconcileCfg, client, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	wantBytes, err := os.ReadFile(filepath.Join("testdata", "proposal_REL-549_pre.txt"))
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	if got != string(wantBytes) {
		t.Errorf("output mismatch for REL-549\n--- got ---\n%s\n--- want ---\n%s", got, string(wantBytes))
	}
}

// Ensure output.Writer is compatible with io.Writer (compile-time check).
var _ io.Writer = output.Writer
