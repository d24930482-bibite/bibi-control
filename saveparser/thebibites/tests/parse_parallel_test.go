package tests

import (
	"bytes"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// largestFixture is the 3.1MB / 1027-bibite save the perf work targets. It has
// enough independent zip entries to exercise the parallel parse + give the race
// detector real concurrency to inspect.
const largestFixture = "autosave_20260301021357.zip"

// TestParseFileDeterministicAcrossRuns is the central correctness gate for the
// parallel parse. The parallel entry-parse fills independent results slots and
// then applies them single-threaded in index order, so N parallel parses of the
// same input must produce an identical Archive every time. If the ordered-apply
// ever races or reorders, runs diverge and this fails.
//
// The 3.1MB fixture parse allocates heavily; under the race detector (10-20x
// instrumentation) a large run count blows the test-binary timeout, so the run
// count is trimmed when -race/-short is active. A handful of parallel parses is
// plenty for the race detector to observe a violation, and the byte-identity
// serialization check still runs on the reduced set.
func TestParseFileDeterministicAcrossRuns(t *testing.T) {
	path := filepath.Join(fixtureDir, largestFixture)

	want, err := tb.ParseFile(path, nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() reference error = %v", err)
	}
	wantBytes := archiveBytes(t, want)

	runs := 20
	if testing.Short() || raceEnabled {
		runs = 4
	}
	for i := 0; i < runs; i++ {
		got, err := tb.ParseFile(path, nil)
		if err != nil {
			t.Fatalf("tb.ParseFile() run %d error = %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d produced an Archive that differs from the reference parse", i)
		}
		// Stricter: the order-driven ZIP serialization must be byte-identical too.
		if gotBytes := archiveBytes(t, got); !bytes.Equal(gotBytes, wantBytes) {
			t.Fatalf("run %d serialized to %d bytes, reference %d bytes (order/content diverged)", i, len(gotBytes), len(wantBytes))
		}
	}
}

// TestParseFileParallelMatchesSequential pins the determinism contract against a
// forced-sequential reference: parsing under GOMAXPROCS=1 takes the single-worker
// path, and that must equal the multi-core parallel parse byte-for-byte.
func TestParseFileParallelMatchesSequential(t *testing.T) {
	path := filepath.Join(fixtureDir, largestFixture)

	prev := runtime.GOMAXPROCS(1)
	seq, err := tb.ParseFile(path, nil)
	runtime.GOMAXPROCS(prev)
	if err != nil {
		t.Fatalf("sequential tb.ParseFile() error = %v", err)
	}

	if runtime.GOMAXPROCS(0) < 2 {
		// Single-core box: both paths are the same code; the across-runs test
		// already covers determinism here.
		t.Skip("GOMAXPROCS < 2; parallel and sequential paths coincide")
	}

	par, err := tb.ParseFile(path, nil)
	if err != nil {
		t.Fatalf("parallel tb.ParseFile() error = %v", err)
	}

	if !reflect.DeepEqual(par, seq) {
		t.Fatalf("parallel parse differs from forced-sequential parse")
	}
	if !bytes.Equal(archiveBytes(t, par), archiveBytes(t, seq)) {
		t.Fatalf("parallel parse serialized differently from forced-sequential parse")
	}
}

// BenchmarkParseFileLargest measures ParseFile wall-time on the 3.1MB fixture so
// before/after parse time is comparable. Baseline (sequential) was ~2.12s.
func BenchmarkParseFileLargest(b *testing.B) {
	path := filepath.Join(fixtureDir, largestFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		archive, err := tb.ParseFile(path, nil)
		if err != nil {
			b.Fatalf("tb.ParseFile() error = %v", err)
		}
		if len(archive.Bibites) == 0 {
			b.Fatalf("expected parsed bibites")
		}
	}
}

func archiveBytes(t testing.TB, archive *tb.Archive) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := tb.WriteArchiveTo(&buf, archive); err != nil {
		t.Fatalf("tb.WriteArchiveTo() error = %v", err)
	}
	return buf.Bytes()
}
