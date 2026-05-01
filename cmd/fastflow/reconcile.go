package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/redblacktree/fastflow/internal/linear"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/reconcile"
	"github.com/redblacktree/fastflow/internal/runner"
	"github.com/spf13/cobra"
)

var (
	flagReconcileIssue           string
	flagReconcileApply           bool
	flagReconcileLinearToken     string
	flagReconcileTeamID          string
	flagReconcileInReviewStateID string
	flagReconcileQAAssigneeID    string
	flagReconcileReviewAssigneeID string
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Run reconcile slices that detect and repair workflow gaps",
	Long:  `Reconcile commands inspect Linear state and propose / apply targeted repairs.`,
}

var staleHandoffCmd = &cobra.Command{
	Use:   "stale-handoff",
	Short: "Detect & repair parents with a Q handoff but missing QA/Review children",
	Long: `Detect parent Linear issues that have a Q HANDOFF / RETEST READY comment but
are missing their QA and Review child lanes, then produce a fact-backed proposal
(dry-run) or execute a safe, idempotent repair (--apply).

Examples:
  # Dry-run against REL-548 (shows proposal or no-op)
  fastflow reconcile stale-handoff --issue REL-548

  # Apply the repair
  LINEAR_API_KEY=... fastflow reconcile stale-handoff --issue REL-548 --apply`,
	RunE: runReconcileStaleHandoff,
}

