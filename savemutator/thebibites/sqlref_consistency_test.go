package thebibites

import (
	"path/filepath"
	"testing"
)

// TestSQLRefTagsMatchParsedValues links the two independent declarations of where
// a field lives in the save JSON: the hand-written parser extraction (e.g.
// floatAt(body, "d2Size") in saveparser) and the sqlref struct tag (e.g.
// `sqlref:"body.d2Size"`). Nothing in the type system ties them together, so a
// game save-schema change that updates one but not the other silently desyncs the
// read path (parser) from the write path (sqlref-driven mutator): the parser
// quietly reads a zero value while the mutator still resolves a path.
//
// For every writable table.column, this asserts that the value the parser
// extracted into the normalized row (tc.current) equals the value at the archive
// JSON path that the sqlref tag resolves to. A desync fails loudly here instead of
// corrupting data in production.
func TestSQLRefTagsMatchParsedValues(t *testing.T) {
	fixtures := liveSQLRefFixtureCandidates(t)

	checked := 0
	for _, fixture := range fixtures {
		fixtureName := filepath.Base(fixture.path)
		for _, tc := range fixture.cases {
			tc := tc
			checked++
			t.Run(fixtureName+"/"+tc.name, func(t *testing.T) {
				target, path, err := ResolveSQLValueRef(tc.ref)
				if err != nil {
					t.Fatalf("ResolveSQLValueRef(%s) error = %v", tc.name, err)
				}

				entry := fixture.archive.Entry(target.EntryName)
				if entry == nil {
					t.Fatalf("archive entry %q not found for %s", target.EntryName, tc.name)
				}
				if entry.JSON == nil {
					t.Fatalf("archive entry %q has no decoded JSON for %s", target.EntryName, tc.name)
				}

				got, ok, err := getJSONPath(entry.JSON, path)
				if err != nil {
					t.Fatalf("getJSONPath(%q) error = %v", path, err)
				}
				if !ok {
					t.Fatalf("sqlref tag path %q resolves to nothing in entry %q, but the parser read %#v for %s: the parser key and the sqlref tag disagree",
						path, target.EntryName, tc.current, tc.name)
				}
				if !jsonValuesEqual(got, tc.current) {
					t.Fatalf("parser-read value %#v != value %#v at sqlref tag path %q (entry %q) for %s: the parser extraction and the sqlref tag disagree on where this field lives",
						tc.current, got, path, target.EntryName, tc.name)
				}
			})
		}
	}

	if checked == 0 {
		t.Fatal("no writable SQL ref cases were checked; live fixtures produced no cases")
	}
}
