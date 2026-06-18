package thebibites

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// parallelParseThreshold is the entry count below which the parse loop runs
// sequentially. Spinning up a worker pool for a handful of entries costs more
// than it saves, and the sequential branch keeps tiny archives simple.
const parallelParseThreshold = 2

// parseEntries computes the parseResult for every entry and applies the results
// to the archive in original entry order.
//
// DETERMINISM CONTRACT: the produced Archive must be byte-for-byte identical to
// the old sequential `for i := range archive.Entries { archive.parseEntry(...) }`
// loop for the same input. The only order-sensitive output is produced by
// applyParseResult (it appends to a.Bibites/Eggs/Pheromones/Diagnostics),
// which here runs single-threaded over results[0..n-1] in the same order the old
// loop ran. The parallel section only fills independent results[i] slots and the
// per-entry Entry fields (entry.JSON / entry.HasUTF8BOM) for the index it owns,
// so it cannot affect ordering or content. applyParseResult is never called from
// a worker goroutine.
func (a *Archive) parseEntries() {
	n := len(a.Entries)
	if n == 0 {
		return
	}

	if n < parallelParseThreshold {
		results := make([]parseResult, n)
		for i := range a.Entries {
			results[i] = parseEntryPayload(&a.Entries[i])
		}
		a.presizeFromResults(results)
		for i := range results {
			a.applyParseResult(results[i])
		}
		return
	}

	// parseEntryPayload is pure per entry: a fresh parserContext per call, no
	// shared package state written, and the only mutations are to results[i] and
	// the entry's own &a.Entries[i] slot. Because each index is claimed by exactly
	// one worker (via the atomic counter), those writes are race-free.
	results := make([]parseResult, n)

	// Worker count is bounded by GOMAXPROCS and never exceeds the entry count, so
	// we never spawn one goroutine per entry. On a GOMAXPROCS=1 box this degrades
	// to a single worker (effectively sequential).
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}

	var next int64 = -1
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= n {
					return
				}
				results[i] = parseEntryPayload(&a.Entries[i])
			}
		}()
	}
	wg.Wait()

	// Pre-size the destination slices from the assembled results so the apply
	// loop appends into already-allocated backing arrays instead of repeatedly
	// doubling+copying them (the dominant GC cost on large saves). This only sets
	// capacity; it does not change append order or content.
	a.presizeFromResults(results)

	// Apply strictly in index order so the append order matches the old loop.
	for i := range results {
		a.applyParseResult(results[i])
	}
}

// presizeFromResults grows a's append-target slices once to fit every result, so
// the subsequent applyParseResult loop never reallocates them. Counting is exact:
// each result contributes at most one Bibite/Egg and a known number of
// Pheromones/Diagnostics.
func (a *Archive) presizeFromResults(results []parseResult) {
	var bibites, eggs, pheromones, diagnostics int
	for i := range results {
		r := &results[i]
		if r.Bibite != nil {
			bibites++
		}
		if r.Egg != nil {
			eggs++
		}
		pheromones += len(r.Pheromones)
		diagnostics += len(r.Diagnostics)
	}
	if bibites > 0 {
		a.Bibites = growCap(a.Bibites, bibites)
	}
	if eggs > 0 {
		a.Eggs = growCap(a.Eggs, eggs)
	}
	if pheromones > 0 {
		a.Pheromones = growCap(a.Pheromones, pheromones)
	}
	if diagnostics > 0 {
		a.Diagnostics = growCap(a.Diagnostics, diagnostics)
	}
}

// growCap returns s with capacity for at least n additional elements, reusing
// the existing backing array when it already has room. Length is unchanged.
func growCap[T any](s []T, n int) []T {
	if cap(s)-len(s) >= n {
		return s
	}
	grown := make([]T, len(s), len(s)+n)
	copy(grown, s)
	return grown
}
