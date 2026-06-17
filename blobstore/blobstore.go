// Package blobstore provides content-addressed blob storage primitives.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	// SHA256HexLength is the canonical lowercase hex length for a SHA-256 digest.
	SHA256HexLength = sha256.Size * 2

	// DefaultInlineThreshold stores blobs smaller than this directly in Ref.Inline.
	DefaultInlineThreshold int64 = 4 * 1024
)

// Store persists immutable blobs and retrieves them by content reference.
type Store interface {
	Put(ctx context.Context, blob []byte) (Ref, error)
	Get(ctx context.Context, ref Ref) ([]byte, error)
	Has(ctx context.Context, ref Ref) (bool, error)
}

// Ref identifies a stored blob by digest and size. Inline is populated for
// blobs below a store's inline threshold; nil Inline means the bytes live in
// the backing store.
type Ref struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Inline []byte `json:"inline"`
}

// IsInline reports whether the blob bytes are carried directly in this ref.
func (r Ref) IsInline() bool {
	return r.Inline != nil
}

// Validate checks that the ref is well-formed. Inline refs are also verified
// against their digest and size metadata.
func (r Ref) Validate() error {
	if r.Size < 0 {
		return fmt.Errorf("blobstore: ref size %d is negative", r.Size)
	}
	if err := validateSHA256(r.SHA256); err != nil {
		return err
	}
	if r.Inline == nil {
		return nil
	}
	if int64(len(r.Inline)) != r.Size {
		return fmt.Errorf("blobstore: inline blob size %d does not match ref size %d", len(r.Inline), r.Size)
	}
	if got := digestBytes(r.Inline); got != r.SHA256 {
		return fmt.Errorf("blobstore: inline blob sha256 %s does not match ref sha256 %s", got, r.SHA256)
	}
	return nil
}

func newRef(blob []byte) Ref {
	return Ref{
		SHA256: digestBytes(blob),
		Size:   int64(len(blob)),
	}
}

func digestBytes(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

func cloneBytes(blob []byte) []byte {
	out := make([]byte, len(blob))
	copy(out, blob)
	return out
}

func validateSHA256(digest string) error {
	if len(digest) != SHA256HexLength {
		return fmt.Errorf("blobstore: sha256 digest length %d, want %d", len(digest), SHA256HexLength)
	}
	for _, c := range digest {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return fmt.Errorf("blobstore: sha256 digest %q is not lowercase hex", digest)
	}
	return nil
}

func usableContext(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return context.Background(), nil
	}
	return ctx, ctx.Err()
}
