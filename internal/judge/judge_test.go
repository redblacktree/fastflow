package judge

import (
	"testing"

	"github.com/redblacktree/fastflow/internal/backend"
)

// fakeBackend is a minimal backend.Backend for testing the judge.
type fakeBackend struct {
	output      string
	err         error
	lastOptions backend.InvokeOptions
}

func (f *fakeBackend) Name() string                       { return "fake" }
func (f *fakeBackend) DefaultModel() string               { return "fake-model" }
func (f *fakeBackend) Capabilities() backend.Capabilities { return backend.Capabilities{} }
func (f *fakeBackend) Invoke(opts backend.InvokeOptions) (*backend.InvokeResult, error) {
	f.lastOptions = opts
	if f.err != nil {
		return nil, f.err
	}
	return &backend.InvokeResult{Output: f.output}, nil
}

func TestNewJudge_DefaultsModel(t *testing.T) {
	fb := &fakeBackend{}
	j := NewJudge(fb, "")
	if j.Model != "fake-model" {
		t.Errorf("Model = %q, want fake-model", j.Model)
	}
	if j.MaxTurns != 5 {
		t.Errorf("MaxTurns = %d, want 5", j.MaxTurns)
	}
}

func TestNewJudge_ExplicitModel(t *testing.T) {
	fb := &fakeBackend{}
	j := NewJudge(fb, "haiku")
	if j.Model != "haiku" {
		t.Errorf("Model = %q, want haiku", j.Model)
	}
}

func TestParseResponse_Yes(t *testing.T) {
	fb := &fakeBackend{output: "YES: it worked great"}
	j := NewJudge(fb, "m")
	res, err := j.Evaluate(&EvaluationContext{JudgePrompt: "did it work?"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Error("expected Success=true")
	}
	if res.Reasoning != "it worked great" {
		t.Errorf("Reasoning = %q", res.Reasoning)
	}
}

func TestParseResponse_No(t *testing.T) {
	fb := &fakeBackend{output: "NO: missing output file"}
	j := NewJudge(fb, "m")
	res, err := j.Evaluate(&EvaluationContext{JudgePrompt: "did it work?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("expected Success=false")
	}
	if res.Reasoning != "missing output file" {
		t.Errorf("Reasoning = %q", res.Reasoning)
	}
}

func TestParseResponse_Interactive(t *testing.T) {
	fb := &fakeBackend{output: "INTERACTIVE: which branch should I use?"}
	j := NewJudge(fb, "m")
	res, err := j.Evaluate(&EvaluationContext{JudgePrompt: "did it work?"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interactive {
		t.Error("expected Interactive=true")
	}
}

func TestParseResponse_Unparseable(t *testing.T) {
	fb := &fakeBackend{output: "I think it went well"}
	j := NewJudge(fb, "m")
	res, err := j.Evaluate(&EvaluationContext{JudgePrompt: "did it work?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("expected Success=false for unparseable response")
	}
}

func TestEvaluatePassesWorkDirToBackend(t *testing.T) {
	fb := &fakeBackend{output: "YES: ok"}
	j := NewJudge(fb, "m")
	_, err := j.Evaluate(&EvaluationContext{
		JudgePrompt: "did it work?",
		WorkDir:     "/tmp/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fb.lastOptions.WorkDir != "/tmp/work" {
		t.Errorf("WorkDir = %q, want /tmp/work", fb.lastOptions.WorkDir)
	}
}
