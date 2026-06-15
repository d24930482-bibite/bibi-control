package thebibites

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScalarRedundancyListsStayLive guards the hand-maintained typedEntityPrefixes
// and scalarKeepSuffixes used by isRedundantScalar. Those lists encode "what the
// typed tables already model" as string prefixes/suffixes — knowledge that
// duplicates the parser and the sqlref tags. Deriving them from a formal
// modeled-path set is deferred (no such set exists yet; see the maintainability
// handoff). Until then this test keeps the lists from silently rotting as the save
// format churns:
//
//   - Boundary cleanliness: under a typed-entity prefix, the only scalars that may
//     survive into json_scalars are the declared keep-suffixes. An unexpected
//     survivor means a modeled field is being double-stored as an EAV row, or the
//     keep list is stale.
//   - Keep-suffix liveness: every scalarKeepSuffix must still be produced by the
//     fixtures. A keep-suffix that no longer appears means the game renamed/removed
//     that field and the entry is dead config.
//
// New fields added under a typed entity are intentionally NOT caught here (they are
// dropped by isRedundantScalar before reaching json_scalars); the schema
// fingerprint test in ./tests catches those by walking raw JSON.
func TestScalarRedundancyListsStayLive(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "testdata", "saves", "the-bibites", "*.zip"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no save fixtures found")
	}

	keepSuffixSeen := make(map[string]bool, len(scalarKeepSuffixes))
	for _, path := range fixtures {
		archive, err := ParseFile(path, nil)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", filepath.Base(path), err)
		}
		out := ExtractTables("redundancy-guard", archive)
		for _, row := range out.JSONScalars {
			prefix, underTyped := matchedTypedPrefix(row.Path)
			if !underTyped {
				continue
			}
			suffix, kept := matchedKeepSuffix(row.Path)
			if !kept {
				t.Errorf("scalar %q survived as an EAV row under typed prefix %q but matches no keep-suffix: the typed model and the EAV walk disagree (update scalarKeepSuffixes or the typed table)", row.Path, prefix)
				continue
			}
			keepSuffixSeen[suffix] = true
		}
	}

	for _, suffix := range scalarKeepSuffixes {
		if !keepSuffixSeen[suffix] {
			t.Errorf("keep-suffix %q is never produced by any fixture: it is dead config (the game likely renamed/removed that field)", suffix)
		}
	}
}

func matchedTypedPrefix(path string) (string, bool) {
	for _, p := range typedEntityPrefixes {
		if strings.HasPrefix(path, p) {
			return p, true
		}
	}
	return "", false
}

func matchedKeepSuffix(path string) (string, bool) {
	for _, s := range scalarKeepSuffixes {
		if strings.HasSuffix(path, s) {
			return s, true
		}
	}
	return "", false
}
