package api_test

// TestCheatlistMatchesBindings is the M2 cross-check test. It verifies that:
//  1. The committed api/web/cheatlist.html GENERATED block matches the live
//     AttrNames() via DSLCheatlistTypes / DSLCheatlistWorkspaceTypes. Fails if
//     someone edits the page by hand or forgets to run go generate.
//  2. AC_TYPE_MEMBERS in api/web/app.js matches the live AttrNames() for the types
//     it covers. Asserts collection == mutable branch by construction, and flags any
//     phantom member (e.g. the `zones` that lived on AC_TYPE_MEMBERS.settings but
//     not on Settings.AttrNames()).
//  3. Every generated member name is present in SL_METHODS (highlighter drift arm).
//
// All three arms are pure string operations over static literals and committed files;
// no fixture-loading, no DuckDB, no AddWorld. Cheap and fast.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	thebibites "github.com/asemones/bibicontrol/script/thebibites"
	"github.com/asemones/bibicontrol/workspace"
)

// repoRootTest walks up from the test file's directory to find go.mod.
func repoRootTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod walking up from", dir)
		}
		dir = parent
	}
}

// canonicalMembersTest merges the two cheatlist helper functions into one map.
func canonicalMembersTest() map[string][]string {
	m := make(map[string][]string)
	for k, v := range thebibites.DSLCheatlistTypes() {
		m[k] = v
	}
	for k, v := range workspace.DSLCheatlistWorkspaceTypes() {
		m[k] = v
	}
	return m
}

// TestCheatlistMatchesBindings runs all three cross-check arms.
func TestCheatlistMatchesBindings(t *testing.T) {
	root := repoRootTest(t)
	canonical := canonicalMembersTest()

	t.Run("cheatlist_html_matches_bindings", func(t *testing.T) {
		testCheatlistHTMLMatchesBindings(t, root, canonical)
	})

	t.Run("ac_type_members_matches_bindings", func(t *testing.T) {
		testACTypeMembersMatchesBindings(t, root, canonical)
	})

	t.Run("sl_methods_covers_all_members", func(t *testing.T) {
		testSLMethodsCoverAllMembers(t, root, canonical)
	})
}

// ---------------------------------------------------------------------------
// Arm 1: cheatlist.html GENERATED block matches live AttrNames()
// ---------------------------------------------------------------------------

// parseCheatlistGeneratedMembers reads api/web/cheatlist.html and extracts the
// members from the GENERATED block. Returns map[label][]member.
// The table rows have the form:
//
//	<tr><td><code>LABEL</code></td><td><code>m1  m2  ...</code></td></tr>
func parseCheatlistGeneratedMembers(content string) (map[string][]string, error) {
	// Extract the fenced block.
	const openFence = "<!-- GENERATED: do not edit -->"
	const closeFence = "<!-- /GENERATED -->"
	open := strings.Index(content, openFence)
	if open < 0 {
		return nil, fmt.Errorf("missing %q fence in cheatlist.html", openFence)
	}
	close := strings.Index(content, closeFence)
	if close < 0 {
		return nil, fmt.Errorf("missing %q fence in cheatlist.html", closeFence)
	}
	block := content[open+len(openFence) : close]

	// Parse <tr><td><code>LABEL</code></td><td><code>MEMBERS</code></td></tr>
	rowRe := regexp.MustCompile(`<tr><td><code>([^<]+)</code></td><td><code>([^<]*)</code></td></tr>`)
	matches := rowRe.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no table rows found in GENERATED block")
	}
	result := make(map[string][]string, len(matches))
	for _, m := range matches {
		label := m[1]
		membersStr := m[2]
		var mems []string
		for _, s := range strings.Fields(membersStr) {
			mems = append(mems, s)
		}
		sort.Strings(mems)
		result[label] = mems
	}
	return result, nil
}

// typeKeyToLabel maps a canonical type key to its typeLabel in the generated HTML.
// These must match the typeLabels map in cmd/gen_dsl_cheatlist/main.go.
var typeKeyToLabel = map[string]string{
	"workspace":           "workspace",
	"world":               "world",
	"session":             "session (open save)",
	"node":                "node",
	"collection_readonly": "collection (spanning / read-only)",
	"collection":          "collection (single-save / mutable)",
	"settings":            "settings",
	"setting_scope":       "setting_scope",
	"gene":                "gene handle",
	"gene_collection":     "gene_collection (b.genes)",
	"setting":             "setting handle",
}