func init() {
	staleHandoffCmd.Flags().StringVar(&flagReconcileIssue, "issue", "", "Linear issue identifier (e.g. REL-548)")
	staleHandoffCmd.Flags().BoolVar(&flagReconcileApply, "apply", false, "Execute the repair (default: dry-run)")
	staleHandoffCmd.Flags().StringVar(&flagReconcileLinearToken, "linear-token", "", "Linear API token (default: $LINEAR_API_KEY)")
	staleHandoffCmd.Flags().StringVar(&flagReconcileTeamID, "team", "", "Linear team ID (default: $LINEAR_TEAM_ID)")
	staleHandoffCmd.Flags().StringVar(&flagReconcileInReviewStateID, "in-review-state", "", "Linear workflow state ID for In Review (default: $LINEAR_IN_REVIEW_STATE_ID)")
	staleHandoffCmd.Flags().StringVar(&flagReconcileQAAssigneeID, "qa-assignee", "", "Linear user ID for QA child (default: $LINEAR_QA_ASSIGNEE_ID)")
	staleHandoffCmd.Flags().StringVar(&flagReconcileReviewAssigneeID, "review-assignee", "", "Linear user ID for Review child (default: $LINEAR_REVIEW_ASSIGNEE_ID)")
	_ = staleHandoffCmd.MarkFlagRequired("issue")

	reconcileCmd.AddCommand(staleHandoffCmd)
	rootCmd.AddCommand(reconcileCmd)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func runReconcileStaleHandoff(cmd *cobra.Command, args []string) error {
	errColor := color.New(color.FgRed).SprintFunc()

	token := firstNonEmpty(flagReconcileLinearToken, os.Getenv("LINEAR_API_KEY"))
	if token == "" {
		return fmt.Errorf("%s LINEAR_API_KEY environment variable or --linear-token flag is required",
			errColor("ERROR"))
	}

	cfg := reconcile.Config{
		TeamID:            firstNonEmpty(flagReconcileTeamID, os.Getenv("LINEAR_TEAM_ID")),
		InReviewStateID:   firstNonEmpty(flagReconcileInReviewStateID, os.Getenv("LINEAR_IN_REVIEW_STATE_ID")),
		InReviewStateName: "In Review",
		QAAssigneeID:      firstNonEmpty(flagReconcileQAAssigneeID, os.Getenv("LINEAR_QA_ASSIGNEE_ID")),
		ReviewAssigneeID:  firstNonEmpty(flagReconcileReviewAssigneeID, os.Getenv("LINEAR_REVIEW_ASSIGNEE_ID")),
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	runDir := runner.GetRunDir(cwd, flagReconcileIssue)

	client := linear.New(token)
	return runReconcileStaleHandoffWith(flagReconcileIssue, flagReconcileApply, cfg, client, runDir)
}

// runReconcileStaleHandoffWith is the testable handler. Tests inject a fake client
// and a temp runDir; the cobra handler builds the real client and delegates here.
func runReconcileStaleHandoffWith(
	issue string,
	apply bool,
	cfg reconcile.Config,
	client reconcile.Mutator,
	runDir string,
) error {
	errColor := color.New(color.FgRed).SprintFunc()
	info := color.New(color.FgCyan).SprintFunc()
	successColor := color.New(color.FgGreen).SprintFunc()

	iss, err := client.GetIssueWithChildren(issue)
	if err != nil {
		return fmt.Errorf("%s failed to fetch issue %s: %w", errColor("ERROR"), issue, err)
	}

	facts := reconcile.Detect(iss)
	proposal, verdict := reconcile.Propose(facts, cfg)

	if verdict.Blocked {
		output.Printf("%s No-op: %s\n", info(">>>"), verdict.Reason)
		return nil
	}

	printProposal(proposal)

	if !apply {
		output.Printf("[run with --apply to execute]\n")
		return nil
	}

	if err := os.MkdirAll(runDir, 0755); err != nil {
		return fmt.Errorf("%s failed to create run dir: %w", errColor("ERROR"), err)
	}
	recordPath := filepath.Join(runDir, "reconcile-"+issue+".json")

	persist := func(r *reconcile.RepairRecord) error {
		data, jerr := json.MarshalIndent(r, "", "  ")
		if jerr != nil {
			return jerr
		}
		return os.WriteFile(recordPath, data, 0644)
	}

	record, err := reconcile.Apply(client, issue, cfg, persist)
	var alreadyApplied *reconcile.ErrAlreadyApplied
	if errors.As(err, &alreadyApplied) {
		output.Printf("%s No-op: %s\n", info(">>>"), alreadyApplied.Reason)
		return nil
	}
	if err != nil {
		output.Printf("%s Apply failed: %v\n", errColor("ERROR"), err)
		if recordPath != "" {
			output.Printf("    Partial record: %s\n", recordPath)
		}
		return err
	}

	output.Printf("%s Repair applied successfully\n", successColor("SUCCESS"))
	output.Printf("  QA child:       %s\n", record.QAChildIdentifier)
	output.Printf("  Review child:   %s\n", record.ReviewChildIdentifier)
	output.Printf("  Repair pointer: %s\n", record.RepairPointerID)
	return nil
}

func printProposal(p *reconcile.Proposal) {
	output.Printf("PROPOSAL: %s — stale handoff detected\n", p.ParentIdentifier)
	output.Printf("  %-21s  %s\n", "parent id", p.ParentID)
	output.Printf("  %-21s  %s\n", "parent state", p.ParentStateName)
	output.Printf("  %-21s  %s\n", "source comment id", p.SourceCommentID)
	output.Printf("  %-21s  %s\n", "missing lanes", strings.Join(p.MissingLanes, ", "))
	output.Printf("  %-21s  \"%s\" → assignee %s\n", "intended QA child", p.IntendedQATitle, p.IntendedQAAssigneeID)
	output.Printf("  %-21s  \"%s\" → assignee %s\n", "intended Review child", p.IntendedReviewTitle, p.IntendedReviewAssigneeID)
	output.Printf("  %-21s  %s → %s\n", "parent state change", p.ParentStateName, p.IntendedParentStateName)

	prURL := p.DerivedPRURL
	if prURL == "" {
		prURL = "(none detected)"
	}
	branch := p.DerivedBranch
	if branch == "" {
		branch = "(none detected)"
	}
	output.Printf("  %-21s  %s\n", "derived PR", prURL)
	output.Printf("  %-21s  %s\n", "derived branch", branch)
	output.Printf("\n")
	output.Printf("Boundary (verbatim, will be copied to QA child):\n")
	for _, line := range strings.Split(p.Boundary, "\n") {
		if line == "" {
			output.Printf("  |\n")
		} else {
			output.Printf("  | %s\n", line)
		}
	}
	output.Printf("\n")
}
