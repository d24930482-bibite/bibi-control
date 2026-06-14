package thebibites

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"sort"

	tb "github.com/asemones/bibicontrol/saveparser/thebibites"
)

// State describes the session mutation lifecycle.
type State string

const (
	// StateClean means there are no staged or applied mutations.
	StateClean State = "clean"
	// StateStaged means mutations are staged but the archive is unchanged.
	StateStaged State = "staged"
	// StateApplied means entry JSON/raw bytes have been updated in memory.
	// Parser projections are invalid until Commit reparses the written save.
	StateApplied State = "applied"
)

// Session owns staged mutations for one parsed archive.
type Session struct {
	archive *tb.Archive
	ops     []Operation
	dirty   map[string]struct{}
	state   State
}

// NewSession creates a mutation session for archive.
func NewSession(archive *tb.Archive) *Session {
	return &Session{
		archive: archive,
		dirty:   make(map[string]struct{}),
		state:   StateClean,
	}
}

// Archive returns the session archive. After Apply and before Commit, only
// entry JSON/raw bytes should be treated as authoritative.
func (s *Session) Archive() *tb.Archive {
	if s == nil {
		return nil
	}
	return s.archive
}

// State returns the current mutation lifecycle state.
func (s *Session) State() State {
	if s == nil {
		return StateClean
	}
	return s.state
}

// ProjectionsValid reports whether parser projections on Archive are valid for
// the archive's current entry bytes.
func (s *Session) ProjectionsValid() bool {
	if s == nil {
		return true
	}
	return s.state != StateApplied
}

// StagedOperations returns a copy of staged operations.
func (s *Session) StagedOperations() []Operation {
	if s == nil || len(s.ops) == 0 {
		return nil
	}
	return append([]Operation(nil), s.ops...)
}

// DirtyEntries returns sorted entry names changed by Apply.
func (s *Session) DirtyEntries() []string {
	if s == nil || len(s.dirty) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.dirty))
	for name := range s.dirty {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Stage adds an operation without modifying the archive.
func (s *Session) Stage(op Operation) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if s.archive == nil {
		return fmt.Errorf("archive is nil")
	}
	if s.state == StateApplied {
		return fmt.Errorf("cannot stage after apply; commit first")
	}
	if err := validateOperationShape(op); err != nil {
		return err
	}

	s.ops = append(s.ops, cloneOperation(op))
	s.state = StateStaged
	return nil
}

// StageSet stages a generic JSON set operation.
func (s *Session) StageSet(target Target, path string, value any) error {
	return s.Stage(Set(target, path, value))
}

// StageSetWithOptions stages a generic JSON set operation with explicit
// options.
func (s *Session) StageSetWithOptions(target Target, path string, value any, options SetOptions) error {
	return s.Stage(SetWithOptions(target, path, value, options))
}

// StageBibiteEnergy stages a guarded update to body.energy for a bibite.
func (s *Session) StageBibiteEnergy(ref BibiteRef, energy float64) error {
	return s.StageSet(BibiteTarget(ref), "body.energy", energy)
}

// Apply atomically applies all staged operations to archive entries. If any
// operation fails, no entry bytes are changed.
func (s *Session) Apply() error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if s.archive == nil {
		return fmt.Errorf("archive is nil")
	}
	if s.state == StateApplied || len(s.ops) == 0 {
		return nil
	}

	updates := make(map[string]*entryUpdate)
	for i, op := range s.ops {
		if err := s.applyOperation(updates, op); err != nil {
			return fmt.Errorf("apply operation %d (%s %s %s): %w", i, op.Kind, op.Target.EntryName, op.Path, err)
		}
	}
	for _, update := range updates {
		raw, err := encodeJSON(update.value, update.entry.HasUTF8BOM)
		if err != nil {
			return fmt.Errorf("serialize JSON entry %q: %w", update.entry.Name, err)
		}
		update.raw = raw
	}

	for name, update := range updates {
		raw := update.raw
		sum := sha256.Sum256(raw)
		update.entry.JSON = update.value
		update.entry.Raw = raw
		update.entry.SHA256 = hex.EncodeToString(sum[:])
		update.entry.CRC32 = crc32.ChecksumIEEE(raw)
		update.entry.UncompressedSize = uint64(len(raw))
		update.entry.CompressedSize = 0
		s.dirty[name] = struct{}{}
	}

	s.state = StateApplied
	return nil
}

// Commit applies staged mutations if needed, writes path, reparses the written
// archive, and returns the fresh parsed archive.
func (s *Session) Commit(path string) (*tb.Archive, error) {
	return s.CommitWithOptions(path, nil)
}

// CommitWithOptions is Commit with parser options used for the reparse step.
func (s *Session) CommitWithOptions(path string, options *tb.Options) (*tb.Archive, error) {
	if s == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if path == "" {
		return nil, fmt.Errorf("commit path is required")
	}
	if err := s.Apply(); err != nil {
		return nil, err
	}
	if err := tb.WriteArchive(path, s.archive); err != nil {
		return nil, err
	}
	fresh, err := tb.ParseFile(path, options)
	if err != nil {
		return nil, err
	}

	s.archive = fresh
	s.ops = nil
	s.dirty = make(map[string]struct{})
	s.state = StateClean
	return fresh, nil
}

type entryUpdate struct {
	entry *tb.Entry
	value any
	raw   []byte
}

func (s *Session) applyOperation(updates map[string]*entryUpdate, op Operation) error {
	update, err := s.entryUpdate(updates, op.Target)
	if err != nil {
		return err
	}
	if err := validateTargetGuards(update.value, op.Target); err != nil {
		return err
	}

	switch op.Kind {
	case OperationSet:
		return setJSONPath(update.value, op.Path, op.Value, op.SetOptions)
	default:
		return fmt.Errorf("unsupported operation kind %q", op.Kind)
	}
}

func (s *Session) entryUpdate(updates map[string]*entryUpdate, target Target) (*entryUpdate, error) {
	if update, ok := updates[target.EntryName]; ok {
		return update, nil
	}

	entry := s.archive.Entry(target.EntryName)
	if entry == nil {
		return nil, fmt.Errorf("entry not found")
	}
	if target.Kind != "" && entry.Kind != target.Kind {
		return nil, fmt.Errorf("entry kind = %q, want %q", entry.Kind, target.Kind)
	}
	if entry.JSON == nil {
		return nil, fmt.Errorf("entry does not have decoded JSON")
	}

	update := &entryUpdate{
		entry: entry,
		value: cloneJSON(entry.JSON),
	}
	updates[target.EntryName] = update
	return update, nil
}

func validateTargetGuards(root any, target Target) error {
	for _, guard := range target.Guards {
		got, ok, err := getJSONPath(root, guard.Path)
		if err != nil {
			return fmt.Errorf("guard %q: %w", guard.Path, err)
		}
		if !ok {
			return fmt.Errorf("guard %q is missing", guard.Path)
		}
		if !jsonValuesEqual(got, guard.Equal) {
			return fmt.Errorf("guard %q = %v, want %v", guard.Path, got, guard.Equal)
		}
	}
	return nil
}

func cloneOperation(op Operation) Operation {
	op.Target.Guards = append([]Guard(nil), op.Target.Guards...)
	return op
}
