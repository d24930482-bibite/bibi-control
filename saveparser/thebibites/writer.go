package thebibites

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const zipVersion20 = 20

// WriteArchive writes archive as a ZIP save file at path.
//
// The writer preserves entry order, names, raw payload bytes, compression
// method, archive comment, entry comments, timestamps, and basic external
// attributes. Untouched entries are emitted from Entry.Raw rather than from
// normalized rows or typed projections.
func WriteArchive(path string, archive *Archive) error {
	if archive == nil {
		return fmt.Errorf("archive is nil")
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	file, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := WriteArchiveTo(file, archive); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// WriteArchiveTo writes archive as ZIP data to w.
func WriteArchiveTo(w io.Writer, archive *Archive) error {
	if archive == nil {
		return fmt.Errorf("archive is nil")
	}

	zipWriter := zip.NewWriter(w)
	if archive.Comment != "" {
		if err := zipWriter.SetComment(archive.Comment); err != nil {
			_ = zipWriter.Close()
			return err
		}
	}

	seen := make(map[string]struct{}, len(archive.Entries))
	for i := range archive.Entries {
		entry := archive.Entries[i]
		if err := validateWritableEntry(entry, seen); err != nil {
			_ = zipWriter.Close()
			return err
		}
		if err := writeArchiveEntry(zipWriter, entry); err != nil {
			_ = zipWriter.Close()
			return err
		}
	}
	return zipWriter.Close()
}

func validateWritableEntry(entry Entry, seen map[string]struct{}) error {
	if err := validateEntryName(entry.Name); err != nil {
		return fmt.Errorf("unsafe zip entry %q: %w", entry.Name, err)
	}
	if _, ok := seen[entry.Name]; ok {
		return fmt.Errorf("duplicate zip entry %q", entry.Name)
	}
	seen[entry.Name] = struct{}{}

	if isDirectoryEntry(entry) {
		if entry.Kind == EntryDirectory && !strings.HasSuffix(entry.Name, "/") {
			return fmt.Errorf("directory entry %q does not end with slash", entry.Name)
		}
		if len(entry.Raw) != 0 {
			return fmt.Errorf("directory entry %q has payload bytes", entry.Name)
		}
		return nil
	}
	if entry.Raw == nil && entry.UncompressedSize != 0 {
		return fmt.Errorf("entry %q has no raw payload bytes", entry.Name)
	}
	return nil
}

func writeArchiveEntry(zipWriter *zip.Writer, entry Entry) error {
	raw := entry.Raw
	if raw == nil {
		raw = []byte{}
	}
	compressed, method, err := encodeZipPayload(entry, raw)
	if err != nil {
		return err
	}

	header := &zip.FileHeader{
		Name:               entry.Name,
		Comment:            entry.Comment,
		NonUTF8:            entry.NonUTF8,
		CreatorVersion:     versionOrDefault(entry.CreatorVersion),
		ReaderVersion:      versionOrDefault(entry.ReaderVersion),
		Flags:              entry.Flags & 0x800,
		Method:             method,
		CRC32:              crc32.ChecksumIEEE(raw),
		ModifiedTime:       entry.ModifiedTime,
		ModifiedDate:       entry.ModifiedDate,
		CompressedSize64:   uint64(len(compressed)),
		UncompressedSize64: uint64(len(raw)),
		Extra:              append([]byte(nil), entry.Extra...),
		ExternalAttrs:      entry.ExternalAttrs,
	}
	if header.ModifiedDate == 0 && header.ModifiedTime == 0 && !entry.Modified.IsZero() {
		header.SetModTime(entry.Modified)
	}

	writer, err := zipWriter.CreateRaw(header)
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", entry.Name, err)
	}
	if isDirectoryEntry(entry) {
		return nil
	}
	if _, err := writer.Write(compressed); err != nil {
		return fmt.Errorf("write zip entry %q: %w", entry.Name, err)
	}
	return nil
}

func isDirectoryEntry(entry Entry) bool {
	return entry.Kind == EntryDirectory || strings.HasSuffix(entry.Name, "/")
}

func encodeZipPayload(entry Entry, raw []byte) ([]byte, uint16, error) {
	if isDirectoryEntry(entry) {
		return nil, zip.Store, nil
	}

	method := entry.Method
	switch method {
	case zip.Store:
		return raw, method, nil
	case zip.Deflate:
		var compressed bytes.Buffer
		writer, err := flate.NewWriter(&compressed, flate.DefaultCompression)
		if err != nil {
			return nil, 0, fmt.Errorf("create deflater for zip entry %q: %w", entry.Name, err)
		}
		if _, err := writer.Write(raw); err != nil {
			_ = writer.Close()
			return nil, 0, fmt.Errorf("deflate zip entry %q: %w", entry.Name, err)
		}
		if err := writer.Close(); err != nil {
			return nil, 0, fmt.Errorf("finish deflating zip entry %q: %w", entry.Name, err)
		}
		return compressed.Bytes(), method, nil
	default:
		return nil, 0, fmt.Errorf("unsupported zip method %d for entry %q", method, entry.Name)
	}
}

func versionOrDefault(version uint16) uint16 {
	if version == 0 {
		return zipVersion20
	}
	return version
}
