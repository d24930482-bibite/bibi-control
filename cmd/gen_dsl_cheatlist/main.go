// gen_dsl_cheatlist generates api/web/cheatlist.html from the Go bindings'
// static AttrNames(). Run via:
//
//	go generate ./saveparser/thebibites
//
// (the //go:generate directive lives in saveparser/thebibites/normalize_types.go
// next to the existing gen_thebibites_schema directive).
//
// The generator builds the canonical type→member table by calling
// DSLCheatlistTypes() (script/thebibites) and DSLCheatlistWorkspaceTypes()
// (workspace), merges them, and replaces the <!-- GENERATED --> fenced block
// inside api/web/cheatlist.html with the rendered member graph. The prose/recipes
// section outside the fence is hand-written and never touched by the generator.
//
// Running this twice in a row produces the same file (idempotent).
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	thebibites "github.com/asemones/bibicontrol/script/thebibites"
	"github.com/asemones/bibicontrol/workspace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen_dsl_cheatlist: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	// Gather canonical member lists from the live bindings.
	members := canonicalMembers()

	// Build the generated HTML block.
	generated := renderGeneratedBlock(members)

	// Read the current cheatlist.html (create template if missing).
	outPath := filepath.Join(root, "api", "web", "cheatlist.html")
	current, err := os.ReadFile(outPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read cheatlist.html: %w", err)
	}
	if os.IsNotExist(err) || len(current) == 0 {
		current = []byte(cheatlistTemplate)
	}

	// Replace the fenced block.
	updated, err := replaceFencedBlock(string(current), generated)
	if err != nil {
		return fmt.Errorf("replace fenced block: %w", err)
	}

	if err := os.WriteFile(outPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write cheatlist.html: %w", err)
	}
	fmt.Printf("gen_dsl_cheatlist: wrote %s\n", outPath)
	return nil
}

// repoRoot walks up from the working directory until it finds go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

// canonicalMembers returns the full merged type→members map from the live
// Go AttrNames(). Keys are the JS AC_TYPE_MEMBERS key names.
func canonicalMembers() map[string][]string {
	m := make(map[string][]string)
	for k, v := range thebibites.DSLCheatlistTypes() {
		m[k] = v
	}
	for k, v := range workspace.DSLCheatlistWorkspaceTypes() {
		m[k] = v
	}
	return m
}

const genOpen = "<!-- GENERATED: do not edit -->"
const genClose = "<!-- /GENERATED -->"

// replaceFencedBlock replaces the content between the GENERATED fences in src
// with the given generated string. Returns an error if the fences are missing.
func replaceFencedBlock(src, generated string) (string, error) {
	open := strings.Index(src, genOpen)
	if open < 0 {
		return "", fmt.Errorf("missing %q fence in cheatlist.html", genOpen)
	}
	close := strings.Index(src, genClose)
	if close < 0 {
		return "", fmt.Errorf("missing %q fence in cheatlist.html", genClose)
	}
	if close < open {
		return "", fmt.Errorf("close fence appears before open fence in cheatlist.html")
	}
	// Include the opening fence tag, replace through (but not including) the close tag.
	return src[:open] + genOpen + "\n" + generated + genClose + src[close+len(genClose):], nil
}

// typeOrder is the display order for DSL types in the generated table.
var typeOrder = []string{
	"workspace",
	"world",
	"session",
	"node",
	"collection_readonly",
	"collection",
	"settings",
	"setting_scope",
	"gene",
	"gene_collection",
	"setting",
}

// typeLabels provides a human-readable label for each type key.
var typeLabels = map[string]string{
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

// renderGeneratedBlock emits the HTML for the type→members table, suitable for
// insertion between the GENERATED fences.
func renderGeneratedBlock(members map[string][]string) string {
	var b bytes.Buffer
	b.WriteString("<!-- Members generated from Go AttrNames() by cmd/gen_dsl_cheatlist. -->\n")
	b.WriteString("<!-- Do not edit between the GENERATED fences; run go generate ./saveparser/thebibites to regenerate. -->\n")
	b.WriteString("<table class=\"cl-members\">\n")
	b.WriteString("<thead><tr><th>DSL type</th><th>Members (from AttrNames)</th></tr></thead>\n")
	b.WriteString("<tbody>\n")

	for _, key := range typeOrder {
		mems, ok := members[key]
		if !ok {
			continue
		}
		sorted := make([]string, len(mems))
		copy(sorted, mems)
		sort.Strings(sorted)
		label := typeLabels[key]
		if label == "" {
			label = key
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
			htmlEsc(label), htmlEsc(strings.Join(sorted, "  ")))
	}

	// Any type in the map not in typeOrder (future additions) gets appended.
	inOrder := make(map[string]bool, len(typeOrder))
	for _, k := range typeOrder {
		inOrder[k] = true
	}
	extras := make([]string, 0)
	for k := range members {
		if !inOrder[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		mems := members[key]
		sorted := make([]string, len(mems))
		copy(sorted, mems)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
			htmlEsc(key), htmlEsc(strings.Join(sorted, "  ")))
	}

	b.WriteString("</tbody>\n")
	b.WriteString("</table>\n")
	return b.String()
}

// htmlEsc escapes HTML special characters.
func htmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&#34;")
	return s
}

