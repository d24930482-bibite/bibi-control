package thebibites

// This file defines the seam for cross-save append (the "query save A, store,
// append into save B" flow). Only the destination-side primitives are
// implemented today: StageAppend / StageDelete for typed JSON arrays,
// StageAppendBibite / StageDeleteBibite for whole entries, and the SQL-ref
// resolvers SQLAppend / SQLDelete. Coordinating two saves, extracting the
// source element, and re-linking identity/species is intentionally left
// unimplemented until a real multi-save workspace exists.

// CollectedElement is one value pulled from a source save by a query and held
// in a store between the query and a later append. JSON is the array element or
// whole-entry payload to graft into a destination save.
type CollectedElement struct {
	SourcePath string
	Table      string
	JSON       any
}

// Workspace is the cross-save append seam: it would open multiple saves, expose
// a destination Session, and append collected source elements into it. It is
// deliberately not implemented here; the destination Session already supports
// every append/delete primitive a Workspace would drive.
//
// TODO(cross-save): implement multi-session coordination plus a source-element
// extractor that produces EntryPayload / array-element JSON, then feed it into
// the destination session's StageSQLAppend / StageAppendBibite.
type Workspace interface {
	// Destination returns the session that collected elements are appended into.
	Destination() *Session
	// AppendArray appends a collected array element to the destination cell.
	AppendArray(dst SQLValueRef, element CollectedElement) error
	// AppendEntry appends a collected whole entry (bibite/egg) to the
	// destination save.
	AppendEntry(element CollectedElement) error
}
