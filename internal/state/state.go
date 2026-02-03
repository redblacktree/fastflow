// Package state provides pipeline state tracking for resume functionality.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const StateFileName = "state.json"

// PipelineState tracks the progress of a pipeline run.
type PipelineState struct {
	// CompletedStages is the ordered list of stages that have passed the judge.
	CompletedStages []string `json:"completed_stages"`
	// Workflow is the workflow name used for this run.
	Workflow string `json:"workflow"`
	// ConfigHash is a hash of the relevant config to detect changes.
	ConfigHash string `json:"config_hash"`
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
func NewState(workflow string, stages []string) *PipelineState {
	return &PipelineState{
		CompletedStages: []string{},
		Workflow:        workflow,
		ConfigHash:      ComputeConfigHash(workflow, stages),
	}
}
