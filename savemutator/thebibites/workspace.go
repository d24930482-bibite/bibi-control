package thebibites

// This file defines the seam for cross-save append (the "query save A, store,
// append into save B" flow). The destination-side primitives live in session.go
// and sqlref.go: StageAppend / StageDelete for typed JSON arrays,
// StageAppendBibite / StageDeleteBibite for whole entries, and the SQL-ref
// resolvers SQLAppend / SQLDelete. The cross-save coordinator that drives them —
// opening a source and destination Session, extracting the source element, and
// reconciling identity (body.id, child refs) while REFUSING species-bearing
// grafts that would need a cross-world species remap (F3) — is the *transfer type
// in transfer.go and transfer_identity.go (constructor NewTransfer). This stays a pure-archive
// mechanism: it never opens a database, touches a revision store, or commits a
// world head. The workspace layer (a higher package) decides when to Commit and
// how to advance a head; the interface below is the mockable seam it depends on.

// CollectedElement is one value pulled from a source save by a query and held
// in a store between the query and a later append. JSON is the array element or
// whole-entry payload to graft into a destination save.
type CollectedElement struct {
	SourcePath string
	Table      string
	JSON       any
}

// Workspace is the cross-save append seam: it opens multiple saves, exposes a
// destination Session, and appends collected source elements into it. The
// concrete implementer is *transfer (transfer.go); this interface stays so the
// workspace layer can mock the coordinator. The settings-copy path is a scalar
// set rather than an append, so it lives on *transfer (SetFromCollected) and is
// not part of this interface.
type Workspace interface {
	// Destination returns the session that collected elements are appended into.
	Destination() *Session
	// AppendArray appends a collected array element to the destination cell.
	AppendArray(dst SQLValueRef, element CollectedElement) error
	// AppendEntry appends a collected whole entry (bibite/egg) to the
	// destination save. opts carries opt-in graft toggles (e.g. body.id remap).
	AppendEntry(element CollectedElement, opts GraftOptions) error
}