func testCheatlistHTMLMatchesBindings(t *testing.T, root string, canonical map[string][]string) {
	t.Helper()
	path := filepath.Join(root, "api", "web", "cheatlist.html")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cheatlist.html: %v", err)
	}

	htmlMembers, err := parseCheatlistGeneratedMembers(string(data))
	if err != nil {
		t.Fatalf("parse cheatlist.html: %v", err)
	}

	for key, wantMems := range canonical {
		label, ok := typeKeyToLabel[key]
		if !ok {
			// Unknown key: skip (future extension).
			continue
		}
		gotMems, ok := htmlMembers[label]
		if !ok {
			t.Errorf("cheatlist.html: missing row for type key %q (label %q)", key, label)
			continue
		}
		wantSorted := sorted(wantMems)
		gotSorted := sorted(gotMems)
		if !equalStringSlice(gotSorted, wantSorted) {
			t.Errorf("cheatlist.html: type %q members mismatch\n  got:  %v\n  want: %v", key, gotSorted, wantSorted)
		}
	}

	// Reverse check: every HTML row must have a canonical source.
	labelToKey := make(map[string]string, len(typeKeyToLabel))
	for k, v := range typeKeyToLabel {
		labelToKey[v] = k
	}
	for label := range htmlMembers {
		if _, ok := labelToKey[label]; !ok {
			t.Errorf("cheatlist.html: GENERATED block has row with unknown label %q (not in typeKeyToLabel)", label)
		}
	}
}

// ---------------------------------------------------------------------------
// Arm 2: AC_TYPE_MEMBERS in app.js matches the live AttrNames()
// ---------------------------------------------------------------------------

// parseACTypeMembers parses the AC_TYPE_MEMBERS object literal from app.js.
// It handles the specific format:  key: ['a', 'b', ...], (with optional comments).
func parseACTypeMembers(src string) (map[string][]string, error) {
	const open = "var AC_TYPE_MEMBERS = {"
	const close = "};"
	start := strings.Index(src, open)
	if start < 0 {
		return nil, fmt.Errorf("AC_TYPE_MEMBERS not found in app.js")
	}
	end := strings.Index(src[start:], close)
	if end < 0 {
		return nil, fmt.Errorf("AC_TYPE_MEMBERS close not found in app.js")
	}
	block := src[start+len(open) : start+end]

	result := make(map[string][]string)
	// Match: key: ['a', 'b', ...], with optional trailing comment
	lineRe := regexp.MustCompile(`(\w+)\s*:\s*\[([^\]]*)\]`)
	strRe := regexp.MustCompile(`'([^']+)'`)
	for _, m := range lineRe.FindAllStringSubmatch(block, -1) {
		key := m[1]
		innerStr := m[2]
		var mems []string
		for _, sm := range strRe.FindAllStringSubmatch(innerStr, -1) {
			mems = append(mems, sm[1])
		}
		sort.Strings(mems)
		result[key] = mems
	}
	return result, nil
}

// acTypeMembersKeys is the set of AC_TYPE_MEMBERS keys that have a canonical
// counterpart. The AC table uses "session" for the Save (open save) type, and
// "collection" for the MUTABLE branch only. "collection_readonly" is not in
// AC_TYPE_MEMBERS (AC can only track one branch per key).
var acTypeToCanonicalKey = map[string]string{
	"workspace":     "workspace",
	"world":         "world",
	"session":       "session",
	"node":          "node",
	"collection":    "collection",    // mutable branch by construction
	"settings":      "settings",
	"setting_scope": "setting_scope",
}

