package runner

import (
	"errors"
	"testing"

	"github.com/redblacktree/fastflow/internal/config"
	hs "github.com/redblacktree/fastflow/internal/harness"
)

// fakeHarness is a no-op harness that records the last InvokeOptions it received.
type fakeHarness struct {
	name         string
	defaultModel string
	received     *hs.InvokeOptions
	calls        []hs.InvokeOptions
	errByModel   map[string]error
	outputs      []string
}

func (f *fakeHarness) Name() string         { return f.name }
func (f *fakeHarness) DefaultModel() string { return f.defaultModel }
func (f *fakeHarness) Capabilities() hs.Capabilities {
	return hs.Capabilities{}
}
func (f *fakeHarness) Invoke(opts hs.InvokeOptions) (*hs.InvokeResult, error) {
	f.calls = append(f.calls, opts)
	f.received = &opts
	if err := f.errByModel[opts.Model]; err != nil {
		return nil, err
	}
	output := "fake output"
	if len(f.outputs) >= len(f.calls) {
		output = f.outputs[len(f.calls)-1]
	}
	return &hs.InvokeResult{Output: output}, nil
}

func TestExecuteStage_UsesPerStageHarness(t *testing.T) {
	fakeA := &fakeHarness{name: "fake-a", defaultModel: "model-a"}
	fakeB := &fakeHarness{name: "fake-b", defaultModel: "model-b"}

	hs.Register("fake-a", func(cfg hs.Config) (hs.Harness, error) { return fakeA, nil })
	hs.Register("fake-b", func(cfg hs.Config) (hs.Harness, error) { return fakeB, nil })

	cfg := &config.Config{
		Harness:         "fake-a",
		JudgeHarness:    "fake-a",
		JudgeModel:      "model-a",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			// s1 overrides to fake-b; model is empty so it should come from fake-b.DefaultModel()
			"s1": {Skill: "test", Harness: "fake-b"},
		},
	}

	r := NewRunner(cfg, "")
	ctx := &RunContext{
		Goal:    "test goal",
		Ticket:  "TEST-001",
		WorkDir: t.TempDir(),
		RunDir:  t.TempDir(),
	}

	stage, err := cfg.GetStage("s1")
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}

	result, err := r.executeStage(ctx, "s1", stage)
	if err != nil {
		t.Fatalf("executeStage: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	if fakeB.received == nil {
		t.Error("fake-b was not called")
	}
	if fakeA.received != nil {
		t.Error("fake-a was unexpectedly called for stage s1")
	}
	if fakeB.received != nil && fakeB.received.Model != "model-b" {
		t.Errorf("Model = %q, want model-b (from fake-b.DefaultModel)", fakeB.received.Model)
	}
}

func TestExecuteStage_UsesGlobalHarnessWhenNoStageOverride(t *testing.T) {
	fakeC := &fakeHarness{name: "fake-c", defaultModel: "model-c"}
	hs.Register("fake-c", func(cfg hs.Config) (hs.Harness, error) { return fakeC, nil })

	cfg := &config.Config{
		Harness:         "fake-c",
		JudgeHarness:    "fake-c",
		JudgeModel:      "model-c",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s2"}}},
		Stages: map[string]config.Stage{
			// s2 has no Harness override, so it should use global "fake-c".
			"s2": {Skill: "test"},
		},
	}

	r := NewRunner(cfg, "")
	ctx := &RunContext{
		Goal:    "test goal",
		Ticket:  "TEST-002",
		WorkDir: t.TempDir(),
		RunDir:  t.TempDir(),
	}

	stage, _ := cfg.GetStage("s2")
	if _, err := r.executeStage(ctx, "s2", stage); err != nil {
		t.Fatalf("executeStage: %v", err)
	}

	if fakeC.received == nil {
		t.Error("fake-c was not called for s2 (expected global harness)")
	}
}

func TestExecuteStage_ModelComesFromStageWhenSet(t *testing.T) {
	fakeD := &fakeHarness{name: "fake-d", defaultModel: "model-d"}
	hs.Register("fake-d", func(cfg hs.Config) (hs.Harness, error) { return fakeD, nil })

	cfg := &config.Config{
		Harness:         "fake-d",
		JudgeHarness:    "fake-d",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s3"}}},
		Stages: map[string]config.Stage{
			"s3": {Skill: "test", Model: "explicit-model"},
		},
	}

	r := NewRunner(cfg, "")
	ctx := &RunContext{
		Goal:    "test goal",
		Ticket:  "TEST-003",
		WorkDir: t.TempDir(),
		RunDir:  t.TempDir(),
	}

	stage, _ := cfg.GetStage("s3")
	if _, err := r.executeStage(ctx, "s3", stage); err != nil {
		t.Fatalf("executeStage: %v", err)
	}

	if fakeD.received == nil {
		t.Fatal("fake-d was not called")
	}
	if fakeD.received.Model != "explicit-model" {
		t.Errorf("Model = %q, want explicit-model", fakeD.received.Model)
	}
}

