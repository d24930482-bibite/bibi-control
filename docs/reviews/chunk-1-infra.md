# Code review — Chunk 1: persistence infrastructure (T1 blobstore + T2 revisionstore)

| | |
|---|---|
| **Range** | `review/0-base` (merge-base `588ab74`) → `review/1-infra` (`b2ec4db`) |
| **Scope reviewed** | `blobstore/` + `revisionstore/` — 1,304 lines of code (6 files) |
| **Excluded as noise** | `go.mod`/`go.sum` (pinned dep bumps), `docs/legacy/*` (renames), `save_schema_fingerprint.golden` (generated), `DSL_tickets_plan.md` (plan doc) |
| **Effort** | high (local `/code-review`, 8 finder angles + 1-vote recall-biased verify) |
| **Verdict** | **Ship-ready after addressing F1.** Defensively written; no critical/data-loss bugs. 1 systemic finding, 1 genuine edge case, 1 efficiency nit. |

This is a clean, well-isolated slice: two leaf packages with no coupling to the DSL
chunks, thorough input validation (digest format, size/inline invariants, time
normalization), and good test coverage of the happy paths and the inline/non-inline
boundary. The findings below are the exceptions, not the rule.

---

## Findings

### F1 — `foreign_keys` is not reliably enabled (referential integrity can silently no-op) · *severity: medium*

**`revisionstore/store.go:79`** (with `revisionstore/schema.sql:1`)

`Open` does `sql.Open("sqlite", path)` with no DSN pragma. `PRAGMA foreign_keys = ON`
lives only as the first statement of `schema.sql`, which runs once via
`ApplyMigrations`. **`foreign_keys` is connection-scoped in SQLite** — it applies only
to the connection that executed the PRAGMA, not to the pool.

Why it matters here: the schema's referential guarantees are the *only* guard against
orphan references. `normalizeRevision` validates `ScriptRunID > 0` and `ParentID > 0`
but never checks **existence** — it relies on `script_run_id ... REFERENCES script_runs(id)`
and `parent_id ... REFERENCES save_revisions(id) ON DELETE RESTRICT` to reject bad links.

`SetMaxOpenConns(1)` masks this in the common case (the one pooled connection that ran
the migration is reused), but it is not a guarantee: if that connection is invalidated
by a driver-level error and `database/sql` opens a replacement, the new connection
defaults `foreign_keys` back to **OFF**, and a `RecordRevision` pointing at a
non-existent `script_run_id`/`parent_id` inserts cleanly — and `ON DELETE RESTRICT`
stops protecting against deletes.

**Fix:** enable the pragma per-connection rather than per-migration. With
`modernc.org/sqlite` the simplest is the DSN:

```go
db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
```

(or a `sql.Connector` whose `Connect` runs the PRAGMA on every new conn). Keep the
PRAGMA in `schema.sql` too if you like, but don't let it be the only place.

---

### F2 — A zero-length inline blob loses its inline-ness on a persistence round-trip · *severity: low (edge case)*

**`revisionstore/store.go:367-395`** (`encodeBlobRef` / `decodeBlobRef`), interacting with
**`blobstore/blobstore.go:36-59`** (`IsInline`/`Validate` key off `Inline != nil`).

The entire inline scheme distinguishes "inline" from "stored" by `Inline != nil`
(non-nil, possibly-empty slice = inline; nil = lives in the bucket). That distinction is
**not preserved across the `inline_blob` BLOB column** for the empty case:

1. `blobstore.Put([]byte{})` → inline ref, `Inline = make([]byte, 0)` (non-nil), `IsInline() == true`.
2. `encodeBlobRef` stores `cloneBytes(Inline)` → a **zero-length** BLOB.
3. SQLite/`modernc` reads a zero-length BLOB back as **`nil`**.
4. `decodeBlobRef(..., nil)` → `Inline = nil` → the reloaded ref is treated as **non-inline**.
5. A later `blobstore.Get` on that ref reads from the bucket — where the empty blob was
   never written — and errors.

Only the 0-byte blob is affected; non-empty inline blobs round-trip correctly (and are
tested — `TestStoreReloadsInlineRevisionBlob`). A 0-byte save is implausible in practice,
so this is low severity, but it's a latent correctness hole in a "generic" blob store.

**Fix options:** persist inline-ness explicitly (e.g. an `is_inline` column, or store the
JSON ref with an inline flag) instead of inferring it from nil-vs-empty; or normalize an
empty inline blob to a sentinel on encode/decode. A focused test with a 0-byte blob would
have surfaced this.

---

### F3 — Dedup re-reads and re-hashes the entire stored object on every duplicate `Put` · *severity: low (efficiency)*

**`blobstore/fsstore.go:197-219`** (`storedObjectMatches`, called from `Put` at `:113`).

On a `Put` whose content already exists, `storedObjectMatches` confirms the
content-addressed key exists and `Size` matches, then `ReadAll`s the **whole object** and
re-digests it before deciding to skip the write. Because the object key *is* the SHA-256,
the size match already establishes identity to collision resistance — the full re-read +
re-hash is redundant work on the commit hot path. For a multi-MB save committed when an
identical blob is already present, that's a full extra file read and SHA-256 for no
behavioral gain.

**Fix:** treat key-exists + size-match as a hit and skip the write; if you want a
belt-and-suspenders integrity check, make it opt-in rather than on every dedup'd `Put`.

---

## Notes / things done well

- **Input validation is thorough and consistent:** `validateSHA256` (length + lowercase-hex)
  is enforced at every boundary; `Ref.Validate` cross-checks inline length and digest;
  `scanRevisionRows` re-derives the ref and asserts `sha256`/`size` agree with the row.
- **`SetMaxOpenConns(1)`** is the right call for `modernc` + `:memory:` (each conn would
  otherwise get its own in-memory DB) and for serializing SQLite writes — keep it. (It also
  happens to be what *partially* masks F1; the F1 fix is still needed.)
- **Time handling** (`normalizeTime`/`formatTime` via RFC3339Nano, `.Round(0)` to drop the
  monotonic clock, UTC on the way in and out) is correct and round-trips.
- **Nil-guarded receivers** (`*FSStore`/`*Store` methods return a clean error rather than
  panicking) are a nice touch for a library boundary.
- `objectPath` is test-only (`fsstore_test.go`) — not dead code.

## Test-coverage gaps worth a line each
- 0-byte inline blob round-trip (would catch **F2**).
- A `RecordRevision` with a non-existent `ScriptRunID`/`ParentID` asserting it is rejected
  (would pin **F1** — and would *fail today* if the connection ever recycles).

## Suggested action
Fix **F1** before this slice is considered done (it's the one with a real integrity
consequence and a one-line fix). **F2**/**F3** are fine to defer or fold into a follow-up.
