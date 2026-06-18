package tests

import (
	"path/filepath"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// BenchmarkParseAndExtractLargest measures the full hot path (decode + parse +
// normalize) for the 3.1MB largest fixture. It is the perf witness for H4: the
// scalar-removal win shows up here as fewer allocs/op and lower wall time under
// GC pressure. Run with -benchmem.
func BenchmarkParseAndExtractLargest(b *testing.B) {
	path := filepath.Join(fixtureDir, "autosave_20260301021357.zip")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		archive, err := tb.ParseFile(path, nil)
		if err != nil {
			b.Fatalf("ParseFile: %v", err)
		}
		save := tb.ExtractTables("bench", archive)
		if len(save.Bibites) == 0 {
			b.Fatalf("no bibites extracted")
		}
	}
}
