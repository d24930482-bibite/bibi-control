package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const defaultFilename = "<script>"

// Options configures a domain-neutral Starlark run.
type Options struct {
	Filename          string
	ThreadName        string
	MaxExecutionSteps uint64
	Load              func(thread *starlark.Thread, module string) (starlark.StringDict, error)

	StagedOps   int
	RevisionRef string
	DryRun      bool
}

// Run executes program in a sandboxed Starlark thread using the supplied
// predeclared globals. It captures print output and returns normalized
// diagnostics for syntax, evaluation, budget, and context failures.
//
// A panic in any host builtin is recovered and converted into a clean RunError
// (rather than unwinding the caller), so the host's "record the run on every exit"
// invariant holds even when a builtin panics — runLoaded still records the failed
// run instead of having the panic escape past RecordScriptRun.
func Run(ctx context.Context, program []byte, globals starlark.StringDict, opts Options) (result Result, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result = Result{
		StagedOps:   opts.StagedOps,
		RevisionRef: opts.RevisionRef,
		DryRun:      opts.DryRun,
	}
	filename := opts.Filename
	if filename == "" {
		filename = defaultFilename
	}

	defer func() {
		if r := recover(); r != nil {
			cause := fmt.Errorf("script panicked: %v", r)
			result.Diagnostics = []Diagnostic{{
				Severity: SeverityError,
				Code:     "panic",
				Message:  cause.Error(),
				Detail:   cause.Error(),
				Filename: filename,
			}}
			err = &RunError{Diagnostics: result.Diagnostics, Cause: cause}
		}
	}()

	if err := ctx.Err(); err != nil {
		result.Diagnostics = diagnosticsForError(filename, err, false, true)
		return result, &RunError{Diagnostics: result.Diagnostics, Cause: err}
	}

	var output strings.Builder
	thread := &starlark.Thread{
		Name: opts.ThreadName,
		Load: opts.Load,
		Print: func(_ *starlark.Thread, msg string) {
			output.WriteString(msg)
			output.WriteByte('\n')
		},
	}

	var budgetExceeded atomic.Bool
	if opts.MaxExecutionSteps > 0 {
		thread.SetMaxExecutionSteps(opts.MaxExecutionSteps)
		thread.OnMaxSteps = func(thread *starlark.Thread) {
			budgetExceeded.Store(true)
			thread.Cancel(fmt.Sprintf("execution step budget exceeded after %d steps", opts.MaxExecutionSteps))
		}
	}

	var contextCancelled atomic.Bool
	stopCancel := context.AfterFunc(ctx, func() {
		contextCancelled.Store(true)
		thread.Cancel(ctx.Err().Error())
	})
	defer stopCancel()

	_, execErr := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, filename, program, globals)
	result.Output = output.String()
	if execErr != nil {
		result.Diagnostics = diagnosticsForError(filename, execErr, budgetExceeded.Load(), contextCancelled.Load())
		return result, &RunError{Diagnostics: result.Diagnostics, Cause: execErr}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Diagnostics = diagnosticsForError(filename, ctxErr, false, true)
		return result, &RunError{Diagnostics: result.Diagnostics, Cause: ctxErr}
	}
	return result, nil
}

func diagnosticsForError(filename string, err error, budgetExceeded, contextCancelled bool) []Diagnostic {
	if err == nil {
		return nil
	}

	code := "error"
	switch {
	case budgetExceeded:
		code = "step_budget_exceeded"
	case contextCancelled:
		code = "cancelled"
	}

	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		diagnostic := Diagnostic{
			Severity: SeverityError,
			Code:     code,
			Message:  evalErr.Error(),
			Detail:   evalErr.Backtrace(),
		}
		if !budgetExceeded && !contextCancelled {
			diagnostic.Code = "eval_error"
		}
		if len(evalErr.CallStack) > 0 {
			frame := evalErr.CallStack.At(0)
			diagnostic.Filename = frame.Pos.Filename()
			diagnostic.Line = int(frame.Pos.Line)
			diagnostic.Column = int(frame.Pos.Col)
		} else {
			diagnostic.Filename = filename
		}
		return []Diagnostic{diagnostic}
	}

	var syntaxErr syntax.Error
	if errors.As(err, &syntaxErr) {
		return []Diagnostic{{
			Severity: SeverityError,
			Code:     "syntax_error",
			Message:  syntaxErr.Msg,
			Detail:   syntaxErr.Error(),
			Filename: syntaxErr.Pos.Filename(),
			Line:     int(syntaxErr.Pos.Line),
			Column:   int(syntaxErr.Pos.Col),
		}}
	}

	if code == "error" {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "cancelled"
		}
	}
	return []Diagnostic{{
		Severity: SeverityError,
		Code:     code,
		Message:  err.Error(),
		Detail:   err.Error(),
		Filename: filename,
	}}
}