func TestExecuteStage_BackupModelsFallbackOnRateLimit(t *testing.T) {
	fakeE := &fakeHarness{
		name:         "fake-e",
		defaultModel: "default-model",
		errByModel:   map[string]error{"cheap-model": hs.ErrRateLimited},
	}
	hs.Register("fake-e", func(cfg hs.Config) (hs.Harness, error) { return fakeE, nil })

	cfg := &config.Config{
		Harness:         "fake-e",
		JudgeHarness:    "fake-e",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s4"}}},
		Stages: map[string]config.Stage{
			"s4": {
				Skill: "test",
				Model: "cheap-model",
				BackupModels: []config.ModelAttempt{
					{Harness: "fake-e", Model: "smart-model"},
				},
			},
		},
	}

	r := NewRunner(cfg, "")
	ctx := &RunContext{
		Goal:    "test goal",
		Ticket:  "TEST-004",
		WorkDir: t.TempDir(),
		RunDir:  t.TempDir(),
	}

	stage, _ := cfg.GetStage("s4")
	result, err := r.executeStage(ctx, "s4", stage)
	if err != nil {
		t.Fatalf("executeStage: %v", err)
	}
	if len(fakeE.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fakeE.calls))
	}
	if fakeE.calls[0].Model != "cheap-model" || fakeE.calls[1].Model != "smart-model" {
		t.Fatalf("backup models = [%s %s], want [cheap-model smart-model]", fakeE.calls[0].Model, fakeE.calls[1].Model)
	}
	if result.Model != "smart-model" {
		t.Errorf("result.Model = %q, want smart-model", result.Model)
	}
}

func TestRun_UpgradesModelWhenJudgeFailsStage(t *testing.T) {
	stageHarness := &fakeHarness{
		name:         "fake-stage-upgrade",
		defaultModel: "default-stage",
		errByModel:   map[string]error{"smart-model": hs.ErrRateLimited},
	}
	judgeHarness := &fakeHarness{
		name:         "fake-judge-upgrade",
		defaultModel: "default-judge",
		outputs:      []string{"NO: not good enough", "YES: fixed by smarter model"},
	}
	hs.Register("fake-stage-upgrade", func(cfg hs.Config) (hs.Harness, error) { return stageHarness, nil })
	hs.Register("fake-judge-upgrade", func(cfg hs.Config) (hs.Harness, error) { return judgeHarness, nil })

	cfg := &config.Config{
		Harness:            "fake-stage-upgrade",
		JudgeHarness:       "fake-judge-upgrade",
		JudgeModel:         "judge-model",
		DefaultWorkflow:    "full",
		DefaultJudgePrompt: "did it work?",
		Workflows:          map[string]config.Workflow{"full": {Stages: []string{"s5"}}},
		Stages: map[string]config.Stage{
			"s5": {
				Skill: "test",
				Model: "cheap-model",
				EscalationModels: []config.ModelAttempt{
					{Harness: "fake-stage-upgrade", Model: "smart-model"},
					{Harness: "fake-stage-upgrade", Model: "smarter-model"},
				},
			},
		},
	}

	r := NewRunner(cfg, "")
	ctx := &RunContext{
		Goal:     "test goal",
		Ticket:   "TEST-005",
		WorkDir:  t.TempDir(),
		RunDir:   t.TempDir(),
		Workflow: "full",
	}

	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stageHarness.calls) != 3 {
		t.Fatalf("stage calls = %d, want 3", len(stageHarness.calls))
	}
	if stageHarness.calls[0].Model != "cheap-model" || stageHarness.calls[1].Model != "smart-model" || stageHarness.calls[2].Model != "smarter-model" {
		t.Fatalf("escalation models = [%s %s %s], want [cheap-model smart-model smarter-model]", stageHarness.calls[0].Model, stageHarness.calls[1].Model, stageHarness.calls[2].Model)
	}
	if len(judgeHarness.calls) != 2 {
		t.Fatalf("judge calls = %d, want 2", len(judgeHarness.calls))
	}
}

func TestRetryAttemptsForStage_ReusesSuccessfulBackupWhenNoEscalation(t *testing.T) {
	fakeF := &fakeHarness{name: "fake-f", defaultModel: "default-model"}
	hs.Register("fake-f", func(cfg hs.Config) (hs.Harness, error) { return fakeF, nil })

	cfg := &config.Config{Harness: "fake-f"}
	r := NewRunner(cfg, "")
	stage := &config.Stage{
		Skill: "test",
		Model: "cheap-model",
		BackupModels: []config.ModelAttempt{
			{Harness: "fake-f", Model: "backup-model"},
		},
	}
	previous := &InvokeResult{Harness: "fake-f", Model: "backup-model"}

	attempts, startIndex, upgrading, err := r.retryAttemptsForStage(stage, previous)
	if err != nil {
		t.Fatalf("retryAttemptsForStage: %v", err)
	}
	if upgrading {
		t.Fatal("upgrading = true, want false")
	}
	if startIndex != 0 {
		t.Fatalf("startIndex = %d, want 0", startIndex)
	}
	if len(attempts) != 1 || attempts[0].Model != "backup-model" {
		t.Fatalf("attempts = %+v, want only backup-model", attempts)
	}
}

func TestShouldTryNextModelDoesNotRetryAuthErrors(t *testing.T) {
	if shouldTryNextModel(hs.ErrNotLoggedIn) {
		t.Fatal("should not retry not-logged-in errors")
	}
	if !shouldTryNextModel(errors.New("model is temporarily unavailable")) {
		t.Fatal("should retry transient model availability errors")
	}
}
