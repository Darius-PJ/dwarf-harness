// Package ledger is the ledger-format and ledger-reader contract (F0): the
// on-disk encoding of a colony's history, the rules that verify it, and the
// read-only Reader through which every consumer replays it. Format version 1
// is a draft until its owner freeze (D40); this documentation is the format.
//
// # Records
//
// A log is a sequence of records. Each record is one line:
//
//	lowercase_hex(SHA-256(json)) TAB json LF
//
// where json is compact UTF-8 JSON (unchanged by json.Compact) holding one
// object, and the hash covers exactly those bytes. Readers check the bytes as
// stored and never re-marshal.
//
// The object's keys, in the order the writer emits them:
//
//	key                   type          presence   rule
//	format                integer       required   always the first key; 1
//	seq                   integer       required   colony sequence; the first record is 1
//	commit_id             UUIDv7        required   one fresh ID per commit
//	commit_index          integer       required   0-based position in the commit
//	commit_count          integer       required   records in the commit, at least 1
//	prev_hash             64 hex        required   the previous record's hash; ZeroHash for seq 1
//	event_time_ns         integer       required   UTC Unix nanoseconds, above 0, never decreasing
//	clock_held            true          optional   the time is held at the previous record's
//	colony_id             UUIDv7        required   the colony's ID
//	type                  string        required   ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$
//	type_version          integer       required   at least 1
//	actor                 string        required   owner, agent, core or runner
//	agent_id              UUIDv7        optional
//	execution_session_id  UUIDv7        optional
//	context_id            UUIDv7        optional
//	turn_id               UUIDv7        optional
//	operation_id          UUIDv7        optional   links a start and its end
//	measurement_path      string        optional   ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$
//	body_refs             digest array  optional   non-empty; strictly ascending, hence distinct
//	payload               object        required   the type's payload; {} when it has no fields
//
// IDs are canonical lowercase dashed UUIDv7 strings. Integers are plain JSON
// integers with no sign, fraction or exponent; seq and event_time_ns exceed
// 2^53 and are exact only for decoders that preserve 64-bit integers. A
// digest is the SHA-256 of some bytes as 64 lowercase hex digits, never all
// zero (Digest).
//
// body_refs lists the bodies in the colony's body store that the record
// references. They are the record's only body references: a hash inside a
// payload, such as a private-content hash, a check digest or a Git object ID,
// is not one. A record family whose payload names a stored body must also
// list it in body_refs, and says so in its own payload rules, because readers
// do not check payload interiors.
//
// A record is invalid if its first key is not format; if a key repeats or a
// known key is null; if a required key is missing; if an optional key is
// present but empty, clock_held is false, or any value breaks its rule. A
// format other than 1 in a correctly hashed record is reported as an
// *UnsupportedFormatError; every other invalid record fails closed. Keys
// this version does not know are ignored, so the envelope can gain optional
// keys by addition; Record.JSON keeps them. An absent optional key decodes
// to its type's zero value: a zero uuid.UUID, an empty MeasurementPath, a
// nil BodyRefs, ClockHeld false. Present and absent are never ambiguous,
// because a present value is never empty, and no default is ever guessed.
//
// A payload must be a JSON object with no repeated key in any object at any
// depth. EncodeCommit refuses one that is not; readers do not re-verify
// payload interiors, which belong to each type's contract.
//
// # Commits and the chain
//
// A commit is one or more records written together. Its records have
// consecutive seq, the same commit_id, commit_count, event_time_ns and
// clock_held, and commit_index 0 to commit_count-1; the record with
// commit_index commit_count-1 ends it. Every record's prev_hash is the hash
// of the record before it, across commits and segments. Event time never
// decreases; clock_held marks a commit whose time was held at the previous
// record's because the clock moved backwards, and is never on seq 1.
// EncodeCommit is the one encoder: it assigns seq, prev_hash and the commit
// fields, and refuses any commit a reader would reject.
//
// A line over MaxRecordBytes, counting hash, TAB and LF, or a commit whose
// lines total over MaxCommitBytes is refused by EncodeCommit, and Check and
// Reader fail closed on any such line or complete commit found on disk. This
// bounds a reader's memory to one commit and the torn tail below. Changing
// either cap later breaks the format, because older readers reject larger
// records, and needs its own decision entry (D40).
//
// # Segments
//
// The log directory holds segment files named by the sequence of their
// first record as 20 zero-padded decimal digits and ".jsonl" (SegmentName,
// ParseSegmentName). The first segment is 00000000000000000001.jsonl, and
// each later one is named by the previous segment's last seq plus 1. A
// segment holds whole commits only, and only the newest may be empty or end
// in a torn commit. Entries whose names begin with a dot are ignored; any
// other entry that is not a regular file with a segment name fails closed.
// Compressed segments are not recognised; adding them needs a name rule.
//
// # Torn tails and failing closed
//
// Check accepts only complete, correctly hashed and chained commits in order.
// Let E be the end of the last complete commit in the final segment. The
// bytes from E to the end of the file are a torn final commit, reported as
// CheckResult.TornBytes and never replayed, only if they are at most
// MaxCommitBytes and every LF-terminated chunk among them that is, or ends
// in, a correctly hashed line decodes and fits the one commit that could
// have been in flight:
//
//   - its colony_id is the colony's, and all such lines share commit_id,
//     commit_count, event_time_ns and clock_held;
//   - seq is the last complete commit's seq + 1 + commit_index, and
//     commit_index strictly increases with file position;
//   - the line with commit_index 0, if present, begins exactly at E and links
//     to the last complete commit's hash; a line whose predecessor in the
//     commit is present links to it;
//   - the time is not before the last complete commit's, and is equal to it
//     when clock_held is set.
//
// Other chunks, such as partial lines, zeros or garbage, are permitted,
// because an interrupted write can persist blocks out of order or as zeros.
// Anything else fails closed with a *CorruptError, as do an invalid line,
// an incomplete commit or emptiness in any earlier segment, a missing first
// segment and a gap between segment names. A correctly hashed line after
// damaged bytes therefore fails closed.
//
// The rule cannot tell damage confined to the final commit, up to
// MaxCommitBytes, from an interrupted write. Detecting that waits for
// external anchoring of the latest hash (D36, assurance A3).
//
// # Reading
//
// A Reader opens files read-only and never writes, truncates, renames or
// locks, so replay has no effects and may run while a writer holds the
// colony. It returns whole commits only, verifying every record it reads,
// consumed or not: hash, chain, sequence, commit framing, colony and time.
// Types absent from ReadOptions.Consume are skipped; a consumed type at an
// undeclared version stops the reader with an *UnsupportedVersionError.
// With ReadOptions.AllTypes the reader also returns the records of
// unconsumed types, for their envelope fields, such as body_refs. Their
// versions are not checked, and their payloads are not the consumer's to
// decode: a consumer decodes only the payloads of the type versions it
// consumes, so that skipping an unknown type stays safe. Cursor advances
// past every commit fully processed, skipped or returned, and stays at the
// last one on any error, so a consumer may persist it.
//
// A reader resuming after a cursor starts in the segment holding the next
// record and first verifies the link from the last line of the segment
// before it. Whole-log verification is Check's job.
//
// Inside a running Core, ReadOptions.Through must be the writer's durable
// mark: a complete but unacknowledged commit can be on disk while the writer
// waits for its flush. ToEnd reads every complete commit on disk and applies
// the torn-tail rule; it is for offline reading, and while a writer is live
// it may expose a commit the writer has not acknowledged.
//
// # Catalogue
//
// A Catalogue lists the record type versions a writer may append. Defined
// returns those this contract version defines: the four F0 record types
// and the four proposed F2-05 types below, each at version 1. F2-05 awaits
// the owner's additive freeze; it does not change F0 or format version 1.
// The type namespace "ledger." is reserved for F0
// record types: no other record family may use it. The namespace "fixture."
// is reserved for this contract's golden fixtures and tests: no record
// family may use it, and Defined never contains it.
// The namespace "workspace." is reserved for C2's record families.
//
// # F0 record types
//
// Core's ledger store produces the F0 record types. Each payload has a Go
// type with an Encode method, which validates the value and emits compact
// JSON with its keys in the order tabled below, and a Decode function. Their
// errors wrap ErrInvalid. A decoder refuses a payload that is not a JSON
// object; a missing required key, a key repeated at any depth, or a known
// key that is null; an ID that is not a canonical lowercase UUIDv7; a digest
// that is not 64 lowercase hex digits or is zero; an integer with a sign,
// fraction or exponent, or out of range; and any value or ordering rule
// broken. Keys it does not know are ignored, at the top level and inside the
// elements of bodies, so that a type version can gain optional keys by
// addition. Encoding a decoded payload reproduces the bytes that Encode
// produced.
//
// Core's writer guarantees the record rules stated under each type: it
// refuses a draft that would break one (see package appender), and, when it
// opens a log, it fails closed on a record that breaks a rule the records
// themselves show. Check, Reader and EncodeCommit do not apply them.
//
// ledger.command v1 (Command) is the identity tombstone of D58: the record
// that a command, named by its immutable command ID, was submitted. It
// carries no request content; the command families (Core step C3) carry the
// request. Only the digest is compared when a command ID is submitted
// again, so the caller's submission_sha256 must cover everything that binds
// the request: its content, its candidate and its context. Any binding the
// digest leaves out could change under the same ID without the mismatch
// that D58 requires, which would make its mismatch rule hollow.
//
//	key                type    presence  rule
//	command_id         UUIDv7  required  the caller's immutable command ID (D37)
//	submission_sha256  digest  required  SHA-256 of the caller's canonical immutable submission bytes
//
// Record rules: it is the record at commit_index 0 of the commit that holds
// the command's effect, and a commit holds at most one; a log holds at most
// one record per command_id; body_refs is absent. Only Core's store writes
// it, when a command is submitted; the writer refuses it through Append.
//
// ledger.body_pin v1 (BodyPin) pins the bodies its body_refs lists, so that
// collection keeps them, until a ledger.body_unpin with the same pin_id.
//
//	key     type    presence  rule
//	pin_id  UUIDv7  required  names the pin; the pinned bodies are the record's body_refs
//
// Record rules: body_refs is present, and every body it lists was published
// when the record was written; pin_id was not active. A pin ID released
// earlier starts a new pin.
//
// ledger.body_unpin v1 (BodyUnpin) releases every body of an active pin.
//
//	key     type    presence  rule
//	pin_id  UUIDv7  required  an active pin
//
// Record rules: body_refs is absent; pin_id was active.
//
// ledger.bodies_collected v1 (BodiesCollected) is the unavailable-body
// history: it records that an explicit collection, ordered by its actor
// under the policy its payload states, made the bodies it lists
// unavailable.
//
//	key         type              presence  rule
//	before_ns   integer           required  the policy's age cutoff in event-time nanoseconds; above 0
//	keep_bytes  integer           optional  the policy's byte target; above 0 when present
//	bodies      array of objects  required  non-empty; strictly ascending by sha256
//
// Each element of bodies is {"sha256": digest, "bytes": integer}: a
// collected body's digest and its size, at least 0.
//
// Record rules: body_refs is absent, because the bodies it lists are payload
// values, not references; actor is owner or core. Only Core's store writes
// it, during an explicit collection; the writer refuses it through Append.
// It lists only bodies that an earlier record referenced and that no active
// pin held.
//
// # F2-05 record types
//
// These four proposed version-1 types are ticket 05's part of F2. C2 owns
// their payload specifications. Only Core's workspace service
// (internal/core/workspace) may append them, through appender.Appender;
// permitted callers are enforced by review. The service checks the record
// rules both when appending and at Open. Check, Reader, EncodeCommit and
// these payload encoders and decoders do not enforce service record rules.
//
// Payloads follow the F0 encoding rules above: compact JSON, keys in the
// tabled order, errors wrapping ErrInvalid, no missing required, null known
// or repeated keys, and unknown keys ignored for additive evolution.
// Re-encoding a decoded payload reproduces the bytes Encode produced.
// Git object IDs are 40 or 64 lowercase hex digits. A decoder accepts either
// length; matching the workspace's object_format is a service record rule.
//
// workspace.registered v1 (WorkspaceRegistered) registers a Git workspace
// with the owner's approval.
//
//	key            type    presence  rule
//	workspace_id   UUIDv7  required  the workspace's ID
//	common_dir     string required  the absolute common-directory grammar below
//	object_format  string required  sha1 or sha256
//
// common_dir accepts exactly: valid UTF-8; 1–4,096 bytes; no byte below
// 0x20 and no 0x7F; no backslash; forward slashes only; absolute in one of
// three forms: POSIX /…, drive-rooted [A-Za-z]:/…, or UNC //host/share…
// with non-empty host and share; no empty component (no // except the two
// leading UNC slashes); no component . or ..; no trailing /. Everything
// else is refused with an error wrapping ErrInvalid.
//
// This is Git's forward-slash string from rev-parse --path-format=absolute
// --git-common-dir, unchanged by Core. Drive-letter case is kept as Git
// printed it; the decoder does not fold case. This grammar is lexical,
// byte-exact and identical on every host; it checks neither existence nor
// platform validity, and makes no host calls. The 4,096-byte cap is a
// contract rule, not a producer policy that may be tightened after freeze.
//
// Record rules: actor is owner; body_refs is absent. This is index 0 of a
// three-record commit: index 1 is its workspace.capture_inputs_pinned with
// revision 1, and index 2 is that record's ledger.body_pin. workspace_id is
// new, and no earlier registration names the same common_dir.
//
// workspace.capture_inputs_pinned v1 (CaptureInputsPinned) records one
// owner-approved revision of the workspace's capture inputs.
//
//	key                    type    presence  rule
//	workspace_id           UUIDv7  required  a registered workspace
//	revision               integer required  1 through MaxInt64
//	capture_inputs_sha256  digest  required  SHA-256 of the capture-inputs document, a body
//	pin_id                 UUIDv7  required  the ledger.body_pin of this revision
//
// Record rules: actor is owner. body_refs is present and holds exactly the
// document and every body it names. The same commit holds a ledger.body_pin
// with this pin_id and the same body_refs. Revision 1 appears only at index 1
// of the registration commit. Each later revision is the current revision
// plus 1, at index 0 of its commit, with its pin at index 1 and the
// ledger.body_unpin of the previous revision's pin_id at index 2.
//
// workspace.scratch_created v1 (ScratchCreated) records a context's private
// scratch worktree and its trusted root eligibility baseline.
//
//	key           type           presence  rule
//	scratch_id    UUIDv7         required  the scratch's ID
//	workspace_id  UUIDv7         required  a registered workspace
//	context_id    UUIDv7         required  the bound work context that owns the scratch and authors its changes
//	dir           string        required  8 lowercase hex digits; scratch/<dir> in the colony directory
//	branch        string        required  the refs/heads/ grammar below
//	base_commit   Git object ID required  the canonical commit the scratch was cut from
//	anchor_tree   Git object ID required  that commit's tree, the root eligibility baseline
//
// branch accepts exactly: valid UTF-8; begins refs/heads/; non-empty suffix;
// at most 4,096 bytes; no byte below 0x20 and no 0x7F; no space and no
// backslash; no empty component (no //); no trailing /. Everything else is
// refused with ErrInvalid. This is a lexical, byte-exact subset, identical
// on every host. The decoder checks neither existence nor platform
// validity. The 4,096-byte cap is a contract rule.
//
// Git's remaining lexical rules (.., @{, lone @, .lock, leading-dot or
// trailing-dot components, ~ ^ : ? * [) are the producer's: Git itself
// refuses them when Core creates the branch, so the decoder does not
// re-implement them. Core's own naming scheme is a service record rule,
// not a contract rule: ticket 05's dedicated scratches use
// branch == "refs/heads/dwarf-harness/" + dir.
//
// Record rules: actor is core; the envelope's context_id equals this
// context_id; body_refs is absent. scratch_id is new; the pair (context_id,
// workspace_id) has no open scratch. dir and branch were never used by an
// earlier scratch. Both Git ID lengths match the workspace's object_format.
//
// workspace.scratch_closed v1 (ScratchClosed) records an explicit closure.
//
//	key         type    presence  rule
//	scratch_id  UUIDv7  required  an open scratch
//	context_id  UUIDv7  required  the scratch's context
//	reason      string required  ^[a-z][a-z0-9_]*$, at most 64 bytes
//
// Version 1 defines reason abandoned. Reason is an open token: both Encode
// and Decode accept every well-formed token, including unknown future
// reasons. A reader treats one as a closure for a reason it does not know,
// never as invalid. Core writes only reasons its reading version defines;
// this is a producer rule, not an encoding rule.
//
// Record rules: actor is owner, or agent with the envelope's context_id
// equal to this context_id; body_refs is absent. A scratch closes at most
// once, and context_id is the scratch's.
//
// The F2-05 golden at testdata/f2/05/log/00000000000000000001.jsonl holds
// six commits and twelve records: two registrations and their pins, two
// scratches, one input revision update and its pin/unpin, then one closure.
// It pins a drive-rooted path and a POSIX path, both Git ID lengths and
// revisions 1 and 2. Tests cover the UNC form separately.
package ledger
