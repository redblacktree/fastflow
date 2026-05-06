package runner

import (
	"testing"

	bk "github.com/redblacktree/fastflow/internal/backend"
	"github.com/redblacktree/fastflow/internal/config"
)

// fakeBackend is a no-op backend that records the last InvokeOptions it received.
type fakeBackend struct {
	name         string
	defaultModel string
	received     *bk.InvokeOptions
}

func (f *fakeBackend) Name() string         { return f.name }
func (f *fakeBackend) DefaultModel() string  { return f.defaultModel }
func (f *fakeBackend) Capabilities() bk.Capabilities {
	return bk.Capabilities{}
}
func (f *fakeBackend) Invoke(opts bk.InvokeOptions) (*bk.InvokeResult, error) {
	f.received = &opts
	return &bk.InvokeResult{Output: "fake output"}, nil
}

func TestExecuteStage_UsesPerStageBackend(t *testing.T) {
	fakeA := &fakeBackend{name: "fake-a", defaultModel: "model-a"}
	fakeB := &fakeBackend{name: "fake-b", defaultModel: "model-b"}

	bk.Register("fake-a", func(cfg bk.Config) (bk.Backend, error) { return fakeA, nil })
	bk.Register("fake-b", func(cfg bk.Config) (bk.Backend, error) { return fakeB, nil })

	cfg := &config.Config{
		Backend:         "fake-a",
		JudgeBackend:    "fake-a",
		JudgeModel:      "model-a",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s1"}}},
		Stages: map[string]config.Stage{
			// s1 overrides to fake-b; model is empty so it should come from fake-b.DefaultModel()
			"s1": {Skill: "test", Backend: "fake-b"},
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

func TestExecuteStage_UsesGlobalBackendWhenNoStageOverride(t *testing.T) {
	fakeC := &fakeBackend{name: "fake-c", defaultModel: "model-c"}
	bk.Register("fake-c", func(cfg bk.Config) (bk.Backend, error) { return fakeC, nil })

	cfg := &config.Config{
		Backend:         "fake-c",
		JudgeBackend:    "fake-c",
		JudgeModel:      "model-c",
		DefaultWorkflow: "full",
		Workflows:       map[string]config.Workflow{"full": {Stages: []string{"s2"}}},
		Stages: map[string]config.Stage{
			// s2 has no Backend override — should use global "fake-c"
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
		t.Error("fake-c was not called for s2 (expected global backend)")
	}
}

func TestExecuteStage_ModelComesFromStageWhenSet(t *testing.T) {
	fakeD := &fakeBackend{name: "fake-d", defaultModel: "model-d"}
	bk.Register("fake-d", func(cfg bk.Config) (bk.Backend, error) { return fakeD, nil })

	cfg := &config.Config{
		Backend:         "fake-d",
		JudgeBackend:    "fake-d",
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
