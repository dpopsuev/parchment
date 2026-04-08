package parchment

import "context"

// GateSeverity indicates how a gate failure affects the lifecycle transition.
type GateSeverity string

const (
	// SeverityBlocking prevents the status transition entirely.
	SeverityBlocking GateSeverity = "blocking"
	// SeverityWarning allows the transition but records an annotation.
	SeverityWarning GateSeverity = "warning"
)

// GateResult is the outcome of a quality gate check.
type GateResult struct {
	Passed   bool         `json:"passed"`
	Severity GateSeverity `json:"severity"`
	Message  string       `json:"message,omitempty"`
}

// QualityGate validates an artifact before a lifecycle transition.
// Blocking gates prevent completion. Warning gates annotate.
type QualityGate interface {
	Name() string
	Validate(ctx context.Context, art *Artifact) (GateResult, error)
}

// StubQualityGate is a configurable test double for QualityGate.
type StubQualityGate struct {
	name   string
	result GateResult
	err    error
	Calls  int
}

var _ QualityGate = (*StubQualityGate)(nil)

// NewStubQualityGate creates a gate that returns the configured result.
func NewStubQualityGate(name string, result GateResult) *StubQualityGate {
	return &StubQualityGate{name: name, result: result}
}

func (g *StubQualityGate) Name() string { return g.name }

func (g *StubQualityGate) Validate(_ context.Context, _ *Artifact) (GateResult, error) {
	g.Calls++
	return g.result, g.err
}

// SetError configures the gate to return an error.
func (g *StubQualityGate) SetError(err error) { g.err = err }
