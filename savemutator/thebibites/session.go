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
	var removed []string
	var added []tb.Entry
	for i, op := range s.ops {
		switch op.Kind {
		case OperationDeleteEntry, OperationAppendEntry:
			if err := s.applyEntryOperation(updates, &removed, &added, op); err != nil {
				return fmt.Errorf("apply operation %d (%s %s): %w", i, op.Kind, op.entryLabel(), err)
			}
		default:
			if err := s.applyOperation(updates, op); err != nil {
				return fmt.Errorf("apply operation %d (%s %s %s): %w", i, op.Kind, op.Target.EntryName, op.Path, err)
			}
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

	s.applyEntryListChanges(removed, added)

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
	case OperationAppend:
		if err := appendJSONArray(update.value, op.Path, op.Value); err != nil {
			return err
		}
		if op.SceneCount != "" {
			return s.adjustSceneCount(updates, op.SceneCount, 1)
		}
		return nil
	case OperationDelete:
		if err := deleteJSONArrayElement(update.value, op.Path); err != nil {
			return err
		}
		if op.SceneCount != "" {
			return s.adjustSceneCount(updates, op.SceneCount, -1)
		}
		return nil
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

// StageAppend stages appending value to the JSON array at containerPath.
func (s *Session) StageAppend(target Target, containerPath string, value any) error {
	return s.Stage(Append(target, containerPath, value))
}

// StageDelete stages removal of the JSON array element at elementPath.
func (s *Session) StageDelete(target Target, elementPath string) error {
	return s.Stage(Delete(target, elementPath))
}

// sceneCountPellets is the scene field reconciled when pellets are added/removed.
const sceneCountPellets = "nPellets"

// StageAppendPellet stages appending a pellet to a group, reconciling scene
// nPellets.
func (s *Session) StageAppendPellet(groupIndex int, pellet any) error {
	op := Append(PelletsTarget(), fmt.Sprintf("pellets[%d].pellets", groupIndex), pellet)
	op.SceneCount = sceneCountPellets
	return s.Stage(op)
}

// StageDeletePellet stages removal of a pellet from a group, reconciling scene
// nPellets.
func (s *Session) StageDeletePellet(groupIndex, groupPelletIndex int) error {
	op := Delete(PelletsTarget(), fmt.Sprintf("pellets[%d].pellets[%d]", groupIndex, groupPelletIndex))
	op.SceneCount = sceneCountPellets
	return s.Stage(op)
}

// StageDeleteBibite stages removal of a whole bibite entry, refusing the delete
// if it would orphan a parent/child reference.
func (s *Session) StageDeleteBibite(ref BibiteRef) error {
	return s.StageDeleteBibiteWithOptions(ref, DeleteOptions{})
}

// StageDeleteBibiteWithOptions is StageDeleteBibite with explicit options.
func (s *Session) StageDeleteBibiteWithOptions(ref BibiteRef, options DeleteOptions) error {
	return s.Stage(DeleteEntryWithOptions(BibiteTarget(ref), options))
}

// StageAppendBibite stages adding a whole bibite entry from payload.
func (s *Session) StageAppendBibite(payload EntryPayload) error {
	return s.Stage(AppendEntry(payload))
}

func (op Operation) entryLabel() string {
	if op.Kind == OperationAppendEntry {
		return op.EntryPayload.Name
	}
	return op.Target.EntryName
}

func (s *Session) applyEntryOperation(updates map[string]*entryUpdate, removed *[]string, added *[]tb.Entry, op Operation) error {
	switch op.Kind {
	case OperationDeleteEntry:
		return s.applyDeleteEntry(updates, removed, op)
	case OperationAppendEntry:
		return s.applyAppendEntry(updates, added, op)
	default:
		return fmt.Errorf("unsupported entry operation %q", op.Kind)
	}
}

func (s *Session) applyDeleteEntry(updates map[string]*entryUpdate, removed *[]string, op Operation) error {
	entry := s.archive.Entry(op.Target.EntryName)
	if entry == nil {
		return fmt.Errorf("entry not found")
	}
	if entry.Kind != op.Target.Kind {
		return fmt.Errorf("entry kind = %q, want %q", entry.Kind, op.Target.Kind)
	}
	if entry.JSON == nil {
		return fmt.Errorf("entry does not have decoded JSON")
	}
	if err := validateTargetGuards(entry.JSON, op.Target); err != nil {
		return err
	}

	if entry.Kind == tb.EntryBibite {
		if id, ok := bibiteBodyID(entry.JSON); ok {
			referencing := s.entriesReferencingChild(op.Target.EntryName, id)
			if len(referencing) > 0 {
				if !op.DeleteOptions.PruneParentLinks {
					return fmt.Errorf("bibite id %d is referenced as a child by %v; set PruneParentLinks to remove those references", id, referencing)
				}
				for _, name := range referencing {
					if err := s.pruneChildRef(updates, name, id); err != nil {
						return err
					}
				}
			}
		}
		if err := s.adjustSceneCount(updates, "nBibites", -1); err != nil {
			return err
		}
	}

	*removed = append(*removed, op.Target.EntryName)
	return nil
}

func (s *Session) applyAppendEntry(updates map[string]*entryUpdate, added *[]tb.Entry, op Operation) error {
	payload := op.EntryPayload
	if s.archive.Entry(payload.Name) != nil {
		return fmt.Errorf("entry %q already exists", payload.Name)
	}
	if tb.ClassifyEntry(payload.Name) != payload.Kind {
		return fmt.Errorf("entry name %q does not classify as kind %q", payload.Name, payload.Kind)
	}
	for i := range *added {
		if (*added)[i].Name == payload.Name {
			return fmt.Errorf("entry %q already staged for append", payload.Name)
		}
	}

	value := cloneJSON(payload.JSON)
	raw, err := encodeJSON(value, false)
	if err != nil {
		return fmt.Errorf("serialize new entry %q: %w", payload.Name, err)
	}
	sum := sha256.Sum256(raw)
	*added = append(*added, tb.Entry{
		Name:             payload.Name,
		Kind:             payload.Kind,
		JSON:             value,
		Raw:              raw,
		SHA256:           hex.EncodeToString(sum[:]),
		CRC32:            crc32.ChecksumIEEE(raw),
		UncompressedSize: uint64(len(raw)),
	})
	if payload.Kind == tb.EntryBibite {
		if err := s.adjustSceneCount(updates, "nBibites", 1); err != nil {
			return err
		}
	}
	s.dirty[payload.Name] = struct{}{}
	return nil
}

func (s *Session) applyEntryListChanges(removed []string, added []tb.Entry) {
	if len(removed) == 0 && len(added) == 0 {
		return
	}
	removeSet := make(map[string]struct{}, len(removed))
	for _, name := range removed {
		removeSet[name] = struct{}{}
	}
	next := make([]tb.Entry, 0, len(s.archive.Entries)+len(added))
	for i := range s.archive.Entries {
		entry := s.archive.Entries[i]
		if _, drop := removeSet[entry.Name]; drop {
			s.dirty[entry.Name] = struct{}{}
			continue
		}
		next = append(next, entry)
	}
	next = append(next, added...)
	s.archive.Entries = next
}

// adjustSceneCount applies delta to a non-negative integer scene count field,
// staging the scene entry as a normal JSON update. Missing fields are left as-is.
func (s *Session) adjustSceneCount(updates map[string]*entryUpdate, field string, delta int64) error {
	update, err := s.entryUpdate(updates, SceneTarget())
	if err != nil {
		return err
	}
	current, ok, err := getJSONPath(update.value, field)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	n, ok := jsonNumberToInt64(current)
	if !ok {
		return fmt.Errorf("scene %s is not an integer", field)
	}
	next := n + delta
	if next < 0 {
		next = 0
	}
	return setJSONPath(update.value, field, next, SetOptions{})
}

// entriesReferencingChild returns the names of bibite entries (other than
// skipName) whose body.eggLayer.children array contains id.
func (s *Session) entriesReferencingChild(skipName string, id int64) []string {
	var names []string
	for i := range s.archive.Entries {
		entry := &s.archive.Entries[i]
		if entry.Kind != tb.EntryBibite || entry.Name == skipName || entry.JSON == nil {
			continue
		}
		if childIndexOf(entry.JSON, id) >= 0 {
			names = append(names, entry.Name)
		}
	}
	return names
}

func (s *Session) pruneChildRef(updates map[string]*entryUpdate, name string, id int64) error {
	update, err := s.entryUpdate(updates, EntryTarget(name, tb.EntryBibite))
	if err != nil {
		return err
	}
	idx := childIndexOf(update.value, id)
	if idx < 0 {
		return nil
	}
	return deleteJSONArrayElement(update.value, fmt.Sprintf("body.eggLayer.children[%d]", idx))
}

func bibiteBodyID(root any) (int64, bool) {
	value, ok, err := getJSONPath(root, "body.id")
	if err != nil || !ok {
		return 0, false
	}
	return jsonNumberToInt64(value)
}

func childIndexOf(root any, id int64) int {
	value, ok, err := getJSONPath(root, "body.eggLayer.children")
	if err != nil || !ok {
		return -1
	}
	children, ok := value.([]any)
	if !ok {
		return -1
	}
	for i, child := range children {
		if n, ok := jsonNumberToInt64(child); ok && n == id {
			return i
		}
	}
	return -1
}

func cloneOperation(op Operation) Operation {
	op.Target.Guards = append([]Guard(nil), op.Target.Guards...)
	if op.EntryPayload.JSON != nil {
		op.EntryPayload.JSON = cloneJSON(op.EntryPayload.JSON)
	}
	return op
}
