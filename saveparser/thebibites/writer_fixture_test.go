package thebibites

import (
	"archive/zip"
	"bytes"
	"io"
	"path/filepath"
	"sort"
	"testing"
)

func TestWriteArchiveRoundTripFixtures(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "*.zip"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	sort.Strings(fixtures)
	if len(fixtures) == 0 {
		t.Fatalf("no fixture saves found")
	}

	for _, fixturePath := range fixtures {
		fixtureName := filepath.Base(fixturePath)
		t.Run(fixtureName, func(t *testing.T) {
			archive, err := ParseFile(fixturePath, nil)
			if err != nil {
				t.Fatalf("ParseFile(original) error = %v", err)
			}

			outPath := filepath.Join(t.TempDir(), fixtureName)
			if err := WriteArchive(outPath, archive); err != nil {
				t.Fatalf("WriteArchive() error = %v", err)
			}

			rewritten, err := ParseFile(outPath, nil)
			if err != nil {
				t.Fatalf("ParseFile(rewritten) error = %v", err)
			}
			assertRoundTripArchive(t, archive, rewritten)
			assertZipPayloadsEqual(t, fixturePath, outPath)
		})
	}
}

func TestWriteArchiveRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name    string
		archive *Archive
	}{
		{
			name: "nil raw payload with nonzero size",
			archive: &Archive{Entries: []Entry{{
				Name:             "scene.bb8scene",
				Kind:             EntryScene,
				Method:           zip.Deflate,
				UncompressedSize: 1,
			}}},
		},
		{
			name: "duplicate entry",
			archive: &Archive{Entries: []Entry{
				{Name: "scene.bb8scene", Kind: EntryScene, Method: zip.Deflate, Raw: []byte("{}")},
				{Name: "scene.bb8scene", Kind: EntryScene, Method: zip.Deflate, Raw: []byte("{}")},
			}},
		},
		{
			name: "unsafe entry",
			archive: &Archive{Entries: []Entry{{
				Name:   "../scene.bb8scene",
				Kind:   EntryScene,
				Method: zip.Deflate,
				Raw:    []byte("{}"),
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteArchive(filepath.Join(t.TempDir(), "out.zip"), tt.archive)
			if err == nil {
				t.Fatalf("WriteArchive() error = nil, want error")
			}
		})
	}
}

func assertRoundTripArchive(t *testing.T, original, rewritten *Archive) {
	t.Helper()

	if rewritten.Comment != original.Comment {
		t.Fatalf("archive comment = %q, want %q", rewritten.Comment, original.Comment)
	}
	if len(rewritten.Entries) != len(original.Entries) {
		t.Fatalf("entry count = %d, want %d", len(rewritten.Entries), len(original.Entries))
	}
	if rewritten.Counts.ArchiveEntryCount != original.Counts.ArchiveEntryCount {
		t.Fatalf("archive entry count = %d, want %d", rewritten.Counts.ArchiveEntryCount, original.Counts.ArchiveEntryCount)
	}
	if rewritten.Counts.BibiteFileCount != original.Counts.BibiteFileCount {
		t.Fatalf("bibite file count = %d, want %d", rewritten.Counts.BibiteFileCount, original.Counts.BibiteFileCount)
	}
	if rewritten.Counts.EggFileCount != original.Counts.EggFileCount {
		t.Fatalf("egg file count = %d, want %d", rewritten.Counts.EggFileCount, original.Counts.EggFileCount)
	}
	if rewritten.Counts.Pellets != original.Counts.Pellets {
		t.Fatalf("pellet count = %d, want %d", rewritten.Counts.Pellets, original.Counts.Pellets)
	}
	if len(rewritten.Diagnostics) != len(original.Diagnostics) {
		t.Fatalf("diagnostics = %d, want %d", len(rewritten.Diagnostics), len(original.Diagnostics))
	}

	for i := range original.Entries {
		want := original.Entries[i]
		got := rewritten.Entries[i]
		if got.Name != want.Name {
			t.Fatalf("entry[%d] name = %q, want %q", i, got.Name, want.Name)
		}
		if got.Kind != want.Kind {
			t.Fatalf("entry[%d] kind = %q, want %q", i, got.Kind, want.Kind)
		}
		if got.Method != want.Method {
			t.Fatalf("entry[%d] method = %d, want %d", i, got.Method, want.Method)
		}
		if got.ModifiedTime != want.ModifiedTime || got.ModifiedDate != want.ModifiedDate {
			t.Fatalf("entry[%d] DOS modified time/date = %04x/%04x, want %04x/%04x", i, got.ModifiedTime, got.ModifiedDate, want.ModifiedTime, want.ModifiedDate)
		}
		if got.SHA256 != want.SHA256 {
			t.Fatalf("entry[%d] SHA256 = %s, want %s", i, got.SHA256, want.SHA256)
		}
		if !bytes.Equal(got.Raw, want.Raw) {
			t.Fatalf("entry[%d] raw payload changed", i)
		}
	}
}

func assertZipPayloadsEqual(t *testing.T, originalPath, rewrittenPath string) {
	t.Helper()

	original := readZipSnapshot(t, originalPath)
	rewritten := readZipSnapshot(t, rewrittenPath)
	if len(rewritten) != len(original) {
		t.Fatalf("zip entry count = %d, want %d", len(rewritten), len(original))
	}

	for i := range original {
		want := original[i]
		got := rewritten[i]
		if got.name != want.name {
			t.Fatalf("zip entry[%d] name = %q, want %q", i, got.name, want.name)
		}
		if got.method != want.method {
			t.Fatalf("zip entry[%d] method = %d, want %d", i, got.method, want.method)
		}
		if got.modifiedTime != want.modifiedTime || got.modifiedDate != want.modifiedDate {
			t.Fatalf("zip entry[%d] DOS modified time/date = %04x/%04x, want %04x/%04x", i, got.modifiedTime, got.modifiedDate, want.modifiedTime, want.modifiedDate)
		}
		if got.comment != want.comment {
			t.Fatalf("zip entry[%d] comment = %q, want %q", i, got.comment, want.comment)
		}
		if got.nonUTF8 != want.nonUTF8 {
			t.Fatalf("zip entry[%d] NonUTF8 = %t, want %t", i, got.nonUTF8, want.nonUTF8)
		}
		if got.externalAttrs != want.externalAttrs {
			t.Fatalf("zip entry[%d] external attrs = %08x, want %08x", i, got.externalAttrs, want.externalAttrs)
		}
		if !bytes.Equal(got.extra, want.extra) {
			t.Fatalf("zip entry[%d] extra field changed", i)
		}
		if !bytes.Equal(got.raw, want.raw) {
			t.Fatalf("zip entry[%d] unzipped payload changed", i)
		}
	}
}

type zipEntrySnapshot struct {
	name          string
	comment       string
	method        uint16
	modifiedTime  uint16
	modifiedDate  uint16
	nonUTF8       bool
	extra         []byte
	externalAttrs uint32
	raw           []byte
}

func readZipSnapshot(t *testing.T, path string) []zipEntrySnapshot {
	t.Helper()

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer reader.Close()

	out := make([]zipEntrySnapshot, 0, len(reader.File))
	for _, file := range reader.File {
		entry := zipEntrySnapshot{
			name:          file.Name,
			comment:       file.Comment,
			method:        file.Method,
			modifiedTime:  file.ModifiedTime,
			modifiedDate:  file.ModifiedDate,
			nonUTF8:       file.NonUTF8,
			extra:         append([]byte(nil), file.Extra...),
			externalAttrs: file.ExternalAttrs,
		}
		if file.Name != "" && file.Name[len(file.Name)-1] == '/' {
			out = append(out, entry)
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}
		entry.raw, err = io.ReadAll(handle)
		closeErr := handle.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close zip entry %s: %v", file.Name, closeErr)
		}
		out = append(out, entry)
	}
	return out
}
