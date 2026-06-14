package thebibites

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"slices"
	"testing"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

func TestSessionStagesAppliesAndCommitsSet(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	scene := archive.Entry("scene.bb8scene")
	originalBibiteHash := bibite.SHA256
	originalBibiteRaw := append([]byte(nil), bibite.Raw...)
	originalSceneHash := scene.SHA256

	if got := archive.Bibites[0].Energy; got != 12.5 {
		t.Fatalf("initial parsed energy = %v, want 12.5", got)
	}

	session := NewSession(archive)
	if err := session.StageBibiteEnergy(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	}, 99.25); err != nil {
		t.Fatalf("StageBibiteEnergy() error = %v", err)
	}
	if session.State() != StateStaged {
		t.Fatalf("state = %s, want %s", session.State(), StateStaged)
	}
	if !session.ProjectionsValid() {
		t.Fatalf("projections should remain valid before apply")
	}
	if !bytes.Equal(bibite.Raw, originalBibiteRaw) {
		t.Fatalf("stage changed entry raw bytes")
	}

	if err := session.Apply(); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if session.State() != StateApplied {
		t.Fatalf("state = %s, want %s", session.State(), StateApplied)
	}
	if session.ProjectionsValid() {
		t.Fatalf("projections should be invalid after apply")
	}
	if got := session.DirtyEntries(); !slices.Equal(got, []string{"bibites/bibite_0.bb8"}) {
		t.Fatalf("dirty entries = %v", got)
	}
	if !bytes.HasPrefix(bibite.Raw, utf8BOM) {
		t.Fatalf("mutated entry did not preserve UTF-8 BOM")
	}
	if bibite.SHA256 == originalBibiteHash {
		t.Fatalf("bibite hash did not change")
	}
	if scene.SHA256 != originalSceneHash {
		t.Fatalf("unrelated scene hash changed")
	}
	if got := archive.Bibites[0].Energy; got != 12.5 {
		t.Fatalf("parsed projection energy changed before commit: %v", got)
	}

	outPath := filepath.Join(t.TempDir(), "mutated.zip")
	fresh, err := session.Commit(outPath)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if session.State() != StateClean {
		t.Fatalf("state = %s, want %s", session.State(), StateClean)
	}
	if !session.ProjectionsValid() {
		t.Fatalf("projections should be valid after commit")
	}
	if len(session.DirtyEntries()) != 0 {
		t.Fatalf("dirty entries were not cleared after commit")
	}
	if got := fresh.Bibites[0].Energy; got != 99.25 {
		t.Fatalf("committed parsed energy = %v, want 99.25", got)
	}
	tables := tb.ExtractTables("mutated", fresh)
	if got := tables.Bibites[0].Energy; got != 99.25 {
		t.Fatalf("normalized energy = %v, want 99.25", got)
	}
	if got := fresh.Entry("scene.bb8scene").SHA256; got != originalSceneHash {
		t.Fatalf("committed unrelated scene hash = %s, want %s", got, originalSceneHash)
	}
}

func TestSessionSetSupportsNestedArrayPaths(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSet(target, "body.stomach.content[0].amount", 7.75); err != nil {
		t.Fatalf("StageSet() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got := fresh.Bibites[0].StomachContents[0].Amount; got != 7.75 {
		t.Fatalf("committed stomach amount = %v, want 7.75", got)
	}
}

func TestSessionRejectsGuardMismatchWithoutChangingRaw(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	originalRaw := append([]byte(nil), bibite.Raw...)

	session := NewSession(archive)
	if err := session.StageBibiteEnergy(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    999,
	}, 99.25); err != nil {
		t.Fatalf("StageBibiteEnergy() error = %v", err)
	}
	if err := session.Apply(); err == nil {
		t.Fatalf("Apply() error = nil, want guard mismatch")
	}
	if !bytes.Equal(bibite.Raw, originalRaw) {
		t.Fatalf("failed apply changed raw bytes")
	}
	if session.State() != StateStaged {
		t.Fatalf("state after failed apply = %s, want %s", session.State(), StateStaged)
	}
}

func TestSessionApplyIsAtomic(t *testing.T) {
	archive := parseSyntheticArchive(t)
	bibite := archive.Entry("bibites/bibite_0.bb8")
	originalRaw := append([]byte(nil), bibite.Raw...)

	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSet(target, "body.energy", 20.0); err != nil {
		t.Fatalf("StageSet(valid) error = %v", err)
	}
	if err := session.StageSet(target, "body.missing.value", 1.0); err != nil {
		t.Fatalf("StageSet(invalid target path) error = %v", err)
	}
	if err := session.Apply(); err == nil {
		t.Fatalf("Apply() error = nil, want missing path")
	}
	if !bytes.Equal(bibite.Raw, originalRaw) {
		t.Fatalf("failed atomic apply changed raw bytes")
	}
}

func TestSessionSetCanCreateFinalMissingKey(t *testing.T) {
	archive := parseSyntheticArchive(t)
	session := NewSession(archive)
	target := BibiteTarget(BibiteRef{
		EntryName: "bibites/bibite_0.bb8",
		BodyID:    42,
	})
	if err := session.StageSetWithOptions(target, "body.newValue", "ok", SetOptions{CreateMissing: true}); err != nil {
		t.Fatalf("StageSetWithOptions() error = %v", err)
	}
	fresh, err := session.Commit(filepath.Join(t.TempDir(), "mutated.zip"))
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	got, ok, err := getJSONPath(fresh.Entry("bibites/bibite_0.bb8").JSON, "body.newValue")
	if err != nil {
		t.Fatalf("getJSONPath() error = %v", err)
	}
	if !ok || got != "ok" {
		t.Fatalf("new field = %v/%t, want ok/true", got, ok)
	}
}

func TestStageRejectsUnsupportedOperationKind(t *testing.T) {
	session := NewSession(parseSyntheticArchive(t))
	err := session.Stage(Operation{
		Kind:   OperationKind("remove"),
		Target: EntryTarget("scene.bb8scene", tb.EntryScene),
		Path:   "nBibites",
	})
	if err == nil {
		t.Fatalf("Stage() error = nil, want unsupported operation kind")
	}
}

func parseSyntheticArchive(t *testing.T) *tb.Archive {
	t.Helper()

	rawBibite := append([]byte(nil), utf8BOM...)
	rawBibite = append(rawBibite, []byte(`{"body":{"id":42,"energy":12.5,"stomach":{"content":[{"material":"Plant","amount":2.5,"averageChunkAmount":1.25}]}},"genes":{"speciesID":3,"gen":4},"brain":{"Nodes":[],"Synapses":[]}}`)...)
	rawScene := append([]byte(nil), utf8BOM...)
	rawScene = append(rawScene, []byte(`{"nBibites":1}`)...)

	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	archive := &tb.Archive{
		Entries: []tb.Entry{
			{
				Index:  0,
				Name:   "scene.bb8scene",
				Kind:   tb.EntryScene,
				Method: zip.Deflate,
				Raw:    rawScene,
			},
			{
				Index:  1,
				Name:   "bibites/bibite_0.bb8",
				Kind:   tb.EntryBibite,
				Method: zip.Deflate,
				Raw:    rawBibite,
			},
		},
	}
	if err := tb.WriteArchive(sourcePath, archive); err != nil {
		t.Fatalf("WriteArchive(synthetic) error = %v", err)
	}
	parsed, err := tb.ParseFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("ParseFile(synthetic) error = %v", err)
	}
	return parsed
}
