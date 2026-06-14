// Package thebibites provides staged mutation helpers for The Bibites save
// archives.
//
// The package intentionally stays separate from saveparser/thebibites. The
// parser package owns lossless archive IO and read/query projections. This
// package owns read-modify-write mutation flow:
//
//  1. Query a parsed archive elsewhere.
//  2. Stage mutations with archive locators and identity guards.
//  3. Apply staged mutations to archive entry JSON/raw bytes.
//  4. Commit by writing the archive and reparsing the written save.
//
// After Apply, entry bytes are authoritative but parser projections on the
// in-memory archive are invalid until Commit returns a freshly parsed archive.
package thebibites
