package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// typedProjectionGoldens pins a fingerprint of every NON-scalar normalized table
// for the 3.1MB largest fixture. This is the byte-identity guard for H4 (dropping
// json_scalars): every typed table projection must land identically to the
// pre-H4 baseline. The fingerprints below were captured on the H2-merged base
// before json_scalars was removed; any drift here means the scalar removal
// changed a typed table, which it must not.
//
// json_scalars is intentionally absent: it is the table being dropped.
var typedProjectionGoldens = map[string]string{
	"active_species":              "26:d3be3a0a2e2631b5880684a1fc8c8cb1",
	"bibite_body":                 "1027:fc116c0877b821667f9a7acdd4375b53",
	"bibite_brain_nodes":          "49746:07b7f2ef449ca5abc65e3c3a799e096d",
	"bibite_brain_synapses":       "4242:91eceb8196f2cbf431ac857bd2b52513",
	"bibite_children":             "875:0202d215ef3748dbc1fd2a40d4cbe40e",
	"bibite_control":              "1027:4b457b4f73161ac536a4b76444a681b3",
	"bibite_egg_layers":           "1027:635eff62be6e640ba76569c1984cd579",
	"bibite_genes":                "40091:e3eff481395e8e2487f93a80d0f58764",
	"bibite_mouth":                "1027:c1320fd5e0e67ebb3e27d5bf282122cc",
	"bibite_pheromone_emitters":   "1027:1a2e27627c74aaa241d46ceda8d8365a",
	"bibite_stomach_contents":     "715:3d1d80b4632fe3ffde05fca728e66caa",
	"bibites":                     "1027:689aef7561b08e56d105e1c9d126c7e8",
	"diagnostics":                 "1:89bc2aee0f2c1518c5dc2f1a717fe91e",
	"egg_brain_nodes":             "387:6cb294dd02166fe361fea79176c2731c",
	"egg_brain_synapses":          "31:ba434aee0b7050be94eb19b0b8a37cd7",
	"egg_genes":                   "314:6d8303d58c66e4e3334673f164468ca9",
	"eggs":                        "8:77054605aa00af0487cb0d9752511b5f",
	"pellet_groups":               "3:272a4c9c63954c81c636fce4fc98daca",
	"pellets":                     "22902:803c4d765fca83281383cba78fa5322a",
	"pheromones":                  "0:e3b0c44298fc1c149afbf4c8996fb924",
	"save_archives":               "1:31745bbcf2afb8af7f281556c11e9860",
	"save_entries":                "1043:420578406da4ae2be588b0ec8aa99b2c",
	"scene_color_selectors":       "0:e3b0c44298fc1c149afbf4c8996fb924",
	"scene_phero_towers":          "0:e3b0c44298fc1c149afbf4c8996fb924",
	"scene_rad_towers":            "0:e3b0c44298fc1c149afbf4c8996fb924",
	"scenes":                      "1:e4ac04cbe1efce46de09508f44121575",
	"settings_bibite_spawners":    "1:567ac862c8116c8750a27cff28ac1314",
	"settings_changer_points":     "5:9704471e2df05f2d62019fc5ecec917d",
	"settings_changer_targets":    "3:10a27cde0cdd34e80b9940358886c841",
	"settings_changers":           "2:94b59015d92979b98f87aca466f3101a",
	"settings_independent_values": "14:42d0aec38cb5cd57df4a5d2b7170af0c",
	"settings_material_values":    "30:da486c2fb42b92ac4732ca320f51b21d",
	"settings_materials":          "4:f0d2f97e88f17188e775bec254f7ee56",
	"settings_simulation_values":  "77:66790a08ab85bd2948393116caefbaec",
	"settings_zone_geometry":      "2:d7bc36173c1ee9a4e6b011df87c41203",
	"settings_zone_groups":        "0:e3b0c44298fc1c149afbf4c8996fb924",
	"settings_zone_values":        "10:0833dd3f92e178b44bfeec923d818350",
	"settings_zones":              "2:aec38636424d711fbb06c915f52181a1",
	"species":                     "31:b2eea0cd7ba0df7fa9247e3862f3b6da",
	"species_brain_nodes":         "1510:800e390cb349f75fbfac4d94529b50d2",
	"species_brain_synapses":      "142:7bc420f778bb1cf9d944ac9c82bcc581",
	"species_genes":               "1085:e888ee06c22e455002740e7023877e59",
	"vars":                        "1:8731dd5d94dbb31ce916d735e5093218",
}

