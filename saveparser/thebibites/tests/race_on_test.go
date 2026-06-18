//go:build race

package tests

// raceEnabled reports whether the test binary was built with -race. Used to trim
// the heavy determinism run count so the race-instrumented suite stays under the
// test-binary timeout.
const raceEnabled = true
