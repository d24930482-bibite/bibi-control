package script

import (
	"context"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// boom is a host builtin that panics, standing in for a host builtin that
// crashes mid-run after the script has already printed diagnostics.
func boomBuiltin() starlark.StringDict {
	return starlark.StringDict{
		"boom": starlark.NewBuiltin("boom", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			panic("host builtin exploded")
		}),
	}
}

// TestRunPanicPreservesOutput is the #5 regression: a script that prints before a
// host builtin panics must still have its captured output recorded. Before the fix
// the recovery defer ran before `output` was declared, so result.Output was empty
// on panic and the operator lost the diagnostics printed up to the crash.
func TestRunPanicPreservesOutput(t *testing.T) {
	result, err := Run(context.Background(), []byte(`
print("before the crash")
print("step = %d" % 2)
boom()
print("after the crash")
`), boomBuiltin(), Options{Filename: "panic.star"})

	if err == nil {
		t.Fatalf("Run() error = nil, want panic-recovered RunError")
	}
	if result.Output != "before the crash\nstep = 2\n" {
		t.Fatalf("result.Output = %q, want the lines printed before the panic", result.Output)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one panic diagnostic", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "panic" {
		t.Fatalf("diagnostic code = %q, want panic", diagnostic.Code)
	}
	if !strings.Contains(diagnostic.Message, "host builtin exploded") {
		t.Fatalf("diagnostic message = %q, want the panic cause", diagnostic.Message)
	}
}

// TestRunNormalPathStillCapturesOutput guards against a double-capture or lost
// assignment regression from moving the output builder: the non-panic path still
// reports the full captured output exactly once.
func TestRunNormalPathStillCapturesOutput(t *testing.T) {
	result, err := Run(context.Background(), []byte(`
print("line one")
print("line two")
`), starlark.StringDict{}, Options{Filename: "ok.star"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "line one\nline two\n" {
		t.Fatalf("result.Output = %q, want both lines once", result.Output)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", result.Diagnostics)
	}
}
