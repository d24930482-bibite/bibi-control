package tests

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// goldenFingerprintPath is the checked-in baseline of every distinct JSON
// path-shape the save format emits across all fixtures. Array indices are
// collapsed to [*] so the baseline describes structure, not content. It lives
// beside the test (not under testdata/, which .gitignore excludes) so it is
// tracked and travels with the repo.
var goldenFingerprintPath = "save_schema_fingerprint.golden"

// TestSaveSchemaFingerprint is the schema-drift early warning for The Bibites'
// frequently-changing save format. It fingerprints the structural shape of every
// fixture save (entry kind + JSON path-shape of every leaf) and diffs the union
// against a checked-in baseline.
//
// When the game ships a new save version, drop its save into testdata/saves and
// run this test: it prints exactly which path-shapes APPEARED (fields the game
// added) and which DISAPPEARED (fields renamed/removed) relative to the baseline.
// That localizes which parser/sqlref code needs attention before a silent desync
// reaches production. Regenerate the baseline with UPDATE_SCHEMA_FINGERPRINT=1
// once the change has been reviewed and the parser updated to match.
func TestSaveSchemaFingerprint(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.zip"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no save fixtures found in %s", fixtureDir)
	}

	shapes := make(map[string]struct{})
	for _, path := range paths {
		archive, err := tb.ParseFile(path, nil)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", filepath.Base(path), err)
		}
		for i := range archive.Entries {
			entry := &archive.Entries[i]
			if entry.JSON == nil {
				continue
			}
			collectSchemaShapes(string(entry.Kind), "", entry.JSON, shapes)
		}
	}

	got := make([]string, 0, len(shapes))
	for shape := range shapes {
		got = append(got, shape)
	}
	sort.Strings(got)
	current := strings.Join(got, "\n") + "\n"

	if os.Getenv("UPDATE_SCHEMA_FINGERPRINT") != "" {
		if err := os.WriteFile(goldenFingerprintPath, []byte(current), 0o644); err != nil {
			t.Fatalf("write golden fingerprint: %v", err)
		}
		t.Logf("updated %s with %d path-shapes", goldenFingerprintPath, len(got))
		return
	}

	wantBytes, err := os.ReadFile(goldenFingerprintPath)
	if err != nil {
		t.Fatalf("read golden fingerprint (%s): %v\nrun with UPDATE_SCHEMA_FINGERPRINT=1 to create it", goldenFingerprintPath, err)
	}
	want := strings.Split(strings.TrimRight(string(wantBytes), "\n"), "\n")
	wantSet := make(map[string]struct{}, len(want))
	for _, shape := range want {
		if shape != "" {
			wantSet[shape] = struct{}{}
		}
	}

	var added, removed []string
	for shape := range shapes {
		if _, ok := wantSet[shape]; !ok {
			added = append(added, shape)
		}
	}
	for shape := range wantSet {
		if _, ok := shapes[shape]; !ok {
			removed = append(removed, shape)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	if len(added) == 0 && len(removed) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("save schema fingerprint drifted from the baseline.\n")
	b.WriteString("This usually means the game save format changed. Review each shape,\n")
	b.WriteString("update the parser/sqlref tags to match, then regenerate the baseline\n")
	b.WriteString("with UPDATE_SCHEMA_FINGERPRINT=1.\n")
	if len(added) > 0 {
		b.WriteString("\nADDED (present in fixtures, missing from baseline — new game fields?):\n")
		for _, shape := range added {
			b.WriteString("  + " + shape + "\n")
		}
	}
	if len(removed) > 0 {
		b.WriteString("\nREMOVED (in baseline, absent from fixtures — renamed/removed fields?):\n")
		for _, shape := range removed {
			b.WriteString("  - " + shape + "\n")
		}
	}
	t.Fatal(b.String())
}

// collectSchemaShapes records the structural path-shape of every JSON leaf under
// value, collapsing array indices to [*]. Empty containers are recorded as a leaf
// so a field that loses all elements/keys still registers a shape.
func collectSchemaShapes(kind, prefix string, value any, out map[string]struct{}) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			out[kind+"|"+prefix+"{}"] = struct{}{}
			return
		}
		for key, child := range v {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			collectSchemaShapes(kind, next, child, out)
		}
	case []any:
		if len(v) == 0 {
			out[kind+"|"+prefix+"[]"] = struct{}{}
			return
		}
		for _, item := range v {
			collectSchemaShapes(kind, prefix+"[*]", item, out)
		}
	default:
		out[kind+"|"+prefix] = struct{}{}
	}
}