// TestTypedProjectionIdentity is the HARD-constraint guard: dropping json_scalars
// must leave every typed normalized table byte-for-byte identical. It fingerprints
// every dbtable in ExtractedSave (reflecting the same struct duckdb/import.go
// drives off) row-for-row and diffs against the pinned goldens captured before the
// scalar removal. json_scalars must be GONE from the reflected set entirely.
func TestTypedProjectionIdentity(t *testing.T) {
	archive, err := tb.ParseFile(filepath.Join(fixtureDir, "autosave_20260301021357.zip"), nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}
	save := tb.ExtractTables("identity", archive)

	got := fingerprintNormalizedTables(save)

	if _, ok := got["json_scalars"]; ok {
		t.Fatalf("json_scalars is still present in ExtractedSave; it must be dropped")
	}

	// Every reflected table must have a pinned golden, and vice versa, so a newly
	// added/removed table is caught here too.
	for table := range got {
		if _, ok := typedProjectionGoldens[table]; !ok {
			t.Errorf("table %q has no pinned golden (update typedProjectionGoldens)", table)
		}
	}
	for table := range typedProjectionGoldens {
		if _, ok := got[table]; !ok {
			t.Errorf("pinned golden table %q is missing from ExtractedSave", table)
		}
	}

	for table, want := range typedProjectionGoldens {
		if want == "" {
			// Goldens are populated by capturing once (see TestCaptureTypedFingerprints).
			continue
		}
		if got[table] != want {
			t.Errorf("table %q fingerprint = %q, want %q", table, got[table], want)
		}
	}
}

// fingerprintNormalizedTables reflects over ExtractedSave's dbtable-tagged fields
// (the exact set duckdb/import.go imports) and returns count:hash per table, where
// hash is a sha256 over the Go-rendered value of every row in slice order.
func fingerprintNormalizedTables(save tb.ExtractedSave) map[string]string {
	out := make(map[string]string)
	v := reflect.ValueOf(save)
	tStruct := v.Type()
	for i := 0; i < tStruct.NumField(); i++ {
		field := tStruct.Field(i)
		table := field.Tag.Get("dbtable")
		if table == "" {
			continue
		}
		fv := v.Field(i)
		h := sha256.New()
		count := 0
		switch fv.Kind() {
		case reflect.Slice:
			count = fv.Len()
			for r := 0; r < fv.Len(); r++ {
				fmt.Fprintf(h, "%#v\n", fv.Index(r).Interface())
			}
		case reflect.Ptr:
			if !fv.IsNil() {
				count = 1
				fmt.Fprintf(h, "%#v\n", fv.Elem().Interface())
			}
		default:
			count = 1
			fmt.Fprintf(h, "%#v\n", fv.Interface())
		}
		out[table] = fmt.Sprintf("%d:%s", count, hex.EncodeToString(h.Sum(nil))[:32])
	}
	return out
}

// TestCaptureTypedFingerprints prints the current fingerprints in sorted order so
// the goldens above can be regenerated. Not a check; logs only.
func TestCaptureTypedFingerprints(t *testing.T) {
	archive, err := tb.ParseFile(filepath.Join(fixtureDir, "autosave_20260301021357.zip"), nil)
	if err != nil {
		t.Fatalf("tb.ParseFile() error = %v", err)
	}
	save := tb.ExtractTables("identity", archive)
	got := fingerprintNormalizedTables(save)
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("\t%q: %q,", k, got[k])
	}
}
