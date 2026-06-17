package blobstore

import (
	"context"
	"fmt"
	"path/filepath"

	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/gcerrors"
)

// FSOption configures an FSStore.
type FSOption func(*FSStore) error

// WithInlineThreshold sets the maximum size cutoff for inline refs. Blobs with
// size strictly less than threshold are returned inline; blobs at the threshold
// or larger are written to the filesystem-backed bucket.
func WithInlineThreshold(threshold int64) FSOption {
	return func(s *FSStore) error {
		if threshold < 0 {
			return fmt.Errorf("blobstore: inline threshold %d is negative", threshold)
		}
		s.inlineThreshold = threshold
		return nil
	}
}

// FSStore stores non-inline blobs under root/objects/ab/cd/<sha256> using Go
// Cloud's fileblob filesystem bucket.
type FSStore struct {
	root            string
	inlineThreshold int64
	bucket          *blob.Bucket
}

var _ Store = (*FSStore)(nil)

// NewFSStore creates a filesystem-backed content-addressed store rooted at
// root. The backing bucket directory is created if it does not already exist.
func NewFSStore(root string, options ...FSOption) (*FSStore, error) {
	if root == "" {
		return nil, fmt.Errorf("blobstore: root is required")
	}

	store := &FSStore{
		root:            filepath.Clean(root),
		inlineThreshold: DefaultInlineThreshold,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(store); err != nil {
			return nil, err
		}
	}

	bucket, err := fileblob.OpenBucket(store.root, &fileblob.Options{
		CreateDir: true,
		NoTempDir: true,
		Metadata:  fileblob.MetadataDontWrite,
	})
	if err != nil {
		return nil, fmt.Errorf("blobstore: open file bucket: %w", err)
	}
	store.bucket = bucket
	return store, nil
}

// Close releases resources owned by the underlying Go Cloud bucket.
func (s *FSStore) Close() error {
	if s == nil || s.bucket == nil {
		return nil
	}
	return s.bucket.Close()
}

// Root returns the store root directory.
func (s *FSStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// InlineThreshold returns the configured inline threshold in bytes.
func (s *FSStore) InlineThreshold() int64 {
	if s == nil {
		return 0
	}
	return s.inlineThreshold
}

// Put stores blob and returns its content reference. Blobs smaller than the
// inline threshold are carried by the returned ref and are not written to the
// filesystem-backed bucket.
func (s *FSStore) Put(ctx context.Context, blob []byte) (Ref, error) {
	if s == nil || s.bucket == nil {
		return Ref{}, fmt.Errorf("blobstore: FSStore is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return Ref{}, err
	}

	ref := newRef(blob)
	if int64(len(blob)) < s.inlineThreshold {
		ref.Inline = cloneBytes(blob)
		return ref, nil
	}

	matches, err := s.storedObjectMatches(ctx, ref)
	if err != nil {
		return Ref{}, err
	}
	if matches {
		return ref, nil
	}
	if err := s.bucket.WriteAll(ctx, s.objectKey(ref.SHA256), blob, nil); err != nil {
		return Ref{}, fmt.Errorf("blobstore: write object %s: %w", ref.SHA256, err)
	}
	return ref, nil
}

// Get returns the bytes for ref. Returned bytes are verified against ref's size
// and digest metadata.
func (s *FSStore) Get(ctx context.Context, ref Ref) ([]byte, error) {
	if s == nil || s.bucket == nil {
		return nil, fmt.Errorf("blobstore: FSStore is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Inline != nil {
		return cloneBytes(ref.Inline), nil
	}

	blob, err := s.bucket.ReadAll(ctx, s.objectKey(ref.SHA256))
	if err != nil {
		return nil, fmt.Errorf("blobstore: read object %s: %w", ref.SHA256, err)
	}
	if int64(len(blob)) != ref.Size {
		return nil, fmt.Errorf("blobstore: object %s size %d does not match ref size %d", ref.SHA256, len(blob), ref.Size)
	}
	if got := digestBytes(blob); got != ref.SHA256 {
		return nil, fmt.Errorf("blobstore: object %s sha256 mismatch: got %s", ref.SHA256, got)
	}
	return blob, nil
}

// Has reports whether ref's bytes are available. Inline refs are considered
// available when their embedded bytes match the ref metadata.
func (s *FSStore) Has(ctx context.Context, ref Ref) (bool, error) {
	if s == nil || s.bucket == nil {
		return false, fmt.Errorf("blobstore: FSStore is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return false, err
	}
	if err := ref.Validate(); err != nil {
		return false, err
	}
	if ref.Inline != nil {
		return true, nil
	}

	attrs, err := s.bucket.Attributes(ctx, s.objectKey(ref.SHA256))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blobstore: stat object %s: %w", ref.SHA256, err)
	}
	return attrs.Size == ref.Size, nil
}

// Delete removes the backing object for ref. It is idempotent: deleting an
// object that is already absent returns nil (gcerrors.NotFound is treated as
// success, the same pattern Has/storedObjectMatches use). Inline refs carry
// their bytes in the ref itself and have no backing object, so deleting one is
// a no-op. This is the only byte-destructive primitive; the crash-safe
// eviction sequence (catalog flip committed first) relies on its idempotency so
// a re-run after a mid-evict crash is safe.
func (s *FSStore) Delete(ctx context.Context, ref Ref) error {
	if s == nil || s.bucket == nil {
		return fmt.Errorf("blobstore: FSStore is nil")
	}
	ctx, err := usableContext(ctx)
	if err != nil {
		return err
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Inline != nil {
		return nil
	}

	if err := s.bucket.Delete(ctx, s.objectKey(ref.SHA256)); err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			return nil
		}
		return fmt.Errorf("blobstore: delete object %s: %w", ref.SHA256, err)
	}
	return nil
}

func (s *FSStore) objectKey(digest string) string {
	return "objects/" + digest[:2] + "/" + digest[2:4] + "/" + digest
}

func (s *FSStore) objectPath(digest string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("blobstore: FSStore is nil")
	}
	if err := validateSHA256(digest); err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(s.objectKey(digest))), nil
}

func (s *FSStore) storedObjectMatches(ctx context.Context, ref Ref) (bool, error) {
	if err := ref.Validate(); err != nil {
		return false, err
	}
	attrs, err := s.bucket.Attributes(ctx, s.objectKey(ref.SHA256))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blobstore: stat existing object %s: %w", ref.SHA256, err)
	}
	if attrs.Size != ref.Size {
		return false, nil
	}
	blob, err := s.bucket.ReadAll(ctx, s.objectKey(ref.SHA256))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blobstore: read existing object %s: %w", ref.SHA256, err)
	}
	return digestBytes(blob) == ref.SHA256, nil
}
