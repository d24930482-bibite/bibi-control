package thebibites

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ParseFile(path string, options *Options) (*Archive, error) {
	opts := DefaultOptions()
	if options != nil {
		opts = *options
	}
	if opts.MaxArchiveBytes <= 0 || opts.MaxEntries <= 0 || opts.MaxEntryBytes == 0 || opts.MaxUncompressedBytes == 0 {
		return nil, fmt.Errorf("invalid parser limits")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > opts.MaxArchiveBytes {
		return nil, fmt.Errorf("archive exceeds max size: %d > %d", info.Size(), opts.MaxArchiveBytes)
	}

	archiveHash, err := hashFile(path)
	if err != nil {
		return nil, err
	}

	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	if len(reader.File) > opts.MaxEntries {
		return nil, fmt.Errorf("archive has too many entries: %d > %d", len(reader.File), opts.MaxEntries)
	}

	archive := &Archive{
		SourcePath: path,
		FileName:   filepath.Base(path),
		Size:       info.Size(),
		SHA256:     archiveHash,
		Entries:    make([]Entry, 0, len(reader.File)),
	}

	var totalUncompressed uint64
	seenNames := make(map[string]struct{}, len(reader.File))
	for i, zipFile := range reader.File {
		if err := validateEntryName(zipFile.Name); err != nil {
			return nil, fmt.Errorf("unsafe zip entry %q: %w", zipFile.Name, err)
		}
		if _, exists := seenNames[zipFile.Name]; exists {
			return nil, fmt.Errorf("duplicate zip entry %q", zipFile.Name)
		}
		seenNames[zipFile.Name] = struct{}{}

		if zipFile.UncompressedSize64 > opts.MaxEntryBytes {
			return nil, fmt.Errorf("entry %q exceeds max uncompressed size: %d > %d", zipFile.Name, zipFile.UncompressedSize64, opts.MaxEntryBytes)
		}
		totalUncompressed += zipFile.UncompressedSize64
		if totalUncompressed > opts.MaxUncompressedBytes {
			return nil, fmt.Errorf("archive exceeds max total uncompressed size: %d > %d", totalUncompressed, opts.MaxUncompressedBytes)
		}

		entry, err := readZipEntry(i, zipFile, opts)
		if err != nil {
			return nil, err
		}
		archive.Entries = append(archive.Entries, entry)
	}

	for i := range archive.Entries {
		archive.parseEntry(&archive.Entries[i])
	}
	archive.recomputeCounts()
	return archive, nil
}

func readZipEntry(index int, zipFile *zip.File, opts Options) (Entry, error) {
	entry := Entry{
		Index:            index,
		Name:             zipFile.Name,
		Kind:             ClassifyEntry(zipFile.Name),
		Method:           zipFile.Method,
		CRC32:            zipFile.CRC32,
		CompressedSize:   zipFile.CompressedSize64,
		UncompressedSize: zipFile.UncompressedSize64,
		Modified:         zipFile.Modified,
	}
	if entry.Kind == EntryDirectory {
		return entry, nil
	}

	reader, err := zipFile.Open()
	if err != nil {
		return entry, fmt.Errorf("open zip entry %q: %w", zipFile.Name, err)
	}
	limit := opts.MaxEntryBytes + 1
	raw, readErr := io.ReadAll(io.LimitReader(reader, int64(limit)))
	closeErr := reader.Close()
	if readErr != nil {
		return entry, fmt.Errorf("read zip entry %q: %w", zipFile.Name, readErr)
	}
	if closeErr != nil {
		return entry, fmt.Errorf("verify zip entry %q: %w", zipFile.Name, closeErr)
	}
	if uint64(len(raw)) > opts.MaxEntryBytes {
		return entry, fmt.Errorf("entry %q exceeded max read size", zipFile.Name)
	}
	if uint64(len(raw)) != zipFile.UncompressedSize64 {
		return entry, fmt.Errorf("entry %q size mismatch: read %d, expected %d", zipFile.Name, len(raw), zipFile.UncompressedSize64)
	}
	sum := sha256.Sum256(raw)
	entry.SHA256 = hex.EncodeToString(sum[:])
	entry.Raw = raw
	return entry, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (a *Archive) parseEntry(entry *Entry) {
	result := parseEntryPayload(entry)
	a.applyParseResult(result)
}