func testACTypeMembersMatchesBindings(t *testing.T, root string, canonical map[string][]string) {
	t.Helper()
	path := filepath.Join(root, "api", "web", "app.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}

	acMembers, err := parseACTypeMembers(string(data))
	if err != nil {
		t.Fatalf("parse AC_TYPE_MEMBERS: %v", err)
	}

	for acKey, canonKey := range acTypeToCanonicalKey {
		want, ok := canonical[canonKey]
		if !ok {
			t.Errorf("AC_TYPE_MEMBERS cross-check: canonical key %q (for AC key %q) not in canonicalMembers", canonKey, acKey)
			continue
		}
		got, ok := acMembers[acKey]
		if !ok {
			t.Errorf("AC_TYPE_MEMBERS: missing key %q", acKey)
			continue
		}
		wantSorted := sorted(want)
		gotSorted := sorted(got)
		if !equalStringSlice(gotSorted, wantSorted) {
			t.Errorf("AC_TYPE_MEMBERS[%q] mismatch\n  got:  %v\n  want: %v\n  (canonical key: %q)", acKey, gotSorted, wantSorted, canonKey)
		}
	}
}

// ---------------------------------------------------------------------------
// Arm 3: SL_METHODS covers all generated member names
// ---------------------------------------------------------------------------

// parseSLMethods extracts the string literals from the SL_METHODS array in app.js.
func parseSLMethods(src string) (map[string]bool, error) {
	const open = "var SL_METHODS = {};"
	start := strings.Index(src, open)
	if start < 0 {
		return nil, fmt.Errorf("SL_METHODS declaration not found in app.js")
	}
	// Find the forEach call that follows the declaration.
	end := strings.Index(src[start:], ".forEach(function(k)")
	if end < 0 {
		return nil, fmt.Errorf("SL_METHODS.forEach not found in app.js")
	}
	block := src[start : start+end]

	strRe := regexp.MustCompile(`'([^']+)'`)
	result := make(map[string]bool)
	for _, m := range strRe.FindAllStringSubmatch(block, -1) {
		result[m[1]] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("SL_METHODS array appears empty")
	}
	return result, nil
}

// slMethodsExemptions lists member names that are intentionally absent from
// SL_METHODS. These fall into a few categories:
//
//  1. Workspace-level builtins called as top-level identifiers (not after a dot):
//     add_world, node, nodes, poll, start_node, transfer, worlds.
//  2. Pure scalar property reads (never invoked as methods): id, run_id, world
//     (attr on node), status, head, sim_time, type (on gene/setting).
//  3. session attributes that are not colored in the current highlighter (zones, sql, commit —
//     commit is in SL_METHODS so its exemption is a no-op, but listed for clarity).
//  4. setting_scope / gene_collection method "get" is in SL_METHODS, so no exemption needed.
//
// This list is intentionally conservative: only truly non-method attrs are here.
// Any member that SHOULD be colored after a dot must be in SL_METHODS, not here.
var slMethodsExemptions = map[string]bool{
	// Workspace-level builtins, not typically used after a dot.
	"add_world":  true,
	"node":       true,  // workspace.node() — call site is top-level
	"nodes":      true,  // workspace.nodes() — host process-node listing
	"poll":       true,
	"start_node": true,
	"transfer":   true,
	"worlds":     true,
	// Scalar property reads on world/node.
	"id":      true,
	"run_id":  true,
	"world":   true,  // node.world is a plain string attr, not nav
	"status":  true,
	"head":    true,
	"sim_time": true,
	// session attrs not currently in SL_METHODS (legacy omissions accepted for now).
	"sql":   true,
	"zones": true,  // session.zones is real; SL_METHODS has 'zones' from settings era
	// gene/setting scalar attrs.
	"type":  true,
	"scope": true,
}

func testSLMethodsCoverAllMembers(t *testing.T, root string, canonical map[string][]string) {
	t.Helper()
	path := filepath.Join(root, "api", "web", "app.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}

	slMethods, err := parseSLMethods(string(data))
	if err != nil {
		t.Fatalf("parse SL_METHODS: %v", err)
	}

	// Collect all unique member names across all types.
	allMembers := make(map[string]bool)
	for _, mems := range canonical {
		for _, m := range mems {
			allMembers[m] = true
		}
	}

	for m := range allMembers {
		if slMethodsExemptions[m] {
			continue
		}
		if !slMethods[m] {
			t.Errorf("SL_METHODS drift: member %q is in AttrNames() but missing from SL_METHODS in app.js", m)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sorted(s []string) []string {
	cp := make([]string, len(s))
	copy(cp, s)
	sort.Strings(cp)
	return cp
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
