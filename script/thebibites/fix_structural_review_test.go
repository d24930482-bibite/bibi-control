package thebibites

import (
	"testing"

	"go.starlark.net/starlark"
)

// TestPendingZoneValueDottedKeyWrappedWrite is the directed check for review #1:
// a WRAPPED zone-value key that contains '.' or '[' must be written in place via
// data[key]["Value"], NOT routed through the dotted-path helper (which would split
// "foo.bar" into data["foo"]["bar"]["Value"] and either error or write the wrong
// location). The bare branch already wrote data[name] directly; this asserts the
// wrapped branch is now equally safe for such keys and preserves the wrapper's
// sibling keys.
func TestPendingZoneValueDottedKeyWrappedWrite(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)

	cases := []string{
		"foo.bar",        // contains '.'
		"arr[0]",         // contains '[' and ']'
		"a.b[1].c",       // both, mixed
		"with.dot[3]end", // adversarial mix
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			// Synthesize a wrapped value under a dotted/bracketed key, with a sibling
			// to prove the wrapper object (not just the leaf) is preserved.
			pz.data[key] = map[string]any{"Value": 1.0, "sibling": "keep-me"}

			pv := pendingZoneValuesOf(t, pz)
			const want = 0.7654321
			if err := pv.SetKey(starlark.String(key), starlark.Float(want)); err != nil {
				t.Fatalf("SetKey(%q): %v", key, err)
			}

			// The write landed in the existing wrapper object at exactly this top-level
			// key — not at a misparsed nested path.
			obj, ok := pz.data[key].(map[string]any)
			if !ok {
				t.Fatalf("after edit %q is %T, want wrapper map (key misparsed or clobbered)", key, pz.data[key])
			}
			if got, _ := obj["Value"].(float64); got != want {
				t.Errorf("wrapped %q.Value = %v, want %v", key, got, want)
			}
			if obj["sibling"] != "keep-me" {
				t.Errorf("wrapper sibling lost for %q: %v", key, obj["sibling"])
			}

			// No stray top-level keys were created by a dotted-path split (e.g. "foo"
			// or "a"). Only the original key (plus structural zone keys) should exist.
			if key == "foo.bar" {
				if _, leaked := pz.data["foo"]; leaked {
					t.Errorf("dotted key %q leaked a top-level %q entry (misparsed)", key, "foo")
				}
			}
			if key == "a.b[1].c" {
				if _, leaked := pz.data["a"]; leaked {
					t.Errorf("dotted key %q leaked a top-level %q entry (misparsed)", key, "a")
				}
			}

			// Read-back through Get observes the edit at the same key.
			gv, found, err := pv.Get(starlark.String(key))
			if err != nil || !found {
				t.Fatalf("Get(%q): found=%v err=%v", key, found, err)
			}
			if got := mustFloat(t, gv); got != want {
				t.Errorf("read-back %q = %v, want %v", key, got, want)
			}
		})
	}
}

// TestPendingZoneValueDottedKeyBareWrite confirms the BARE branch (the one the
// review notes already guards against the dotted-key risk) writes data[key]
// directly for a dotted/bracketed key — the parity baseline the wrapped branch now
// matches.
func TestPendingZoneValueDottedKeyBareWrite(t *testing.T) {
	ls := loadFixture(t)
	pz := clonePendingZone(t, ls, 0)
	const key = "weird.key[2]"

	pz.data[key] = 1.0 // bare numeric under a dotted key
	pv := pendingZoneValuesOf(t, pz)
	const want = 3.5
	if err := pv.SetKey(starlark.String(key), starlark.Float(want)); err != nil {
		t.Fatalf("SetKey(%q) bare dotted: %v", key, err)
	}
	got, ok := pz.data[key].(float64)
	if !ok {
		t.Fatalf("bare dotted write: %q is %T, want float64", key, pz.data[key])
	}
	if got != want {
		t.Errorf("bare dotted %q = %v, want %v", key, got, want)
	}
	if _, leaked := pz.data["weird"]; leaked {
		t.Errorf("bare dotted key %q leaked a top-level %q entry", key, "weird")
	}
}
