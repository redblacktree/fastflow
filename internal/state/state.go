// Package state provides pipeline state tracking for resume functionality.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const StateFileName = "state.json"

// Status constants for run lifecycle.
const (
	StatusRunning    = "running"
	StatusCheckpoint = "checkpoint"
	StatusComplete   = "complete"
	StatusFailed     = "failed"
	StatusStale      = "stale"
)

// PipelineState tracks the progress of a pipeline run.
type PipelineState struct {
	// CompletedStages is the ordered list of stages that have passed the judge.
	CompletedStages []string `json:"completed_stages"`
	// Workflow is the workflow name used for this run.
	Workflow string `json:"workflow"`
	// ConfigHash is a hash of the relevant config to detect changes.
	ConfigHash string `json:"config_hash"`
	// Ticket is the ticket identifier for this run.
	Ticket string `json:"ticket,omitempty"`
	// Status is the current run status: running, checkpoint, complete, failed, stale.
	Status string `json:"status,omitempty"`
	// Stage is the name of the currently executing stage.
	Stage string `json:"stage,omitempty"`
	// StartedAt is the RFC3339 timestamp when the run started.
	StartedAt string `json:"started_at,omitempty"`
	// UpdatedAt is the RFC3339 timestamp of the last state change.
	UpdatedAt string `json:"updated_at,omitempty"`
	// WorkDir is the absolute path to the working directory for this run.
	WorkDir string `json:"work_dir,omitempty"`
	// ExitCode is the process exit code (0 for success, non-zero for failure).
	ExitCode int `json:"exit_code,omitempty"`
	// Error is the error message if the run failed.
	Error string `json:"error,omitempty"`
	// Pid is the process ID of the fastflow process.
	Pid int `json:"pid,omitempty"`
	// OnComplete is the shell command to execute after run completes.
	OnComplete string `json:"on_complete,omitempty"`
}

// Load reads the state file from the run directory.
// Returns nil state (not error) if file doesn't exist.
func Load(runDir string) (*PipelineState, error) {
	path := filepath.Join(runDir, StateFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state PipelineState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}
	return &state, nil
}

// Save writes the state to the run directory.
func (s *PipelineState) Save(runDir string) error {
	path := filepath.Join(runDir, StateFileName)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	return nil
}

// MarkStageComplete adds a stage to the completed list and saves.
func (s *PipelineState) MarkStageComplete(runDir, stageName string) error {
	s.CompletedStages = append(s.CompletedStages, stageName)
	return s.Save(runDir)
}

// IsStageComplete checks if a stage has already been completed.
func (s *PipelineState) IsStageComplete(stageName string) bool {
	for _, completed := range s.CompletedStages {
		if completed == stageName {
			return true
		}
	}
	return false
}

// SetStatus updates the run status and saves.
func (s *PipelineState) SetStatus(runDir, status string) error {
	s.Status = status
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	return s.Save(runDir)
}

// SetStage updates the current stage name and saves.
func (s *PipelineState) SetStage(runDir, stageName string) error {
	s.Stage = stageName
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	return s.Save(runDir)
}

// SetFinalStatus updates the run to a terminal state with exit details and saves.
func (s *PipelineState) SetFinalStatus(runDir, status string, exitCode int, errMsg string) error {
	s.Status = status
	s.ExitCode = exitCode
	s.Error = errMsg
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	return s.Save(runDir)
}

// ComputeConfigHash generates a hash from workflow stages for change detection.
func ComputeConfigHash(workflow string, stages []string) string {
	h := sha256.New()
	h.Write([]byte(workflow))
	for _, stage := range stages {
		h.Write([]byte(stage))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NewState creates a new state for a fresh run.
func NewState(workflow string, stages []string, ticket string, workDir string) *PipelineState {
	now := time.Now().Format(time.RFC3339)
	return &PipelineState{
		CompletedStages: []string{},
		Workflow:        workflow,
		ConfigHash:      ComputeConfigHash(workflow, stages),
		Ticket:          ticket,
		Status:          StatusRunning,
		StartedAt:       now,
		UpdatedAt:       now,
		WorkDir:         workDir,
	}
}

// RunInfo contains discovered run information from state files.
type RunInfo struct {
	Ticket string
	State  *PipelineState
	RunDir string
}

// ScanRunDirs scans thoughts/shared/runs/ under workDir for state.json files.
// Returns a list of discovered runs.
func ScanRunDirs(workDir string) ([]RunInfo, error) {
	runsDir := filepath.Join(workDir, "thoughts", "shared", "runs")
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read runs directory: %w", err)
	}

	var runs []RunInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ticket := entry.Name()
		runDir := filepath.Join(runsDir, ticket)
		st, err := Load(runDir)
		if err != nil || st == nil {
			continue // skip dirs without valid state.json
		}
		runs = append(runs, RunInfo{
			Ticket: ticket,
			State:  st,
			RunDir: runDir,
		})
	}
	return runs, nil
}