// cheatlistTemplate is the initial cheatlist.html written when no file exists.
// The GENERATED fences wrap the machine-owned block; everything outside is prose.
const cheatlistTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>bibicontrol — DSL cheatlist</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 900px; margin: 2rem auto; padding: 0 1rem; }
h1, h2, h3 { margin-top: 2rem; }
code { background: #f4f4f4; padding: 0 0.25em; border-radius: 3px; font-size: 0.9em; }
pre { background: #f4f4f4; padding: 1rem; overflow-x: auto; border-radius: 4px; }
pre code { background: none; padding: 0; }
table.cl-members { border-collapse: collapse; width: 100%; }
table.cl-members th, table.cl-members td { text-align: left; padding: 0.35rem 0.6rem; border: 1px solid #ddd; vertical-align: top; }
table.cl-members thead { background: #f0f0f0; }
.note { background: #fffbe6; border-left: 4px solid #f5c400; padding: 0.5rem 1rem; margin: 1rem 0; }
a.back { display: inline-block; margin-bottom: 1.5rem; }
</style>
</head>
<body>
<a class="back" href="/">&#8592; back to notebook</a>
<h1>bibicontrol DSL — quick reference</h1>
<p>
  This page documents the <strong>settled</strong> object DSL surface as of the merged
  M-tickets (M1 spanning kinds, M4 gene keyed-lookup, M5 brain nav).
  The type&rarr;members table below is <em>generated</em> from the Go bindings'
  <code>AttrNames()</code>; run <code>go generate ./saveparser/thebibites</code> to regenerate.
</p>

<h2>Type &rarr; Members</h2>

<!-- GENERATED: do not edit -->
<!-- /GENERATED -->

<h2>Read / write / iterate distinctions</h2>

<h3>Gene access (M4)</h3>
<p><strong>Note: <code>gene("X")</code> was removed in M4.</strong> Use these instead:</p>
<ul>
  <li><code>b.genes["X"]</code> &mdash; loud <code>KeyError</code> on miss (use when the gene must exist)</li>
  <li><code>b.genes.get("X")</code> or <code>b.genes.get("X", default)</code> &mdash; tolerant, returns <code>None</code>/<code>default</code> on miss</li>
  <li><code>b.genes["X"] = v</code> &mdash; write (stages a mutation)</li>
  <li><code>for g in b.genes</code> &mdash; iterate all genes; <code>len(b.genes)</code></li>
  <li>Gene handle: <code>g.name</code>, <code>g.type</code>, <code>g.value</code></li>
</ul>

<h3>Collection branches</h3>
<p>
  A <strong>spanning collection</strong> (e.g. <code>workspace.bibites</code>, <code>world.nodes</code>)
  is <strong>read-only aggregate-only</strong>: only <code>count / sum / mean / median / min / max /
  quantile / where / group_by</code> are available &mdash; no <code>set</code>, <code>set_expr</code>,
  or <code>delete</code>.
</p>
<p>
  A <strong>single-save collection</strong> (e.g. <code>s.bibites</code> from <code>world.open()</code>)
  also exposes <code>set / set_expr / delete</code>.
  Mutation is per-save by construction; call <code>s.commit(path)</code> to write back.
</p>
<p>
  <strong>Which save(s) you read</strong> follows from where you start:
  <code>s = world.open()</code> &rarr; the <strong>current head save</strong> (one working
  partition, the only <strong>writable</strong> scope);
  <code>world.bibites</code> &rarr; that world's <strong>whole retained history</strong>
  (every committed revision);
  <code>workspace.bibites</code> &rarr; <strong>all worlds' history</strong>.
  Both spanning scopes read <strong>committed history only</strong> &mdash; never an
  uncommitted working save &mdash; and expose <code>world_id</code> / <code>sim_time</code>
  (the per-revision time axis) as columns.
</p>

<h3>workspace.nodes asymmetry (M1)</h3>
<div class="note">
  <strong><code>workspace.nodes</code> is NOT the brain-node aggregate.</strong>
  It is the host process-node listing (the cluster node registry).
  To query brain nodes across all worlds use <code>world.nodes</code> (per world)
  or reach them via <code>workspace.synapses</code> / <code>workspace.genes</code>.
  See <code>workspace/automation.go:88-91</code> for the authoritative comment.
</div>

<h3>Brain-nav attrs (M5)</h3>
<p>On a synapse element (<code>b.synapses[i]</code>): <code>syn.source</code>, <code>syn.target</code>
&rarr; the source/target node <code>ArrayElement</code>.</p>
<p>On a node element (<code>b.nodes[i]</code>): <code>n.inputs</code>, <code>n.outputs</code>
&rarr; read-only sub-collection of incoming/outgoing synapses.</p>
<p><em>These nav attrs are conditional: <code>source</code>/<code>target</code> only on synapses;
<code>inputs</code>/<code>outputs</code> only on nodes.</em></p>

<h3>Settings</h3>
<ul>
  <li><code>save.settings.simulation["SettingName"]</code> &mdash; loud read</li>
  <li><code>save.settings.simulation.get("SettingName")</code> &mdash; tolerant read</li>
  <li><code>save.settings.simulation["SettingName"].set(value)</code> &mdash; staged write</li>
  <li><code>save.settings.material("MatName")["SettingName"]</code> &mdash; material scope</li>
</ul>

<h3>Data-derived surfaces (per save-format version)</h3>
<p>
  The per-entity column list (<code>b.attr</code> names on an <code>Entity</code>/
  <code>ArrayElement</code>) comes from the generated metadata that tracks the Bibites
  save format. It changes between Bibites versions; use
  <code>dir(b)</code> in the notebook to see the current live column list.
  The cheatlist does not freeze this list (see the save-format-churn policy).
</p>

<h2>Copy-paste recipes</h2>

<h3>Open a world and iterate bibites</h3>
<pre><code>w = workspace.world("My World")
s = w.open()
for b in s.bibites:
    print(b.desc, b.genes["Diet"].value)
s.commit("/path/to/output.zip")
</code></pre>

<h3>Bulk mutation with .where().set()</h3>
<pre><code>s = workspace.world("My World").open()
n = s.bibites.where("health &lt; 0.5").set("energy", 1.0)
print(n, "bibites updated")
s.commit("/path/to/output.zip")
</code></pre>

<h3>Spanning aggregate (all worlds)</h3>
<pre><code># All bibites across all worlds' history.
result = workspace.bibites.where("dead = false").count()
print(result)

# Per-world breakdown.
for row in workspace.bibites.group_by("world_id").count():
    print(row)
</code></pre>

<h3>Per-world brain stats (M1/M5)</h3>
<pre><code># All-worlds node aggregate via world.nodes (NOT workspace.nodes).
w = workspace.world("My World")
for node in w.nodes.where("node_type = 'sigmoid'"):
    print(node)

# Synapse nav (M5).
s = w.open()
for b in s.bibites:
    for syn in b.synapses:
        src = syn.source   # the source node ArrayElement
        tgt = syn.target   # the target node ArrayElement
        print(src.node_desc, "->", tgt.node_desc, "weight:", syn.weight)
</code></pre>

<h3>Gene keyed-lookup (M4)</h3>
<pre><code>s = workspace.world("My World").open()
for b in s.bibites:
    diet = b.genes.get("Diet")       # tolerant: None on miss
    if diet is not None:
        b.genes["Diet"] = diet.value * 1.1  # loud write
s.commit("/path/to/output.zip")
</code></pre>

<h3>start_node + wait</h3>
<pre><code>w = workspace.add_world("/path/to/save.zip", "run1")
nd = workspace.start_node(world_id=w.id, sim_steps=1000)
nd.wait()
print(nd.status)
</code></pre>

<h3>transfer</h3>
<pre><code>src = workspace.world("Source").open()
dst = workspace.world("Dest").open()
sel = src.bibites.where("health &gt; 0.8")
workspace.transfer(sel, dst)
dst.commit("/path/to/dest_out.zip")
</code></pre>

<h2>query (power-user SQL escape hatch)</h2>
<pre><code># Raw DuckDB SQL — all tables are accessible.
rows = workspace.query("SELECT world_id, count(*) FROM bibites GROUP BY world_id")
for row in rows:
    print(row)
</code></pre>

</body>
</html>
`
