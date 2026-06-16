package script

// Result is the domain-neutral result of a Starlark program run.
type Result struct {
	Output      string
	Diagnostics []Diagnostic
	StagedOps   int
	RevisionRef string
	DryRun      bool
}

// DiagnosticSeverity describes the severity of a script diagnostic.
type DiagnosticSeverity string

const (
	SeverityError DiagnosticSeverity = "error"
)

// Diagnostic is a normalized syntax, evaluation, cancellation, or host error.
type Diagnostic struct {
	Severity DiagnosticSeverity
	Code     string
	Message  string
	Detail   string
	Filename string
	Line     int
	Column   int
}

// RunError wraps the underlying interpreter error while preserving diagnostics
// that are stable enough to show directly to users.
type RunError struct {
	Diagnostics []Diagnostic
	Cause       error
}

func (e *RunError) Error() string {
	if len(e.Diagnostics) > 0 && e.Diagnostics[0].Message != "" {
		return e.Diagnostics[0].Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "script run failed"
}

func (e *RunError) Unwrap() error {
	return e.Cause
}
