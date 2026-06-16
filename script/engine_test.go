package script

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

func TestRunCapturesPrintOutput(t *testing.T) {
	result, err := Run(context.Background(), []byte(`
print("hello")
print("answer = %d" % 42)
`), starlark.StringDict{}, Options{
		Filename:    "hello.star",
		StagedOps:   3,
		RevisionRef: "sha256:abc",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "hello\nanswer = 42\n" {
		t.Fatalf("Output = %q, want captured print lines", result.Output)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", result.Diagnostics)
	}
	if result.StagedOps != 3 || result.RevisionRef != "sha256:abc" || !result.DryRun {
		t.Fatalf("Result metadata = %#v", result)
	}
}

func TestRunStepBudgetAbortsBoundedLoop(t *testing.T) {
	result, err := Run(context.Background(), []byte(`
def spin():
    total = 0
    for i in range(1000000):
        total += i

spin()
`), nil, Options{
		Filename:          "budget.star",
		MaxExecutionSteps: 100,
	})
	if err == nil {
		t.Fatalf("Run() error = nil, want step budget failure")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error type = %T, want *RunError", err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one diagnostic", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "step_budget_exceeded" {
		t.Fatalf("diagnostic code = %q, want step_budget_exceeded", diagnostic.Code)
	}
	if !strings.Contains(diagnostic.Message, "execution step budget exceeded") {
		t.Fatalf("diagnostic message = %q, want budget message", diagnostic.Message)
	}
	if diagnostic.Filename != "budget.star" || diagnostic.Line == 0 {
		t.Fatalf("diagnostic location = %s:%d:%d, want source location", diagnostic.Filename, diagnostic.Line, diagnostic.Column)
	}
	if len(runErr.Diagnostics) != 1 || runErr.Diagnostics[0].Code != diagnostic.Code {
		t.Fatalf("RunError diagnostics = %#v, want result diagnostics", runErr.Diagnostics)
	}
}

func TestRunSyntaxErrorDiagnostic(t *testing.T) {
	result, err := Run(context.Background(), []byte(`def broken(:`), nil, Options{Filename: "broken.star"})
	if err == nil {
		t.Fatalf("Run() error = nil, want syntax error")
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one diagnostic", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "syntax_error" {
		t.Fatalf("diagnostic code = %q, want syntax_error", diagnostic.Code)
	}
	if diagnostic.Filename != "broken.star" || diagnostic.Line != 1 || diagnostic.Column == 0 {
		t.Fatalf("diagnostic location = %s:%d:%d, want broken.star:1:<col>", diagnostic.Filename, diagnostic.Line, diagnostic.Column)
	}
	if diagnostic.Message == "" || !strings.Contains(diagnostic.Detail, "broken.star:1") {
		t.Fatalf("diagnostic = %#v, want message and positioned detail", diagnostic)
	}
}

func TestRunEvalErrorDiagnosticIncludesBacktrace(t *testing.T) {
	result, err := Run(context.Background(), []byte(`
def fail():
    return 1 // 0

fail()
`), nil, Options{Filename: "eval.star"})
	if err == nil {
		t.Fatalf("Run() error = nil, want eval error")
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one diagnostic", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "eval_error" {
		t.Fatalf("diagnostic code = %q, want eval_error", diagnostic.Code)
	}
	if !strings.Contains(diagnostic.Message, "division by zero") {
		t.Fatalf("diagnostic message = %q, want division by zero", diagnostic.Message)
	}
	if !strings.Contains(diagnostic.Detail, "Traceback") || !strings.Contains(diagnostic.Detail, "eval.star:3") {
		t.Fatalf("diagnostic detail = %q, want backtrace with source line", diagnostic.Detail)
	}
	if diagnostic.Filename != "eval.star" || diagnostic.Line != 3 {
		t.Fatalf("diagnostic location = %s:%d:%d, want eval.star:3:<col>", diagnostic.Filename, diagnostic.Line, diagnostic.Column)
	}
}
