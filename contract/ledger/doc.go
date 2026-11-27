// Package ledger is the ledger-format and ledger-reader contract (F0): the
// on-disk encoding of a colony's history, the rules that verify it, and the
// read-only Reader through which every consumer replays it. The owner froze
// format version 1 (D40, D111); catalogue parts join it only by addition, as
// F2-05 did (D115). This documentation is the format.
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
// OpenReaderWithCheckpoint optionally reuses an opaque, process-local
// ReadCheckpoint issued by a completed Reader. Before restoring a position it
// checks the directory, colony, range, segment and freshly read predecessor
// chain, then hashes every framed byte of the saved prefix on the same file
// used to read the suffix. Equality reuses the prefix's completed verification,
// not a caller-asserted chain; the suffix uses the ordinary parser and verifier.
// Prefix I/O and SHA-256 work remain, even when prefix decoding is avoided.
//
// A nil, zero, damaged, stale or mismatched checkpoint selects ordinary
// scanning. Directory identity uses Abs then Clean without resolving aliases
// or folding case; failure to derive it disables only checkpoint use/capture.
// The opt-in reader reports actual file-close failures, including automatic
// closure and repeated Close, and never treats I/O or seek failures as misses.
// OpenReader and Check keep their legacy behavior.
//
// Reader.Checkpoint returns a fresh value only after a complete, advancing
// numeric Through, terminal EOF and successful actual closure. ToEnd, equal
// bounds, partial commits, early Close and errors issue none. The value has no
// persistent encoding or retained resources; its checksum detects accidental
// damage, not hostile same-process changes. Checkpoints inherit the append-only
// live-writer protocol, not snapshot or hostile-rewrite atomicity.
//
// # Catalogue
//
// A Catalogue lists the record type versions a writer may append. Defined
// returns those this contract version defines: the four F0 record types,
// the four F2-05 types, the three F2-06 types, the two F2-07 types, the three
// F2-08 types, the four F2-09 types, the four F-U3-61 types, the four
// F3-10 types, the ten F3-11 types, the three F3-12 types, the eight
// F3-13 types, the four F3-139 types, the six F3-15 types, the two
// F3-16 types, the two F3-90 types, the five F3-91 types, the two F3-150
// types and the two additive F7-30 types below, each at version 1.
// F2-05 joined by the owner's additive freeze (D115); it does not change F0
// or format version 1. F2-06 is ticket 06's part of F2, frozen (D118),
// also by addition to format version 1.
// F2-07 is ticket 07's part of F2, frozen (D119),
// also by addition to format version 1.
// F2-08 is ticket 08's part of F2, frozen (D123),
// also by addition to format version 1.
// F2-09 is ticket 09's part of F2, frozen (D124),
// also by addition to format version 1.
// F-U3-61 is ticket 61's part, U3's first producer family, frozen (D130),
// also by addition to format version 1.
// F3-10 is ticket 10's part of F3, frozen (D139),
// also by addition to format version 1.
// F3-11 is ticket 11's part of F3, frozen (D140),
// also by addition to format version 1.
// F3-12 is ticket 12's part of F3, frozen (D144),
// also by addition to format version 1.
// F3-13 is ticket 13's part of F3, frozen (D148),
// also by addition to format version 1.
// F3-139 is ticket 139's part of F3, frozen (D154),
// also by addition to format version 1; record availability does not imply
// an operational producer or graph consumer.
// F3-15 is ticket 15's part of F3, frozen (D165),
// also by addition to format version 1.
// F3-16 is ticket 16's part of F3, frozen (D169),
// also by addition to format version 1; record availability does not imply
// an operational continuation owner, recovery or child shutdown.
// F3-90 is ticket 90's part of F3, frozen (D174),
// also by addition to format version 1; record availability does not imply
// an operational hold, fence, first-action entrance or held runtime behavior.
// F3-91 is ticket 91's part of F3, frozen (D175),
// also by addition to format version 1; record availability does not imply
// operational service-loss detection, timers, restoration or owner notices.
// F3-150 is ticket 150's part of F3, frozen (D187),
// also by addition to format version 1; record availability does not imply
// setup commands, managed configuration or any permission.
// F7-30 is ticket 30's part, C7's first producer family, frozen (D188),
// also by addition to format version 1; record availability does not imply
// a running host, an authenticated launcher connection or a binding producer.
// The type namespace "ledger." is reserved for F0
// record types: no other record family may use it. The namespace "fixture."
// is reserved for this contract's golden fixtures and tests: no record
// family may use it, and Defined never contains it.
// The namespace "workspace." is reserved for C2's record families.
// The namespace "graph." is reserved for U3's record families.
// The namespaces "runtime." and "private." are reserved for C3's Core runtime.
// The namespace "launcher." is reserved for C7's record families.
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
// These four version-1 types are ticket 05's part of F2, frozen by addition
// to format version 1 (D115). C2 owns
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
//	common_dir     string  required  the absolute common-directory grammar below
//	object_format  string  required  sha1 or sha256
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
//	dir           string         required  8 lowercase hex digits; scratch/<dir> in the colony directory
//	branch        string         required  the refs/heads/ grammar below
//	base_commit   Git object ID  required  the canonical commit the scratch was cut from
//	anchor_tree   Git object ID  required  that commit's tree, the root eligibility baseline
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
// F2-07 adds an optional parent_scratch_id with its own creation rules for
// temporary scratches; a log without F2-07 records is unaffected.
//
// workspace.scratch_closed v1 (ScratchClosed) records an explicit closure.
//
//	key         type    presence  rule
//	scratch_id  UUIDv7  required  an open scratch
//	context_id  UUIDv7  required  the scratch's context
//	reason      string  required  ^[a-z][a-z0-9_]*$, at most 64 bytes
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
// F2-06 adds rules on when a closure may be appended and what its commit
// carries; a log without F2-06 records is unaffected.
// F2-07 defines the reason temporary_merged_back and rules for closing
// temporary scratches and their parents; a log without F2-07 records is
// unaffected.
//
// The F2-05 golden at testdata/f2/05/log/00000000000000000001.jsonl holds
// six commits and twelve records: two registrations and their pins, two
// scratches, one input revision update and its pin/unpin, then one closure.
// It pins a drive-rooted path and a POSIX path, both Git ID lengths and
// revisions 1 and 2. Tests cover the UNC form separately.
//
// # F2-06 record types
//
// These three version-1 types are ticket 06's part of F2, frozen by
// addition to format version 1 (D118). C2 owns their
// payload specifications. Only Core's workspace service
// (internal/core/workspace) may append them, through appender.Appender;
// permitted callers are enforced by review. The service checks the record
// rules both when appending and at Open. Check, Reader, EncodeCommit and
// these payload encoders and decoders do not enforce service record rules.
// C2's product never admits, captures or integrates; its mechanism tests
// exercise these records. From C4 on, RequestPromotion admits checkpoints.
//
// Payloads follow the F0 encoding rules above: compact JSON, keys in the
// tabled order, errors wrapping ErrInvalid, no missing required, null known
// or repeated keys, and unknown keys ignored for additive evolution.
// Re-encoding a decoded payload reproduces the bytes Encode produced.
// An optional key is omitted when absent and refused when present with a
// zero, empty or null value. Git object IDs are 40 or 64 lowercase hex
// digits. A decoder accepts either length; matching the workspace's
// object_format is a service record rule.
//
// workspace.promotion_admitted v1 (PromotionAdmitted) records a
// checkpoint's admission: its request receipt and immutable submission.
//
//	key                    type           presence  rule
//	request_id             UUIDv7         required  the caller's immutable request ID (D58)
//	workspace_id           UUIDv7         required  a registered workspace
//	scratch_id             UUIDv7         required  the pair's open scratch, which the capture read
//	context_id             UUIDv7         required  the requesting and authoring context
//	lineage_id             UUIDv7         required  the scratch lineage
//	checkpoint             integer        required  1 through MaxInt64
//	parent_request_id      UUIDv7         optional  the local parent; absent at a root
//	restart_from           UUIDv7         optional  the failed head a new root replaces
//	scratch_base           Git object ID  required  the physical scratch's base commit
//	baseline_tree          Git object ID  required  the trusted eligibility baseline
//	delta_base_tree        Git object ID  required  the delta base, bound separately
//	capture_id             UUIDv7         required  the capture; ref refs/dwarf-harness/captures/<capture_id>
//	sealed_tree            Git object ID  required  the submission's sealed tree
//	manifest_sha256        digest         required  the raw manifest, a body
//	inputs_revision        integer        required  1 through MaxInt64; the capture-inputs revision used
//	capture_inputs_sha256  digest         required  that revision's document digest
//	pin_id                 UUIDv7         required  the manifest's ledger.body_pin in the same commit
//
// Decoder rules: checkpoint is 1 exactly when parent_request_id is absent.
// restart_from is never present with parent_request_id, so a new root has
// checkpoint 1.
//
// Record rules: actor is owner or agent; the envelope's context_id equals
// this context_id. body_refs is exactly [manifest_sha256]. The same commit
// holds, immediately after this record, a ledger.body_pin with pin_id and
// the same body_refs. No commit index is fixed: a ledger.command may come
// first. request_id and capture_id were never admitted before; lineage_id
// is new at a root or new root. scratch_id is the open scratch of
// (context_id, workspace_id), and scratch_base is its base_commit.
//
// With H the scratch's latest admitted request, exactly one case applies:
//   - Root: no H, no parent or restart; baseline_tree and delta_base_tree
//     both equal the scratch's anchor_tree.
//   - Successor: H has not failed; parent_request_id is H; lineage_id is
//     H's and checkpoint is H's plus 1; both trees equal H's sealed_tree.
//   - New root: H failed; restart_from is H; both trees equal the scratch's
//     represented tree, its anchor_tree until a later F2 part records another.
//
// F2-08 is that part: the represented tree is the sealed_tree of the
// scratch's latest request ended by workspace.promotion_accepted or
// workspace.promotion_no_change, else its anchor_tree; a log without F2-08
// records is unaffected.
//
// inputs_revision is a recorded workspace revision not above its current
// one, and capture_inputs_sha256 is that revision's digest. Git ID lengths
// match the workspace's object_format. The K bound is service policy,
// checked on append only, not at Open.
//
// Closure is an F2-06 rule: while a request is live (admitted and not
// ended), its scratch has no workspace.scratch_closed. A closure commit
// holds, after workspace.scratch_closed, one ledger.body_unpin for each
// still-active report_pin_id of the scratch's requests, in log order of
// their pins. A log without F2-06 records is unaffected.
// F2-07 adds the optional provenance keys scratch_observation_id and
// merge_back_ids; a log without F2-07 records is unaffected.
//
// Core's workspace service produces admissions and consumes all payload
// keys and body_refs for identity, lineages, slots, the train and retention.
// C3 consumes request state and passive results for the original context;
// C4 consumes submissions behind generations; ticket 08's finalizer uses
// local-parent order and accepted deltas; the owner may inspect them.
// All payload keys and body_refs are replay-essential. The record carries
// identifiers, Git object IDs and digests, not content. The manifest body
// lists the scratch's paths, sizes and raw hashes, can name private files,
// and is read only through Core's authorized body reads.
//
// workspace.generation_derived v1 (GenerationDerived) records one
// integration of an admitted request over its train predecessor.
//
//	key                        type           presence  rule
//	generation_id              UUIDv7         required  a new generation
//	request_id                 UUIDv7         required  a live admitted request
//	supersedes                 UUIDv7         optional  the request's replaced current generation
//	predecessor_request_id     UUIDv7         optional  the train predecessor; absent for canonical main
//	predecessor_generation_id  UUIDv7         optional  that request's current clean generation
//	predecessor_commit         Git object ID  required  that generation's derived_commit, or main's tip
//	predecessor_tree           Git object ID  required  the predecessor's tree
//	inputs_revision            integer        required  1 through MaxInt64; the inputs revision the merge used
//	integration_rules          string         required  ^[a-z][a-z0-9-]*/[1-9][0-9]*$, at most 64 bytes
//	outcome                    string         required  clean or conflict; closed in v1
//	derived_commit             Git object ID  optional  when clean: the integration commit, predecessor its only parent
//	derived_tree               Git object ID  optional  when clean: the integration commit's tree
//	conflict_sha256            digest         optional  when conflict: the conflict report, a body
//	report_pin_id              UUIDv7         optional  when conflict: the report's ledger.body_pin
//
// Decoder rules: predecessor_generation_id is present exactly when
// predecessor_request_id is. clean requires derived_commit and derived_tree
// and refuses conflict_sha256 and report_pin_id. conflict requires
// conflict_sha256 and report_pin_id and refuses both derived keys. Any
// other outcome is refused in v1.
//
// Core writes integration-rules/1, which names the explicit-base
// git merge-tree --write-tree command, the private bare environment in
// which no merge driver runs and no tree .gitattributes is read, Git's
// default merge options under the pinned configuration, in which
// merge.renormalize, merge.conflictStyle and merge.default are unset, the
// derived-tree case check, and the integration commit's identity, date and
// message. The value is Core policy named in the record: any change to
// those rules is a new value and needs no new type version.
// The integration commit's ref is
// refs/dwarf-harness/generations/<generation_id>.
//
// Record rules: actor is core; the envelope's context_id is the request's.
// body_refs is absent when clean and exactly [conflict_sha256] when
// conflict. A conflict's commit holds, immediately after this record, a
// ledger.body_pin with report_pin_id and the same body_refs. generation_id
// is new; request_id is admitted and not ended. supersedes is the request's
// current generation, absent exactly when it has none; when present,
// predecessor_commit differs from the superseded generation's. When the
// superseded generation was a conflict, the commit also holds, after this
// record and its pin (if any), the ledger.body_unpin of that generation's
// report_pin_id.
//
// When predecessor_request_id is present, it names the nearest live
// request ahead in the workspace's admission order.
// predecessor_generation_id is that request's current generation, with
// outcome clean, and predecessor_commit and predecessor_tree are its
// derived ones. When absent, no live request is ahead and the predecessor
// is canonical main. inputs_revision is recorded and not above the current
// revision; Git ID lengths match the workspace's object_format. The
// generation window is service policy, not a record rule.
// F2-08 adds an optional predecessor_witness_seq naming the latest accepted
// predecessor; a log without F2-08 records is unaffected.
//
// Core's workspace service produces generations and consumes their current
// state; C4 binds attempts to generation_id and evidence to the predecessor
// and derived tree; ticket 08's finalizer and owner audit consume them.
// All payload keys and body_refs are replay-essential. Identifiers, Git
// object IDs and digests are host-private until release under Core §13.3.
// The conflict report body names conflicting paths and is read only through
// Core's authorized body reads.
//
// workspace.promotion_failed v1 (PromotionFailed) records a request ended
// without promotion, failing its lineage.
//
//	key                type    presence  rule
//	request_id         UUIDv7  required  a live admitted request
//	outcome            string  required  ^[a-z][a-z0-9_]*$, at most 64 bytes
//	generation_id      UUIDv7  optional  conflict: the request's conflicting current generation
//	failed_request_id  UUIDv7  optional  dependency_failed: the local request whose failure ended this one
//
// Decoder rules: conflict requires generation_id and refuses
// failed_request_id; dependency_failed requires failed_request_id and
// refuses generation_id. Outcome is an open token: both Encode and Decode
// accept every other well-formed token with either optional key present or
// absent. A reader treats an unknown outcome as a failure it does not know,
// never as invalid. Core writes only outcomes its reading version defines;
// this is a producer rule, not an encoding rule.
//
// Record rules: the envelope's context_id is the request's; body_refs is
// absent. Version 1's outcomes have actor core; a later part defines its
// outcomes' actors. F2-08 defines the outcomes cancelled and interrupted and
// their actors; a log without F2-08 records is unaffected.
// request_id is admitted and not ended. For conflict,
// generation_id is the request's current generation, with outcome conflict,
// and no live request is ahead of it in its workspace's train. For
// dependency_failed, failed_request_id is a request of the same lineage
// with a smaller checkpoint, ended by promotion_failed earlier in the log
// or earlier in the same commit. Every outcome, including one the reader
// does not know, fails the lineage: after the commit's last record no
// request of the lineage with a larger checkpoint than one ended in the
// commit is live. After its endings the commit holds one ledger.body_unpin
// of each ended request's admission pin_id, in the same order. An ending
// never unpins a report pin.
//
// Core's workspace service produces failures and consumes lineage state,
// slots and retention; C3 consumes terminal results (D63); C4 stops
// superseded children; the owner may inspect them. All payload keys are
// replay-essential. The record carries only identifiers and an outcome
// token.
//
// The F2-06 golden at testdata/f2/06/log/00000000000000000001.jsonl holds
// twelve commits and twenty-five records. promotion_admitted appears at
// seqs 6, 8, 10 and 24; generation_derived at 12, 13, 14, 16 and 22; and
// promotion_failed at 18 and 19. It pins a root, a successor, an owner
// admission, a clean generation over main and one over a local parent, a
// speculative conflict with its report pinned, supersession with the
// report unpinned, a terminal conflict with its cascade and manifest
// unpins, a foreign-removal rebuild and a new root after failure. The
// workspace uses sha1; tests cover 64-digit Git IDs separately.
//
// # F2-07 record types
//
// These two version-1 types are ticket 07's part of F2, frozen by
// addition to format version 1 (D119). C2 owns their
// payload specifications. Only Core's workspace service
// (internal/core/workspace) may append them, through appender.Appender;
// permitted callers are enforced by review. The service checks the record
// rules both when appending and at Open. Check, Reader, EncodeCommit and
// these payload encoders and decoders do not enforce service record rules.
// C2's product still admits nothing; Core's workspace service writes these
// records.
//
// Payloads follow the F0 encoding rules above: compact JSON, keys in the
// tabled order, errors wrapping ErrInvalid, no missing required, null known
// or repeated keys, and unknown keys ignored for additive evolution.
// Re-encoding a decoded payload reproduces the bytes Encode produced.
// UUIDs are canonical lowercase non-zero UUIDv7s; digests are non-zero
// SHA-256s. Git object IDs are 40 or 64 lowercase hex digits; matching the
// workspace's object_format is a service record rule. Optional scalars are
// omitted when zero in Go and refused on decode when present with a zero,
// empty or null value. The optional changed_paths of scratch_merge_back is
// the one exception for empty values, specified at its key below.
//
// A path array is a JSON array of strings, strictly increasing by UTF-8
// byte order with no duplicates; required arrays may be empty but never
// absent or null. Encode writes [] for a nil required path slice.
// A repository path is valid UTF-8, non-empty and slash-separated,
// repository-relative: no leading or trailing /, empty component (//),
// component . or .., NUL byte or backslash. There is no normalization or
// case folding. Every other byte Git allows is kept, including newline and
// tab, which JSON escapes. No per-path or per-array byte cap is a contract
// rule. Core refuses an observation whose encoded record or commit would
// exceed MaxRecordBytes or MaxCommitBytes as pathset_cap, without truncation
// or chunking.
//
// workspace.scratch_changed v1 (ScratchChanged) records a host observation
// and advances a scratch's acknowledged private tip.
//
//	key                    type           presence  rule
//	observation_id         UUIDv7         required  a new observation
//	workspace_id           UUIDv7         required  a registered workspace
//	scratch_id             UUIDv7         required  the observed scratch
//	context_id             UUIDv7         required  the context authoring the observation
//	parent_scratch_id      UUIDv7         optional  the creation parent of a temporary
//	previous_commit        Git object ID  required  the previous acknowledged tip
//	previous_tree          Git object ID  required  that tip's tree
//	scratch_commit         Git object ID  required  the observed Core commit
//	scratch_tree           Git object ID  required  the observed canonical tree
//	written_paths          path array     required  previous_tree to scratch_tree; may be []
//	changed_paths          path array     required  full anchor_tree to scratch_tree set; may be []
//	inputs_revision        integer        required  1 through MaxInt64
//	capture_inputs_sha256  digest         required  that revision's document
//	scratch_rules          string         required  ^[a-z][a-z0-9-]*/[1-9][0-9]*$, at most 64 bytes
//	manifest_sha256        digest         required  the raw observation manifest
//	pin_id                 UUIDv7         required  the immediate manifest pin
//
// Decoder rules: scratch_commit differs from previous_commit, including an
// observation with no tree change. written_paths is empty exactly when
// previous_tree equals scratch_tree.
//
// Record rules: actor is core; the envelope's context_id equals this
// context_id. body_refs is exactly [manifest_sha256]. The same commit holds,
// immediately after this record, a ledger.body_pin with pin_id and the
// same body_refs. When the scratch had an active observation-manifest pin,
// that pin's ledger.body_unpin follows next, once: only the latest
// observation manifest stays pinned per scratch. No commit index is fixed.
//
// observation_id is new. scratch_id is an open scratch of (context_id,
// workspace_id), its dedicated scratch or one of its temporaries, with no
// unresolved prepared attempt naming it as source or parent.
// parent_scratch_id is present exactly when the scratch's creation record
// carries one and equals it. previous_commit and previous_tree are its
// acknowledged tip: initially base_commit and anchor_tree, then the latest
// scratch_changed's scratch_commit/scratch_tree or the latest applied
// merge-back's target_commit/target_tree targeting it. After this record
// the scratch's tip is scratch_commit/scratch_tree. changed_paths is the
// full set relative to its anchor_tree, never a delta. inputs_revision is
// recorded and not above the workspace's current revision, and
// capture_inputs_sha256 is that revision's digest. Git ID lengths match
// object_format. Neither the represented tree nor the anchor changes.
// Commit parentage, dates and messages are producer rules proved by the
// service, not replay rules.
//
// The workspace service consumes the tip, provenance and retention facts;
// C3 consumes private-work status, and U3/U4/U9 consume only projections
// permitted by their grants. The owner may inspect them. All payload keys
// and body_refs are replay-essential. Exact paths and authors are
// context-private. Observation manifests, which list raw paths, sizes and
// hashes, are read only through Core's authorized body reads.
//
// workspace.scratch_merge_back v1 (ScratchMergeBack) records a private
// merge-back plan or result, never a canonical promotion.
//
//	key                    type           presence  rule
//	merge_back_id          UUIDv7         required  the attempt's ID
//	workspace_id           UUIDv7         required  a registered workspace
//	scratch_id             UUIDv7         required  the source temporary
//	parent_scratch_id      UUIDv7         required  the task destination
//	context_id             UUIDv7         required  the parent's context
//	authoring_context_id   UUIDv7         required  the source's context
//	source_observation_id  UUIDv7         required  the source's latest acknowledged observation
//	fork_commit            Git object ID  required  the source's base_commit
//	fork_tree              Git object ID  required  the source's anchor_tree
//	parent_commit          Git object ID  required  the destination tip at preparation or conflict
//	parent_tree            Git object ID  required  that tip's tree
//	source_commit          Git object ID  required  the observed source tip
//	source_tree            Git object ID  required  that tip's tree
//	inputs_revision        integer        required  1 through MaxInt64
//	capture_inputs_sha256  digest         required  that revision's document
//	integration_rules      string         required  ^[a-z][a-z0-9-]*/[1-9][0-9]*$, at most 64 bytes
//	outcome                string         required  prepared, write_failed, fast_forward, merged or conflict; closed in v1
//	target_commit          Git object ID  optional  present exactly for non-conflict outcomes
//	target_tree            Git object ID  optional  present exactly for non-conflict outcomes
//	written_paths          path array     required  fork_tree to source_tree; may be []
//	changed_paths          path array     optional  present exactly for fast_forward or merged; this optional key may be present and empty []
//	manifest_sha256        digest         optional  prepared: the parent's pre-write raw manifest
//	pin_id                 UUIDv7         optional  prepared: the preparation pin
//	conflict_sha256        digest         optional  conflict: the report body
//	report_pin_id          UUIDv7         optional  conflict: the immediate report pin
//	failure                string         optional  write_failed: ^[a-z][a-z0-9_]*$, at most 64 bytes
//
// Decoder rules: target_commit and target_tree are both present exactly
// when outcome is not conflict. changed_paths is present exactly when
// outcome is fast_forward or merged and may be []; nil in Go means absent,
// while a non-nil empty slice encodes as []. manifest_sha256 and pin_id are
// both present exactly for prepared. conflict_sha256 and report_pin_id are
// both present exactly for conflict. failure is present exactly for
// write_failed. Its v1 tokens are git, index_lock, ref_mismatch,
// verification, cancelled and interference. Failure is open: Encode and
// Decode preserve well-formed unknown tokens; a reader treats one as a
// failure it does not know. Core writes only tokens its reading version
// defines. written_paths is empty exactly when fork_tree equals source_tree.
//
// fast_forward requires parent_commit = fork_commit, target_commit =
// source_commit and target_tree = source_tree. merged requires
// parent_commit != fork_commit and target_commit != source_commit.
// prepared and write_failed require target_commit = source_commit exactly
// when parent_commit = fork_commit. context_id and authoring_context_id
// differ: a temporary never belongs to its parent's context.
//
// Record rules: actor is core; the envelope's context_id is the parent's
// context. scratch_id is an open temporary whose creation parent_scratch_id
// equals parent_scratch_id, an open dedicated scratch of the same workspace.
// authoring_context_id is the source's context and context_id the parent's.
// source_observation_id is the source's latest acknowledged observation;
// its scratch_commit/scratch_tree equal source_commit/source_tree.
// fork_commit/fork_tree equal the source's base_commit/anchor_tree. For
// prepared and conflict, parent_commit/parent_tree equal the parent's
// acknowledged tip at that record.
//
// prepared: body_refs is the sorted distinct union of {manifest_sha256}
// and the body_refs of the capture_inputs_pinned record of inputs_revision.
// The same commit holds, immediately after this record, a ledger.body_pin
// with pin_id and the same body_refs. At most one unresolved prepared
// attempt names each source and each destination. While one is unresolved,
// neither scratch gains a scratch_changed, a closure or another prepared
// attempt.
//
// write_failed repeats the unresolved prepared attempt's plan: every key
// from parent_scratch_id through written_paths is identical except outcome,
// and merge_back_id, workspace_id and scratch_id are the attempt's. It has
// no body_refs and no unpin, and leaves the attempt unresolved.
// fast_forward and merged repeat the same plan with the same exception for
// outcome and the same attempt IDs, including identical target_*. They
// have no body_refs, carry changed_paths as the parent's full set relative
// to its anchor_tree, and are followed in the same commit by the prepared
// pin's ledger.body_unpin. After an applied record the parent's tip is
// target_commit/target_tree, the attempt is resolved and its ID is never
// used again.
//
// conflict is a new ID with no prepared phase. body_refs is exactly
// [conflict_sha256], with an immediate ledger.body_pin with report_pin_id
// and the same body_refs in the same commit. Both tips stay intact. The
// report pin belongs to the parent and is released only by its closure,
// never by a later result. A repeated attempt after a conflict is a new ID.
// No commit index is fixed. inputs_revision is recorded and not above the
// current revision; capture_inputs_sha256 is its digest. Git ID lengths
// match object_format. Nothing here is a canonical request, generation or
// accepted effect.
//
// The workspace service consumes private tips, retry plans, notices and
// retention; C3 consumes passive notices in the task's next natural turn;
// U3/U4/U9 consume permitted private-work projections, and later ticket
// 08/C4 consume admitted provenance. The owner may inspect them. Every
// payload key and body_refs is replay-essential. Exact paths and authors
// are context-private, not outward hints; conflict reports are read only
// through Core's authorized body reads.
//
// F2-07 also adds these keys to existing v1 payloads, in the listed order
// after the existing last key. They have only the generic optional-key
// decoder rules, with no cross-key rule.
//
//	type                          key                     type            presence  rule
//	workspace.scratch_created      parent_scratch_id       UUIDv7          optional  after anchor_tree
//	workspace.promotion_admitted   scratch_observation_id  UUIDv7          optional  after pin_id
//	workspace.promotion_admitted   merge_back_ids          UUIDv7 array    optional  after scratch_observation_id; non-empty, distinct, order preserved
//
// Encode omits a nil or empty merge_back_ids; Decode refuses a present
// empty array, null, non-array, non-string, non-v7 or repeated element.
// No sorting is performed. All three keys are replay-essential when
// present. The following record rules are conditional on the new key,
// type or reason; logs without F2-07 records remain valid.
//
// Creation with parent_scratch_id: the parent is an open scratch_created
// without a parent key, in the same workspace_id and with a different
// context_id. The pair (context_id, workspace_id) may have an open dedicated
// scratch: F2-05's one-open-scratch-per-pair rule governs records without
// the parent key, and a temporary is not the pair's scratch.
// base_commit/anchor_tree equal the parent's acknowledged tip at creation.
// dir and branch follow the existing never-reused and
// refs/heads/dwarf-harness/<dir> rules.
//
// Closure with reason temporary_merged_back requires a creation record
// with a parent key and an acknowledged applied merge-back naming the
// scratch as source whose source_observation_id is the scratch's latest
// observation, with no scratch_changed after that merge-back. Actor is
// agent with the envelope's context_id equal to the scratch's, or owner.
// abandoned on a temporary requires no unresolved prepared attempt naming
// it as source; the service additionally proves that its files equal its
// acknowledged tip, a producer rule. A dedicated scratch has no
// scratch_closed while a scratch naming it as parent is open or a prepared
// attempt targeting it is unresolved. Every closure commit holds, after
// scratch_closed, one ledger.body_unpin for each still-active pin the
// scratch owns: its latest observation-manifest pin, conflict-report pins
// of merge-backs that targeted it, and F2-06's report pins, in log order of
// their pins, each once.
//
// Admission provenance: scratch_observation_id is the scratch's latest
// acknowledged observation at admission, absent exactly when it has none.
// merge_back_ids lists in acknowledged sequence order the applied
// merge-backs targeting this scratch whose applied record's sequence is
// greater than the parent admission's (greater than the scratch's creation
// for a root or new root) and less than this admission's. It is absent
// exactly when there are none. Replay checks membership, order and ancestry
// from records and reads no body or Git.
//
// Core writes scratch-rules/1, which names the pinned host observation
// against the scratch's acknowledged tip with the trusted names of its
// anchor and tip, the separation of the raw manifest from the canonical
// tree, the deterministic single-parent Core commit (its identity, the date
// from the observation UUIDv7 and the message dwarf-harness scratch
// observation, a blank line, scratch <ID> and observation <ID>), and the
// order in which the retention ref, the record and the private branch move.
//
// Core writes scratch-merge-rules/1, which names the explicit fork base
// passed to git merge-tree --write-tree in the integration environment of
// integration-rules/1, the derived-tree case check, fast-forward by commit
// identity, the deterministic single-parent merge commit on the parent
// (message dwarf-harness scratch merge-back, a blank line, scratch <ID> and
// merge-back <ID>), the materialization recipe (fresh private index, refresh,
// obstruction preflight, two-tree checkout, full-tree verification, owned
// index lock, branch compare-and-swap last) and the prepared/result protocol.
// Each value is Core policy named in the record: any change to those rules
// is a new value and needs no new type version; integration-rules/1 is
// unchanged.
//
// The F2-07 golden at testdata/f2/07/log/00000000000000000001.jsonl holds
// twenty-two commits and forty-one records. scratch_created with a parent
// appears at seqs 8, 17 and 30; scratch_changed at 5, 9, 18, 20, 31 and 33;
// scratch_merge_back at 11, 13, 23, 25, 26 and 36; scratch_closed with
// temporary_merged_back at 15 and 28 and abandoned on a temporary at 38;
// promotion_admitted with provenance at 40. It pins a temporary beside its
// author's dedicated scratch, task and source observations with the
// previous manifest unpinned, a fast-forward and an explicit-base merge
// each with its prepared witness and unpin, a write failure between prepared
// and applied, a conflict with its report pinned and surviving the source's
// abandonment, both temporary closure reasons, and a root admission bound
// to the latest observation and both applied merge-backs. The workspace
// uses sha1; tests cover 64-digit Git IDs separately.
//
// # F2-08 record types
//
// These three version-1 types are ticket 08's part of F2, frozen by
// addition to format version 1 (D123). C2 owns their
// payload specifications. Only Core's workspace service
// (internal/core/workspace) may append them, through appender.Appender;
// permitted callers are enforced by review. The service checks the record
// rules both when appending and at Open. Check, Reader, EncodeCommit and
// these payload encoders and decoders do not enforce service record rules.
// C2's product still admits nothing. The mechanism that writes these records
// is ticket 08's S3, reached only by package tests until C4 (D52).
//
// Payloads follow F2-07's common encoding and repository-path rules above,
// except that promotion_accepted.changed_paths is the one required path
// array that may not be empty: neither Encode nor Decode substitutes [].
// The optional integer predecessor_witness_seq is omitted when zero in Go
// and refused when present with 0, null, a negative or non-integer value.
// All Git IDs within one F2-08 payload have the same length, 40 or 64
// lowercase hex digits; matching the workspace's object_format remains a
// service record rule.
//
// predecessor_witness_seq names the sequence of the workspace's latest
// workspace.promotion_accepted. Its presence is a service record rule, not a
// decoder rule: present exactly when the workspace has an acknowledged
// promotion_accepted before this record, naming the latest one, whose
// accepted_commit/accepted_tree equal this record's
// predecessor_commit/predecessor_tree. Absent, the predecessor is canonical
// main as the service observed it at append, F2-06's two-halves rule (D118).
// A generation naming a train predecessor carries no witness; its special
// presence rule is given with the additions below.
//
// workspace.finalization_intended v1 (FinalizationIntended) binds one
// intended canonical update to its request and generation.
//
//	key                      type           presence  rule
//	intent_id                UUIDv7         required  a new intent
//	request_id               UUIDv7         required  the leading live admitted request
//	generation_id            UUIDv7         required  its current clean generation
//	predecessor_witness_seq   integer        optional  1 through MaxInt64; the witness rule above
//	predecessor_commit        Git object ID  required  the generation's predecessor, canonical main
//	predecessor_tree          Git object ID  required  that predecessor's tree
//	target_commit             Git object ID  required  the generation's derived_commit
//	target_tree               Git object ID  required  the generation's derived_tree
//	finalization_rules        string         required  ^[a-z][a-z0-9-]*/[1-9][0-9]*$, at most 64 bytes
//
// Decoder rules: predecessor_commit differs from target_commit and
// predecessor_tree differs from target_tree. A tree-equal generation is
// no-change, never an intent. predecessor_witness_seq, when present, is at
// least 1, and all four Git IDs have one length.
//
// Record rules: actor is core; the envelope's context_id is the request's;
// body_refs is absent. No commit index is fixed. intent_id is new.
// request_id is admitted and live, with no live request ahead of it in its
// workspace's train, no earlier finalization_intended naming it, and no
// unresolved intent in the workspace. There is one unresolved intent per
// workspace: no later intent before the predecessor's proven durable
// outcome. generation_id is the request's current clean generation, not
// superseded. predecessor_commit/predecessor_tree equal its predecessor
// commit and tree, and target_commit/target_tree its derived commit and tree.
// predecessor_witness_seq follows the witness rule above. When the generation
// carries predecessor_request_id, that request has since ended by
// promotion_accepted, that acceptance is the latest witness, and its accepted
// commit and tree are the generation's predecessor: a speculative generation
// stays finalizable without a rebuild.
//
// While the intent is unresolved, there is no later finalization_intended in
// the workspace, no generation_derived superseding its generation, no
// promotion_failed for the request other than interrupted, and no
// promotion_no_change for it. The request stays live, so its scratch has no
// scratch_closed under F2-06's closure rule. The intent is resolved by
// exactly one promotion_accepted with the same intent_id or one
// promotion_failed with outcome interrupted for the request, as below.
//
// Producer rules, proved by the service and not replay: the intent is
// appended and acknowledged before the one guarded update of refs/heads/main.
// Cancellation after the intent answers too late.
// Durability of the update is established before the acceptance is appended.
// The request's checkpoint slot is released only after the acceptance or the
// ending is acknowledged.
// Before the intent, the service preflights the encoded acceptance against
// MaxRecordBytes and MaxCommitBytes and refuses as pathset_cap without
// truncation or chunking.
// Evidence and policy/check/environment/law revision bindings join by
// addition when F4, ticket 09 and U1.L freeze them.
// F2-09 adds an optional activation_record_hash binding the active manifest;
// a log without F2-09 records is unaffected.
//
// workspace.promotion_accepted v1 (PromotionAccepted) resolves an intent
// successfully and witnesses the workspace's accepted canonical tip.
//
//	key                      type           presence  rule
//	intent_id                UUIDv7         required  the unresolved intent this record resolves
//	request_id               UUIDv7         required  that intent's request
//	generation_id            UUIDv7         required  that intent's generation
//	predecessor_witness_seq   integer        optional  1 through MaxInt64; the witness rule above
//	predecessor_commit        Git object ID  required  that intent's predecessor_commit
//	predecessor_tree          Git object ID  required  that intent's predecessor_tree
//	accepted_commit           Git object ID  required  exactly that intent's target_commit
//	accepted_tree             Git object ID  required  exactly that intent's target_tree
//	changed_paths             path array     required  non-empty exact predecessor_tree to accepted_tree set
//
// changed_paths uses F2-07's path grammar and byte ordering, includes
// deletions and both names of a rename, and names paths, not file content.
// A normal and a reconciled acceptance are the same record with the same
// consumer effects: there is no recovery flag.
//
// Decoder rules: predecessor_commit differs from accepted_commit and
// predecessor_tree differs from accepted_tree. changed_paths is non-empty,
// well-formed, byte-sorted and distinct; nil, null, absent and non-array
// values are refused. predecessor_witness_seq, when present, is at least 1;
// all four Git IDs have one length.
//
// Record rules: actor is core; the envelope's context_id is the request's;
// body_refs is absent. intent_id is the workspace's unresolved intent.
// request_id, generation_id, predecessor_witness_seq, predecessor_commit,
// predecessor_tree, accepted_commit and accepted_tree equal that intent's
// bindings, accepted_* its target_*. The same commit holds, after this
// record, one ledger.body_unpin of the request's admission pin_id, once,
// and never a report unpin. changed_paths is the exact
// predecessor-tree-to-accepted-tree set, computed by the service; replay
// does not recompute it.
//
// After the commit, the request has ended successfully: it leaves the train,
// its slot is released on acknowledgement, and its lineage does not fail.
// Live requests of the lineage with larger checkpoints stay live: F2-06's
// whole-suffix rule applies to promotion_failed only. This record becomes
// the workspace's latest witness. The scratch's represented tree becomes
// the admission's sealed_tree. A second acceptance for the same intent or
// request is inconsistent.
//
// workspace.promotion_no_change v1 (PromotionNoChange) ends a request
// successfully without an intended or accepted canonical effect.
//
//	key                      type           presence  rule
//	request_id               UUIDv7         required  the leading live admitted request
//	generation_id            UUIDv7         required  its current clean generation
//	predecessor_witness_seq   integer        optional  1 through MaxInt64; the witness rule above
//	predecessor_commit        Git object ID  required  that generation's predecessor_commit
//	predecessor_tree          Git object ID  required  that generation's predecessor_tree
//
// Decoder rules are only the generic ones: required UUIDv7s, an optional
// integer at least 1 when present, and two Git IDs of one length. Tree
// equality with the generation and witness is a service record rule.
// There is no intent_id, changed_paths or body_refs.
//
// Record rules: actor is core; the envelope's context_id is the request's;
// body_refs is absent. The request is admitted, live and leading, and has
// no finalization_intended. generation_id is its current clean generation,
// whose predecessor_commit/predecessor_tree equal this record's and whose
// derived_tree equals predecessor_tree. Trees, never commits, are compared:
// a different derived commit with the same tree is no-change.
// predecessor_witness_seq follows the witness rule above. The same commit
// holds, after this record, the admission's manifest unpin, once, never a
// report unpin. The request ends successfully as above; the witness does
// not advance, and the represented tree becomes the admission's sealed_tree.
// A child derived over this generation's derived_commit is stale and
// rebuilds against the witness, a service rule. Nothing here is passing
// validation, a delivered effect, pressure or a field source.
//
// F2-08 also adds this optional key as the last key of an existing v1
// payload, after report_pin_id:
//
//	type                          key                      type     presence  rule
//	workspace.generation_derived   predecessor_witness_seq  integer  optional  1 through MaxInt64; the witness rule above
//
// Decoder rule: when predecessor_witness_seq is present, both
// predecessor_request_id and predecessor_generation_id are absent.
// The F2-06 golden pins the byte-identical absent form.
// workspace.promotion_failed gains no key: cancelled and interrupted are
// new tokens under F2-06's existing open outcome grammar. Every reader
// already accepts them with either optional key present or absent; the
// decoder and encoding rules are unchanged.
//
// The following record rules are conditional on the new token, key or type;
// logs without F2-08 records remain valid.
//
// promotion_failed with outcome cancelled: actor is owner, or agent with
// the envelope's context_id equal to the request's. generation_id and
// failed_request_id are absent. The request is admitted, live and has no
// unresolved finalization_intended. F2-06's rules apply unchanged: later
// records in the commit end the lineage's live higher checkpoints as
// dependency_failed naming it, then the manifest unpins in ending order,
// never a report unpin.
//
// promotion_failed with outcome interrupted: actor is core; both optional
// keys are absent; the request is admitted and live. When it has an
// unresolved intent, this ending resolves it, and no promotion_accepted
// with that intent_id may follow. The suffix and unpin rules are as above.
//
// generation_derived with predecessor_witness_seq: present exactly when the
// workspace has an acknowledged promotion_accepted before it and the
// generation's predecessor is canonical main, with no predecessor IDs.
// It names the latest acceptance, whose accepted commit and tree equal
// predecessor_commit/predecessor_tree. A generation at the train's head
// without the key is valid only while the workspace has no acceptance.
//
// Representation: a dedicated scratch's represented tree is the sealed_tree
// of its latest request ended by promotion_accepted or promotion_no_change,
// else its anchor_tree. A new root's baseline_tree and delta_base_tree equal
// it, making F2-06's new-root case concrete; a successor keeps its parent's
// sealed_tree. Neither base_commit nor anchor_tree changes, and acceptance
// records no closure.
//
// Recovery-pending is a service rule: a workspace whose log holds live
// requests at Open is recovery-pending; the service refuses admission,
// integration, intents and no-change for it until its bookkeeping phase has
// ended those requests (interrupted, with suffixes dependency_failed, D55),
// the unresolved intent, if any, by the conditional mapping (interrupted at
// the old tip, accepted at the intended tip, blocked otherwise); cancellation
// of a pre-intent request and status queries remain.
// Replay enforces record facts; it reads no Git, body or clock.
//
// Core writes finalization-rules/1, which names the files ref backend; the
// checks before the canonical update (refs/heads/main a direct ref, no
// worktree with main checked out, the target's tree and its sole parent equal
// to the expected old tip); the one guarded
// git update-ref --no-deref --end-of-options refs/heads/main <target> <old>
// under the pinned core.fsync=committed and core.fsyncMethod=fsync
// configuration; the directory flushes of the object and ref directories
// after it and the re-read of main that establishes durability; the
// no-change comparison by tree against the latest witness; and the order
// intent, update, durability, acceptance, slot release. The value is Core
// policy named in the record: any change to those rules is a new value and
// needs no new type version; integration-rules/1, scratch-rules/1 and
// scratch-merge-rules/1 are unchanged.
//
// The workspace service consumes intents, witnesses, representation and
// retention. C3 consumes terminal results for the original context (D63);
// C4 consumes the witness as the predecessor binding of its evidence;
// U1.E, U3 and U4 consume accepted deltas when their gates permit; the owner
// may inspect them. Every payload key is replay-essential. Identifiers,
// Git IDs and paths are host-private and original-context scoped.
// changed_paths names repository paths, never content. An intent, a
// no-change or a too-late cancellation is never a delivered effect.
//
// The F2-08 golden at testdata/f2/08/log/00000000000000000001.jsonl holds
// twenty-two commits and thirty-nine records. finalization_intended appears
// at seqs 8, 27 and 33; promotion_accepted at 9 and 34; promotion_no_change
// at 14; generation_derived with the witness key at 13, 26 and 32;
// promotion_failed cancelled at 20, its cascade at 21, interrupted resolving
// an intent at 28 and pre-intent at 38. It pins a first acceptance witnessing
// main as observed, a no-change over it with the same tree and a different
// commit, an owner cancellation with its cascade, a new root over the
// represented tree, an intent interrupted at the old tip, a chained
// acceptance naming the first as witness, and a queued successor interrupted
// before any intent. The workspace uses sha1; tests cover 64-digit Git IDs
// separately.
//
// # F2-09 record types
//
// These four version-1 types are ticket 09's part of F2, frozen by
// addition to format version 1 (D124). C2 owns their
// payload specifications. Only Core's workspace service
// (internal/core/workspace) may append them, through appender.Appender;
// permitted callers are enforced by review. The service checks the record
// rules both when appending and at Open. Check, Reader, EncodeCommit and
// these payload encoders and decoders do not enforce service record rules.
// C2's product still answers no_validator. Ticket 09's S3a supplies the
// mechanism that writes these records, reached only by package tests until
// C3/C4 (D52).
//
// Payloads follow F2-07's common encoding rules above. UTC ns fields are
// signed int64 JSON decimal integers, from MinInt64 through MaxInt64:
// zero and either sign are permitted, but -0, leading zeros, fractions,
// exponents, strings, null and overflow are not. Ordering against earlier
// records and the live clock is a service rule; the local comparisons below
// are decoder rules. workspace_policies and affected_workspace_ids are
// required arrays, possibly [], never null; Encode substitutes [] for nil.
// A workspace_policies element emits workspace_id, check_policy_hash and
// environment_policy_hash in that order: a canonical UUIDv7 and two non-zero
// digests. Elements are strictly ascending by the workspace ID's 16 bytes.
// affected_workspace_ids contains canonical UUIDv7 strings in the same
// strict order. Neither array permits duplicate IDs. observed_utc_ns is
// present exactly as hold requires. outcome is an open token,
// ^[a-z][a-z0-9_]*$, at most 64 bytes.
//
// workspace.policy_activation_requested v1 (PolicyActivationRequested)
// records the owner's exact approval of a retained target and schedule.
//
//	key                          type           presence  rule
//	request_id                   UUIDv7         required  new request; later the activation_id
//	owner_approval_id            UUIDv7         required  new exact approval, never reused
//	field_law_sha256             digest         required  retained law artifact
//	validation_policy_hash       digest         required  retained validation policy artifact
//	workspace_policies           array          required  the complete local policy map
//	affected_workspace_ids       UUIDv7 array    required  derived scope, strictly ascending
//	expected_active_record_hash  digest         optional  latest activation's envelope hash; absent at genesis
//	schedule                     string         required  immediate or not_before
//	admission_utc_ns              signed integer required  trusted admission observation
//	not_before_utc_ns             signed integer required  eligibility instant, not a promised switch time
//	replaces_request_id          UUIDv7         optional  request ended immediately before this record
//	replaces_revision            integer        optional  its current revision, 1 through MaxInt64
//	pin_id                       UUIDv7         required  new permanent artifact pin
//
// Decoder rules: schedule is closed. immediate requires not_before_utc_ns
// equal to admission_utc_ns; not_before requires it strictly greater.
// replaces_request_id and replaces_revision are both present or both absent;
// replaces_request_id differs from request_id. The two global digests and
// every element's two digests are non-zero. request_id, owner_approval_id
// and pin_id are distinct.
//
// Record rules: actor is owner; agent_id, execution_session_id, context_id,
// turn_id and operation_id are absent. body_refs is exactly the sorted
// distinct union of field_law_sha256, validation_policy_hash and every
// element's check_policy_hash and environment_policy_hash. A ledger.body_pin
// with pin_id, the same actor and body_refs and no caller-context fields
// immediately follows. This pin is permanent: a ledger.body_unpin naming it
// is inconsistent. The request, approval and pin IDs are new. The commit
// contains only this request at index 0 and its pin at index 1, or an ended
// with outcome replaced at index 0 naming this request, this request at
// index 1 and its pin at index 2. There is no pending request before this
// record: one pending request per colony. Every workspace_policies element
// names a registered workspace. expected_active_record_hash equals the
// latest policy_activated envelope hash, or is absent when none exists.
// affected_workspace_ids is derived against that manifest, or the empty
// genesis map: the union of its and this record's workspace IDs when law
// or validation policy differs, else exactly the IDs whose entry is added,
// removed or changed; at genesis, this record's workspace IDs. Every
// affected ID is registered. Replacement keys name the ended at index 0
// and its expected_pending_revision. The initial revision is this request
// record's sequence; thereafter it is the sequence of the latest
// hold_changed or ended for the request.
//
// Producer rules, proved by the service and not replay: every artifact's
// bytes were verified against its digest, law and policy loaded through
// the frozen contract/law loaders, and all bytes published before the
// request commit. Check and environment bytes have identity, not interpreted
// meaning. Approved bytes remain retained for the life of the log, even
// after replacement, cancellation or activation. admission_utc_ns is a
// trusted live sample; a not_before schedule is strictly after it. The
// pending request is the owner's authorization until CAS cancellation or
// replacement; reapproval is a new request with new identities.
//
// workspace.policy_activation_hold_changed v1 (PolicyActivationHoldChanged)
// changes only the pending request's activation hold.
//
//	key                             type           presence     rule
//	request_id                      UUIDv7         required     pending request
//	activation_request_record_hash  digest         required     that request's envelope hash
//	hold                            string         required     due, clock_untrusted or none
//	observed_utc_ns                  signed integer conditional  present exactly for due or none
//
// Decoder rules: hold is closed. due and none require observed_utc_ns,
// including a present zero; clock_untrusted refuses that key. The Go field
// is a plain int64: Encode omits it for clock_untrusted and refuses a
// nonzero value in that case. An untrusted clock supplies no fabricated
// instant; a known observation raises the durable clock floor.
//
// Record rules: actor is core; all caller-context fields and body_refs are
// absent; this is a one-record commit. request_id names the pending request
// and the hash names its request record. hold differs from the current hold,
// initially no hold. due requires observed_utc_ns at least not_before_utc_ns
// and at least the durable clock floor: the greatest recorded admission_utc_ns,
// due/none observed_utc_ns and effective_utc_ns before it. none requires a
// current clock_untrusted hold and an observation at least the floor but
// below not_before_utc_ns. A due hold is final until the request ends or
// applies. clock_untrusted is appendable from no hold or none. A known
// observation raises the floor; this record's sequence becomes the revision.
//
// workspace.policy_activation_ended v1 (PolicyActivationEnded) ends an
// approval request without applying its target.
//
//	key                             type     presence  rule
//	request_id                      UUIDv7   required  pending request
//	activation_request_record_hash  digest   required  that request's envelope hash
//	expected_pending_revision       integer  required  current revision, 1 through MaxInt64
//	outcome                         string   required  open token; v1 writes cancelled and replaced
//	replacement_request_id          UUIDv7   optional  required for replaced, refused for cancelled
//
// Decoder rules: replaced requires replacement_request_id differing from
// request_id; cancelled refuses it. Every other well-formed outcome accepts
// that key present or absent. A reader treats an unknown outcome as an ending
// it does not know: terminal and not applied, never invalid. C3's revocation
// of an approval, when F3 defines it, joins as an additive ended outcome
// under this open token grammar.
//
// Record rules: actor is owner for cancelled and replaced; caller-context
// fields and body_refs are absent. request_id and its hash name the pending
// request; expected_pending_revision equals its current revision. cancelled
// is a one-record commit; replaced is index 0 of the three-record group above,
// and replacement_request_id is the request at index 1. The ended request
// is terminal and leaves the pending slot, which a replacement fills in the
// same commit. Only that request's hold is removed: ticket 08's unresolved-
// intent and recovery-pending fences are untouched, and no approval pin is
// unpinned. Producers write only outcomes their version defines; later
// additions define their own actor and grouping rules.
//
// workspace.policy_activated v1 (PolicyActivated) switches the colony's
// active manifest once, at an actual trusted instant.
//
//	key                             type           presence  rule
//	activation_id                   UUIDv7         required  pending request_id
//	epoch                           integer        required  previous epoch plus 1; 1 through MaxInt64
//	field_law_sha256                digest         required  requested law artifact
//	validation_policy_hash          digest         required  requested validation policy artifact
//	workspace_policies              array          required  requested complete local map
//	owner_approval_id               UUIDv7         required  requested approval
//	activation_request_record_hash  digest         required  request record's envelope hash
//	expected_active_record_hash     digest         optional  absent exactly at epoch 1
//	not_before_utc_ns                signed integer required  requested eligibility instant
//	effective_utc_ns                 signed integer required  actual switch instant
//
// Decoder rules: effective_utc_ns is at least not_before_utc_ns;
// expected_active_record_hash is absent exactly when epoch is 1;
// activation_id and owner_approval_id differ; required digests are non-zero
// and workspace_policies is strictly ascending. effective_sequence and this
// activation record's own hash are the envelope's Seq and Record.Hash, never
// payload members: the manifest tuple of DESIGN-UNDERCURRENT (lines 456–458)
// and contract/law (lines 242–248) needs the actual committed identity,
// not a self-hash or a guessed sequencer number.
//
// Record rules: actor is core; caller-context fields are absent; this is a
// one-record commit. body_refs is the request's union, already permanently
// pinned, with no new pin. activation_id is the pending request, whose
// current hold is due. owner_approval_id, both global digests,
// workspace_policies, activation_request_record_hash,
// expected_active_record_hash and not_before_utc_ns equal that request's.
// effective_utc_ns is at least not_before_utc_ns and the durable clock floor.
// epoch is the preceding activation's plus 1, or 1 at genesis; exhaustion
// cannot wrap. No workspace in affected_workspace_ids has an unresolved
// finalization_intended. After the commit the manifest and its envelope
// hash and sequence are the colony's active identity, the request is applied
// once and its hold is gone.
//
// Producer rules, not replay: immediately before append, under the sequencer,
// the switch rechecks the pending ID and revision, retained bytes' presence
// and digests, trusted time and absence of affected unresolved intents.
// effective_utc_ns is never backdated to the planned instant.
//
// F2-09 also adds this optional last key to an existing v1 payload, after
// finalization_rules, with only the generic optional-digest decoder rule:
// present zero or null is refused.
//
//	type                             key                     type    presence  rule
//	workspace.finalization_intended   activation_record_hash  digest  optional  active manifest's envelope hash
//
// This names the policy_activated record current when the intent was
// appended, with a workspace_policies entry for that intent's workspace.
// Its presence is a service record rule, not a decoder rule. Every intent
// before the log's first policy_activated may omit it; once one exists,
// every new workspace intent carries it and an unconfigured workspace
// cannot intend. No key is added to promotion_accepted, promotion_no_change
// or generation_derived.
//
// Replay reconstructs the pending request, revision, hold, active manifest,
// epoch and clock floor from committed envelopes and hashes; it reads no
// clock, starts no timer, publishes no body, runs no Git, check or model
// work and appends nothing. The service alone samples time, arms one timer,
// verifies artifact bytes and performs the switch. Actual request and
// activation hashes come from committed envelopes read back after
// acknowledgement, never predicted from Durable()+1 or hashed into payloads;
// that bounded read scans from the start of the commit's segment, the same
// segment-bounded cost ledgerstore's rescans pay. The conditional intent-key
// rule applies only after the first activation; a stale or wrong-presence
// expected manifest is refused at admission and never recorded; a log
// without F2-09 records is unaffected.
//
// The workspace service consumes every key for the pending slot, holds,
// active manifest, clock floor and retention. C3 consumes controls, status
// and recovery for the owner. C4 consumes the active manifest and intent
// binding as the policy, check, environment and law revisions of its
// evidence. U1.E consumes law activation when its gate permits; the owner
// may inspect these records. Every payload key is replay-essential, and
// body_refs on the request and the activation.
// Workspace IDs, approvals, artifact digests, schedules and manifests are
// host-private, never federation exports. A control is never accepted
// delivery, field activity, permission to run checks or proof of engine
// readiness.
//
// The F2-09 golden at testdata/f2/09/log/00000000000000000001.jsonl holds
// twenty-two commits and thirty-four records. policy_activation_requested
// appears at seqs 7, 11, 17, 20 and 24; policy_activation_hold_changed at
// 9, 13, 14, 15, 22 and 26; policy_activation_ended replaced at 16 and
// cancelled at 19; policy_activated at 10, 23 and 27; finalization_intended
// with activation_record_hash at 32. It pins a genesis activation of an
// immediate request, a scheduled request held for an untrusted clock,
// released and then due, replaced by CAS in one commit, its replacement
// cancelled, a checks-only change affecting one workspace activated later
// than scheduled, a global change over it, and an intent binding the
// manifest then current. W1 uses sha1; tests cover 64-digit Git IDs in the
// extended intent separately.
//
// # F-U3-61 record types
//
// These four version-1 types are ticket 61's part, U3's first producer
// family, frozen by addition to format version 1 (D130). U3 owns their
// payloads and the graph. namespace. Only internal/undercurrent/graph may
// append them through appender.Appender, enforced by review and tools/check's
// appender rule. The service checks record rules when appending and at Open;
// these codecs enforce payload rules only. No producer exists until ticket
// 61's S3. Nothing here is accepted promotion, an engine source or U5 readiness.
//
// Every payload and nested-object key below is required. UUIDs are canonical
// lowercase UUIDv7s; digests are non-zero 64-digit lowercase SHA-256 strings.
// Integers use nat: plain decimal 0 through MaxInt64, with no signs (-0
// included), fractions, exponents, quoted numbers, null or overflow. Repeated
// keys at any depth are invalid; unknown keys at every depth are ignored.
// Required arrays encode [] for nil, never null.
//
// Node kinds, adapters, edge types, geometries, reasons and scopes are open
// tokens, ^[a-z][a-z0-9_]*$, at most 64 bytes; later values join by addition
// in their own freeze records. Namespaces use the same open grammar. This
// part emits artifact and module nodes and static_dependency coupling edges
// only. Version-1 writer values are namespace workspace; adapters opaque,
// python, typescript, sql and go (approved by D132 under D29), with checks,
// declarations, resources and usage representing workspace-wide unknowns;
// scope canonical; analysis reasons incremental, load, structural_100,
// analyzer_epoch and digest_mismatch;
// coverage reasons unsupported_syntax, unresolved_reference,
// adapter_unavailable, dependency_unapproved, upstream_unfrozen,
// source_unavailable, work_exhausted, state_cap, temporary_cap, helper_limit,
// stale_source and recovery_unknown. phase and state are closed. Profiles
// use the existing [a-z][a-z0-9-]*/[1-9][0-9]* grammar, at most 64 bytes:
// engine-limits/1 and graph-helper-limits/1 are the version-1 writer values
// (engine-limits/1 spells DESIGN-UNDERCURRENT §13.7's engine-limits/v1 profile).
//
// Common value shapes emit their keys in the following order:
//
//	value          keys
//	RecordRef      seq, hash
//	GraphSource    workspace_id, registered, subject, tree
//	NodeKey        namespace, origin_id, kind, local_key
//	GraphNode      key, version_sha256, adapter, configuration_sha256
//	EdgeEvidence   artifact, artifact_sha256, adapter, configuration_sha256, reference
//	GraphEdge      edge_id, from, to, type, geometry, weight_ppm, evidence
//	CoverageEntry  scope, adapter, state, reasons
//	GraphCursor    artifact_index, output_index
//
// A RecordRef has seq 1 through MaxInt64 and the envelope's hash. A GraphSource
// has a workspace UUIDv7, registered.seq < subject.seq and a 40- or 64-digit
// lowercase Git tree ID. NodeKey origin_id is UUIDv7; local_key is non-empty
// valid UTF-8 of at most 4,096 bytes. Its kind-specific tuple is a service rule.
// GraphNode carries a valid key, two non-zero digests and an adapter token.
// EdgeEvidence carries a valid artifact key, two non-zero digests, an adapter
// token and a non-empty valid UTF-8 reference of at most 4,096 bytes.
// GraphEdge requires distinct from/to keys, open type and geometry tokens,
// weight_ppm 1 through 1,000,000, and edge_id equal to EdgeID(from,to,type):
// SHA-256 of the compact JSON array [from,to,type], with keys encoded in the
// NodeKey order above and type as a JSON string, every string escaped as this
// package's encoder (encoding/json) escapes it. Reversing endpoints or changing
// type changes identity; replacing evidence does not.
//
// Coverage scope is non-empty valid UTF-8 of at most 8,192 bytes; its canonical
// NodeKey JSON string or workspace:<uuid>:canonical form is a service rule.
// Adapter is an open token. State is complete or unknown; complete iff reasons
// is []. Reasons are open tokens strictly ascending by bytes. NodeKey arrays
// sort by their compact JSON bytes, nodes by their key's bytes, edges and
// remove_edges by the 32 edge-ID bytes, and coverage by (scope,adapter) bytes.
// Every order is strict and therefore duplicate-free. Both cursor indices are
// non-negative. EmptyGraphDigest hashes the exact bytes
// {"scopes":[],"nodes":[],"edges":[],"coverage":[]};
// EmptyChunksDigest hashes zero bytes.
//
// graph.analysis_started v1 (GraphAnalysisStarted) starts one attempt.
//
//	key                    type         rule
//	analysis_id            UUIDv7       attempt identity
//	analysis_key           digest       deduplication identity
//	source                 GraphSource accepted immutable source
//	input_frontier         RecordRef    colony's latest preceding acceptance
//	scope                  token        v1 writes canonical
//	base_version           integer      0 through MaxInt64
//	base_digest            digest       current effective graph digest
//	analyzer_epoch         integer      1 through MaxInt64
//	configuration_sha256   digest       analyzer configuration identity
//	limits_profile         identifier   v1 writes engine-limits/1
//	helper_profile         identifier   v1 writes graph-helper-limits/1
//	reason                 token        why this attempt starts
//
// Decoder rules: source.subject.seq <= input_frontier.seq; base_version is
// zero iff base_digest equals EmptyGraphDigest. Service rules: analysis_key
// hashes the compact tuple [source,input_frontier,scope,base_version,base_digest,
// analyzer_epoch,configuration_sha256,limits_profile,helper_profile]; reason is
// excluded. registered names workspace.registered for workspace_id; subject
// names workspace.promotion_accepted whose request_id was admitted in that
// workspace by an earlier workspace.promotion_admitted and whose accepted_tree
// equals tree. input_frontier is the latest workspace.promotion_accepted in
// the colony before this record. A rebuild's source is that frontier record's
// own subject. The base is the current effective version and digest, initially
// 0 and EmptyGraphDigest. Only one attempt is active per colony; a repeated key
// returns its existing result before any work.
//
// graph.analysis_progress v1 (GraphAnalysisProgress) reserves or checkpoints
// work, or closes an attempt without a version.
//
//	key              type            rule
//	analysis_id      UUIDv7          attempt identity
//	analysis_key     digest          deduplication identity
//	revision         integer         1 through MaxInt64
//	phase            string          reserved, checkpoint, unknown or superseded
//	reserved_total   integer         0 through MaxInt64
//	verified_cursor  GraphCursor     durably verified position
//	coverage         CoverageEntry[] required, possibly []
//
// Decoder rules: unknown and superseded require non-empty coverage with every
// entry unknown. reserved and checkpoint may carry []. Service rules: revision
// is the previous progress revision plus one for this analysis_id; reserved
// raises reserved_total by 1..min(10,000,remaining) before any helper, Git or
// rebuild work. Nothing is refunded. The engine-limits/1 cap of 2,000,000 is
// reached at most once per key and is terminal: unknown with work_exhausted.
// checkpoint advances verified_cursor only over durably committed chunks.
// unknown and superseded close the attempt. The sticky exhaustion key is
// (source,scope,analyzer_epoch,configuration_sha256,limits_profile,helper_profile),
// persisted by the terminal unknown record and checked before any started.
//
// graph.delta_chunk v1 (GraphDeltaChunk) holds invisible net mutations.
//
//	key           type            rule
//	analysis_id   UUIDv7          attempt identity
//	analysis_key  digest          deduplication identity
//	chunk_index   integer         0 through MaxInt64
//	remove_edges  digest[]        required, possibly []
//	remove_nodes  NodeKey[]       required, possibly []
//	put_nodes     GraphNode[]     required, possibly []
//	put_edges     GraphEdge[]     required, possibly []
//	coverage      CoverageEntry[] required, possibly []
//
// Decoder rules: the common element and order rules apply; no key is both
// removed and put, and no edge ID is both removed and put. Producer rules:
// payloads are at most 512 KiB, split greedily at whole elements in the key
// order above, never splitting an element or string. Chunks are sequential,
// immutable and invisible until an eligible final record. State forms in
// remove-edges, remove-nodes, put-nodes, put-edges, coverage order across chunk
// boundaries. Dangling endpoints and incident edges are history errors, never
// cascaded. Only net mutations are carried: unchanged values are not re-put.
//
// graph.version_committed v1 (GraphVersionCommitted) finishes one analysis.
//
//	key                       type     rule
//	analysis_id               UUIDv7   attempt identity
//	analysis_key              digest   deduplication identity
//	base_version              integer  0 through MaxInt64
//	base_digest               digest   starting graph
//	chunk_count               integer  0 through MaxInt64
//	chunks_sha256             digest   concatenated chunk payload bytes
//	graph_digest              digest   canonical resulting graph state
//	structural_since_rebuild  integer  0 through MaxInt64
//
// Decoder rules: base_version is zero iff base_digest equals EmptyGraphDigest;
// chunk_count is zero iff chunks_sha256 equals EmptyChunksDigest iff graph_digest
// equals base_digest. Service rules: chunks_sha256 hashes concatenated compact
// GraphDeltaChunk payload bytes in index order. graph_digest hashes canonical
// state {"scopes":[...],"nodes":[...],"edges":[...],"coverage":[...]}; scopes
// are strictly ascending by workspace UUID bytes, each containing
// {source,scope,analyzer_epoch,configuration_sha256} in that order, with nodes,
// edges and coverage ordered as above. Nothing operational enters that hash:
// no task IDs, timings, counters or chunk splits. With positive chunk_count,
// the effective graph version is this record's own envelope sequence. With
// zero chunk_count it stays base_version: a confirmation records the rebuild
// and resets the counter without advancing the version.
//
// Replay derives structural_since_rebuild from the prefix: distinct
// workspace.promotion_accepted records since the last eligible rebuild, every
// one with non-empty changed_paths under F2-08's rule. A mismatch is ErrHistory.
// A final record is instead superseded without effect if its base version or
// digest is no longer current, its source.subject is not its workspace's
// latest acceptance, or the colony's latest acceptance preceding this record
// is not input_frontier; eligibility is checked before the claimed counter.
//
// Record rules for every graph type: actor core; agent_id,
// execution_session_id, context_id and turn_id absent; operation_id equals
// analysis_id; measurement_path graph.analysis; body_refs absent. Commit
// shapes are one started; one reserved; one or more delta_chunk records
// followed by their checkpoint; one version_committed; or one unknown or
// superseded. The service enforces these at production and Open, not Check,
// Reader, EncodeCommit or these codecs. Replay reads only record envelopes
// and payloads: no child, Git, parser, timer, body read or append. Actual
// sequence and hash come from the committed envelope read back after
// acknowledgement, never Durable()+1 or a payload.
//
// This part's consumed upstream types are workspace.registered,
// workspace.promotion_admitted and workspace.promotion_accepted v1. The service
// verifies an acceptance's envelope hash, admitted workspace and tree, not
// its F2 lineage. Together with the four graph types this is seven consumed
// types. U3 replay consumes every key; U1.E/ticket 60 consumes effective
// versions, topology and coverage when its gate permits; U5 and U4 follow
// under their later gates; the owner may inspect. Every key is replay-essential.
// Exact paths, object and content digests, references, workspace IDs and
// analysis identities are host-private, never federation exports. No source
// text, secret or body reference appears. A graph record is never delivery,
// validation evidence, permission or proof of coverage beyond its complete
// entries.
//
// The F-U3-61 golden at testdata/u3/61/log/00000000000000000001.jsonl has
// forty-one commits and sixty-two records. analysis_started appears at 11,
// 23, 32, 37, 44, 53 and 56; analysis_progress reserved at 12, 15, 24, 33,
// 38, 45, 54 and 57, checkpoint at 14, 19, 26, 35 and 51, unknown at 55,
// superseded at 62; delta_chunk at 13, 18, 25, 34 and 50;
// version_committed at 20, 27, 36, 39 and 52. It pins a two-chunk load with
// an unrelated commit between chunks, an incremental version for a second
// workspace with 64-digit Git IDs, a same-key content replacement, a zero-net
// rebuild retaining the version, a stale final record after another
// workspace's racing acceptance, an attempt ending unknown, and an attempt
// superseded by its own workspace's newer acceptance.
//
// # F3-10 private-state record types
//
// These four version-1 types are ticket 10's part of F3, frozen
// (D139). C3 owns their payload specifications. Only Core's agent service
// (internal/core/agents) may produce them, through the ledger store's Submit;
// permitted callers are enforced by review. The service checks the record
// rules below both before submission and at Open. Check, Reader, EncodeCommit
// and the payload codecs do not enforce cross-record or filesystem rules.
// Nothing here changes the rules or bytes of an earlier family.
//
// Common decoder rules are F0's strict JSON, required-member, duplicate-key,
// non-null known-member, canonical UUIDv7/digest and plain unsigned-decimal
// rules, including nested change and target objects. Unknown optional members
// are ignored at every object level; duplicate keys are refused even inside
// ignored values. The complete input, including whitespace and extensions,
// must fit MaxPrivatePayloadBytes (16 KiB). Encoders emit compact JSON in the
// tabled order and enforce the same cap. Errors wrap ErrInvalid.
//
// kind, operation and outcome are open tokens matching ^[a-z][a-z0-9_]*$,
// at most 64 ASCII bytes. Version-1 producers write general/work, operations
// agent_register/context_create/private_write, and outcome applied. Decoders
// preserve other well-formed tokens; an unfamiliar outcome is not evidence
// of an applied effect. scope, slot and mode are closed values.
//
// runtime.agent_registered v1 (RuntimeAgentRegistered):
//
//	key                 type    rule
//	directory           string  safe basename, 1 through 128 ASCII bytes
//	general_context_id  UUIDv7  agent's general context
//
// Decoder rules: directory matches [A-Za-z0-9][A-Za-z0-9_-]* and is not CON,
// PRN, AUX, NUL, COM1 through COM9 or LPT1 through LPT9, case-insensitively.
// Record rules: new agent and session IDs; exactly one immutable session per
// agent; globally unique directory spelling under case-insensitive comparison
// on every host. general_context_id equals the envelope context and is created
// in this group. Identities are not inferred from names or regenerated at Open.
//
// runtime.context_created v1 (RuntimeContextCreated):
//
//	key   type   rule
//	kind  token  v1 writes general or work
//
// Record rules: general appears exactly once, during registration; work
// belongs to an existing matching agent/session. A context UUID is new in the
// colony, never reused by another agent. Creation includes its initial revision.
// A work context confers neither a task nor a grant.
//
// private.revision v1 (PrivateRevision):
//
//	key               type             rule
//	scope             string           agent or context
//	expected_version  integer          0 through MaxInt64-1
//	revision          integer          expected_version plus one
//	changes           PrivateChange[]  non-empty, strictly slot-sorted
//
// Each change's keys, in encoder order:
//
//	key              type     rule
//	slot             string   scope's slot
//	mode             string   replace, append or clear as allowed below
//	sha256           digest   required except clear; exact selected bytes
//	bytes            integer  required except clear; 0 through MaxInt64
//	previous_sha256  digest   optional, only on transcript append
//
// Decoder rules: agent slots are instructions and memory (at most two);
// context slots are continuation, handoff, memory and transcript (at most
// four). Slots are strictly ascending UTF-8 bytes, without duplicates.
// Instructions, memory and handoff require replace; transcript requires
// append; continuation requires replace or clear. Transcript and continuation
// replacements are non-empty. Markdown may be empty, represented by its real
// digest and bytes:0, not a zero digest. Clear omits sha256, bytes and
// previous_sha256 altogether, even when a supplied value would be null or zero.
// Non-transcript changes omit previous_sha256. Initialization (expectation 0)
// requires exactly the sorted replacement set {instructions,memory} for agent
// or {handoff,memory} for context; no initial transcript or continuation.
//
// Record rules: one current revision per agent scope and one per context;
// the expectation equals the current scope revision, checked under the lock
// immediately before Submit. Each selected object is durably published in
// its owning private directory before the commit. At most one revision per
// scope/group; registration's two scopes are distinct. Transcript append
// selects one non-empty opaque chunk; previous_sha256 is absent for the
// first chunk and equals the exact previous head thereafter. Identical chunks
// appended twice are two occurrences, distinguished by revision. Hash equality
// grants no cross-context alias. Private object hashes are not body references.
//
// runtime.command_receipt v1 (RuntimeCommandReceipt):
//
//	key                type           rule
//	command_id         UUIDv7         group's command ID
//	submission_sha256  digest         group's canonical submission digest
//	operation          token          operation being acknowledged
//	target             PrivateTarget  keys scope, slots in that order
//	expected_version   integer        original revision expectation
//	outcome            token          v1 writes applied
//	request_id         UUIDv7         optional; v1 producers omit it
//
// Decoder rules: target.scope is agent/context; target.slots is a required
// array (possibly []), strictly sorted and distinct, containing only that
// scope's slots. agent_register requires agent/[] and expectation 0;
// context_create requires context/[] and expectation 0; private_write requires
// non-empty slots and expectation 1 through MaxInt64-1. Unknown operations
// retain the structural rules, with expectation 0 through MaxInt64. Record
// rules: the receipt is last and immutable in its group. command_id and
// submission_sha256 match its single F0 command. A write target lists exactly
// its revision's manifest slots and scope, with the same expectation. Principal
// is the derived owner actor plus the identity envelope, not caller text.
// The returned sequence is the actual receipt envelope's, never a payload
// self-sequence. Receipt and result are one value, not admission and completion.
//
// Group shapes, in order, each in one commit (names omit their namespaces):
//
//	register: command, agent_registered, context_created(general),
//	          revision(agent,1), revision(context,1), command_receipt
//	create:   command, context_created(work), revision(context,1), command_receipt
//	write:    command, revision, command_receipt
//
// Every record in a group, including the F0 command, has the same agent_id,
// execution_session_id, context_id and operation_id; each is required, and
// operation_id equals the command ID. The session is the agent's sole session.
// An agent-scope write carries the originating registered context of that
// agent, not necessarily its general context. Actors are owner for command,
// registration, creation and receipt, and core for revision. turn_id,
// measurement_path and body_refs are absent. Private bytes never enter the
// public ledger or body store.
//
// Services refuse extra, missing or reordered records, wrong actors or
// bindings, reused identity/command IDs, receipt ID/digest/target mismatches,
// illegal initialization, stale revisions or transcript predecessors, and
// overflow. Stale, invalid, busy, publication failure or caller cancellation
// before Submit writes nothing and consumes no command ID. Unreferenced
// published objects have no external effect. There are no private start/end
// records, pending/interrupted outcomes or recovery appends. After commit,
// materialization and projection complete before acknowledgement. Open replays
// complete groups without dispatch or append; it completes and reports stale
// materializations from verified objects. Foreign bytes or missing/corrupt
// objects block the scope rather than being inferred as committed state.
//
// Schema-1 canonical submission is a producer rule: SHA-256 hashes compact
// JSON with fixed keys schema (1), actor ("owner"), agent_id, session_id,
// context_id, operation, target, expected_version, arguments, in that order.
// target has scope then slots, using the receipt rules and sorted slots.
// Registration arguments have directory, agent_sha256, agent_bytes in order;
// creation has kind; private_write has changes, using the change key order
// above but excluding the derived transcript predecessor. Hash every exact
// supplied byte through its digest and size. All supplied binding IDs and
// expectations enter this form; the command ID itself, ephemeral handles,
// paths/view spellings, ledger metadata and derived predecessor do not.
// Same command ID and digest replays its receipt; a different digest refuses
// before revision/CAS checks. Schema-1 bytes remain stable when future producers
// are added; a new meaning cannot reuse an old command ID.
//
// The F3-10 golden at testdata/f3/10/log/00000000000000000001.jsonl has ten
// commits and thirty-four records: registration 1-6; work creation 7-10;
// general memory replace 11-13; first and second work transcript chunks 14-16
// and 17-19; continuation replace 20-22 and clear 23-25; agent memory from
// the work context 26-28; work handoff/memory/transcript 29-31; agent
// instructions from the general context 32-34. Replays and refusals add no
// record. IDs, times, schema-1 digests and content digests are deterministic;
// EncodeCommit supplies the actual record hash chain.
//
// # Scoped authority, tasks and non-paid work (F3-11)
//
// F3-11 is frozen (D140). It adds ten version-1 types to
// ledger-format version 1; Defined contains thirty-eight types. Only the
// internal/core/agents producer writes this family, through Submit. Authorization
// and resource metadata, task/assignment metadata, work/usage metadata and
// private revision metadata are all access-filtered. No payload contains private
// text, credentials or a capability handle, and no unfiltered agent ledger
// stream is introduced. BindAgent is a trusted-host origin binding, not
// authentication; authority is re-derived from the ledger on each operation.
//
// F3-10's text, bytes, actors, group shapes, codecs and golden are unchanged.
// private.scoped_revision advances the SAME scope revision and transcript
// predecessor chain as private.revision. A consumer reconstructing private heads
// consumes both types in ledger order. A consumer of F3-10 alone cannot
// reconstruct the heads of a log holding F3-11 and MUST NOT be used on one: it
// could retain an old head or reject a subsequent owner revision as stale.
// F3-11 groups never embed F3-10 records. Services dispatch the two families
// separately before applying their common private-change reducer.
//
// # Common codec rules
//
// The complete input and compact encoded output, including whitespace and
// extensions, each fit MaxScopedPayloadBytes (65536 bytes). Check the input
// bound before parsing. F0's strict complete JSON, recursive duplicate-key
// rejection, canonical UUIDv7s, nonzero lowercase SHA-256 digests, and unknown
// optional-member rules apply. A known member may never be null. Unknown
// optional members are ignored, but duplicate keys inside them still refuse.
// Integers are plain unsigned JSON decimals in 0..MaxInt64; checked increments
// cannot overflow. Errors wrap ErrInvalid.
//
// Numeric runtime ceilings (slots, delegation depth, list counts, selected
// objects, description/body bytes and max_bytes) are producer Limits guards
// before Submit, NOT decoder ceilings. Historical larger values remain valid
// within the complete payload cap. Nonempty, sorted/distinct and kind-conditional
// structure, scopes >=4, units=1 and checked version increments are codec rules.
// Capacity's five amounts are independent and nonnegative, including
// max_operation_bytes; it is not coupled to local_slots.
//
// All following lists specify compact encoder key order; ? means optional,
// not null. Nested objects:
//
//	Binding         agent_id, context_id
//	Origin          agent_id, session_id, context_id
//	Assignment      assignment_id, generation, assignee, assigner_actor, assigner
//	TaskBinding     task_id, assignment_id, generation
//	ObjectRef       sha256, bytes
//	ArtifactRef     source_sequence, sha256, bytes
//	Capacity        turn_slots, local_slots, buffer_bytes, local_units,
//	                max_operation_bytes
//	ScopeKey        kind, id?
//	PrivateReadRef  scope, slot, revision, cursor, max_bytes
//	SelectedObject  sha256, bytes, revision, sequence
//
// Binding/Origin IDs are required. The service verifies registered pairs and
// the agent's sole session. Assignment generation and TaskBinding generation
// are positive; assigner_actor is owner or agent, assignee is Binding and
// assigner is Origin. An absent assignment is generation zero, never a partial
// object. A TaskBinding is wholly present or absent. ArtifactRef's
// source_sequence is positive; the service verifies that source's body_refs
// actually lists the digest. ObjectRef bytes and ArtifactRef bytes are
// nonnegative. A description's source is its task-change envelope.
//
// ScopeKey kind is colony, agent, context or grant, in that sorting order,
// then UUID bytes; colony forbids id, the others require it. Lists are strictly
// sorted, hence distinct. PrivateReadRef requires a valid scope/slot, positive
// revision and max_bytes, and nonnegative cursor (zero for nontranscript slots).
// SelectedObject is an exact head/chunk occurrence with nonnegative bytes and
// positive revision/sequence. Its revision cannot exceed the requested revision.
// Transcript occurrences are in increasing revision/sequence order after cursor.
//
// Resource has a closed kind, followed only by that kind's known members:
//
//	context    kind, binding, scope, slots
//	task       kind, task_id?
//	workspace  kind, workspace_id, binding, path_prefix
//	artifact   kind, artifact
//	external   kind, resource_key
//
// Context scope is agent/context. Sorted slots are instructions/memory for
// agent and continuation/handoff/memory/transcript for context. Empty slots
// are valid for context inspection, turn controls and delegation, never private
// read/write. A grant task_id is absent only for task.create plus delegation
// controls; an absent task ID contains a present ID, and a child may add one.
// Creation is bounded by the grant's recipients and finite units, not a fresh
// per-task consent grant. Other known kinds' members are forbidden, even null.
//
// Resource paths are 0..1024 UTF-8 bytes: slash-separated relative components,
// with no empty internal component, dot/dotdot, backslash, colon, control
// character, absolute/drive/UNC form or ASCII-case-insensitive .git component.
// Empty means the root prefix. No clean-then-accept coercion is permitted.
// ValidResourcePath is lexical and host-independent, NOT symlink containment or
// a file executor. Concrete targets equal or descend component-wise from a
// permitted prefix. External keys are opaque, nonsecret 1..256-byte UTF-8 strings
// without controls; there is no URL inference.
//
// Resources sort strictly by compact encoded bytes, operations by ASCII bytes,
// bindings by (agent UUID, context UUID) bytes and ID lists by UUID bytes.
// Context resources have private data class; the other classes equal their
// resource kind. Operations per class are closed:
//
//	private    context.inspect, private.read, private.write, turn.start,
//	           turn.inspect, turn.cancel
//	task       task.inspect/create/claim/assign/start/wait/resume/submit/
//	           complete/fail/cancel/reassign (each with the task. prefix)
//	workspace  file.read, file.search, file.write, file.edit
//	artifact   artifact.read
//	external   shell.execute, process.spawn, network.connect, connector.call,
//	           model.call
//
// grant.issue and grant.revoke may accompany any class. An external grant has
// at most one effect operation plus those controls; there is no implicit
// external authority in a local grant. External producers are not present here.
//
// SubjectRef is kind followed by that kind's members. Kind is an OPEN token;
// this producer writes artifact, followed by artifact (ArtifactRef). For artifact
// the decoder checks those members normally. For an unfamiliar kind it checks
// only the token and generic well-formedness (including duplicates and cap),
// preserving the members and their order/values. It never interprets unfamiliar
// members as an ArtifactRef. The service refuses to apply an unfamiliar kind;
// ticket 12 can add kinds by addition. SubjectRef.Members exposes those unknown
// members as an object excluding kind. Compact re-encoding retains their order.
//
// Lifetime, outcome and failure-cause tokens are also open:
// ^[a-z][a-z0-9_]*$, at most 64 ASCII bytes. Version 1 writes lifetime
// until_revoked; ANY other lifetime grants nothing in this service. There is no
// timer or implicit lease. Operation tokens have the same components separated
// by dots, at most 64 bytes; only the documented operation meanings apply.
//
// Payloads (version 1)
//
//	runtime.grant_issued:
//	  grant_id, expected_authority_version, authority_version, grantee,
//	  parent_grant_id?, operations, resources, data_class, recipients, limits,
//	  delegation_depth, lifetime, task_binding?
//	runtime.grant_revoked:
//	  grant_id, expected_authority_version, authority_version
//	runtime.task_changed:
//	  task_id, action, expected_version, version, creator_actor, creator,
//	  description, acceptance_sha256, dependencies, audience, assignment?,
//	  state, wait_reason?, wait_artifact?, submission?, failure_cause?,
//	  failure_evidence?
//	runtime.capacity_configured:
//	  scope, expected_version, version, limits
//	runtime.work_admitted:
//	  work_id, kind, expected_version, version, authority_version, grant_id,
//	  scopes, operation, target, task_binding?, parent_turn_id?,
//	  arguments_sha256?, tool_schema?, max_bytes?
//	runtime.work_dispatched:
//	  work_id, expected_version, version, authority_version, grant_id,
//	  arguments_sha256
//	runtime.work_ended:
//	  work_id, expected_version, version, outcome
//	runtime.local_used:
//	  authority_version, grant_id, scopes, units, bytes, read?, artifact?,
//	  selected?, next_cursor?
//	private.scoped_revision:
//	  scope, expected_version, revision, changes
//	runtime.scoped_receipt:
//	  command_id, submission_sha256, submission, outcome, version, work_id?
//
// Grant authority_version is expected+1. Operations, resources and recipients
// are nonempty, sorted sets; delegation_depth is nonnegative. Owner roots omit
// parent; agents require a current parent and attenuation: one leaf and its
// entire chain authorize an operation, never a union. Explicit delegation
// requires grant.issue, a permitted recipient, subset regions/operations/
// recipients, no larger capacities and decreasing depth. Parent ceilings are
// shared. Revocation is irreversible, makes descendants unusable by ancestry,
// and releases no holds or counters. No mutable grant updates exist.
//
// Task version is expected+1. creator_actor is owner/agent; creator, description,
// acceptance hash, dependencies and audience are immutable. Dependencies is a
// required array (possibly []), distinct and not self; audience is a nonempty
// sorted set of AGENT IDs, not bindings. The service checks existing dependencies
// and prevents new-ID cycles. Assignment names a never-task-bound work context
// of an audience agent; reassignment imports no context bytes, scratch or grants.
// Action/state are closed. create has expectation 0 and state waiting, optional
// generation-1 assignment; claim/assign produce assigned waiting at generation 1.
// start produces active; wait produces waiting with reason dependency/input/
// review/explicit_hold and optional SubjectRef wait_artifact; resume produces
// waiting with reason cleared. submit produces submitted with SubjectRef
// submission; complete produces completed with that same exact submission.
// fail produces failed with an open failure_cause and SubjectRef failure_evidence;
// cancel produces cancelled. reassign produces waiting with new assignment ID,
// next generation and all wait/submission/failure fields cleared. Wait fields
// occur only while waiting, submission only submitted/completed, failure fields
// only failed. Post-assignment mutations compare prior assignment expectations.
// Terminal tasks never reopen. Completion/failure are authorized decisions, not
// review, promotion or evidence that future ticket-12 guards passed.
//
// Capacity scope permits colony/agent/context only and version is expected+1.
// Owner configuration changes future ceilings without resetting used/held
// exposure, including a reduction below usage. Colony/agent enrollment is
// required by service admission; context enrollment is optional (absence means
// no extra context ceiling), though context is always in the scope set.
//
// Admission has expected_version=0/version=1 and a closed kind turn/local.
// Turn operation is turn.start, with a context target whose scope is context; all four local-only fields
// are absent. Local operations are file.read/search/write/edit with workspace
// target; parent_turn_id, arguments_sha256, tool_schema=1 and positive max_bytes
// are required. Scope sets have at least four sorted distinct entries and are
// recomputed by the service, not trusted as caller authority. Turn admission is
// agent-only and executes nothing. Local dispatch has expected=1/version=2,
// consumes the held local unit once and retains local/buffer holds.
//
// Work ending requires positive expected and version=expected+1. Turn outcomes
// released/cancelled/interrupted require every child to have an ending record.
// Local released_unsent is only before dispatch; succeeded/failed/interrupted
// after dispatch require trusted Core completion observation. unknown IS an
// ending record matching a start but releases NOTHING; later proven ending can
// advance it. Every unfamiliar well-formed outcome is likewise held. Never infer
// success/release from an unknown token. A repeated identical current unknown
// under a new command ID refuses no_effect. Honest Core endings use original
// work identity, not a requirement that a revoked grant remain current.
//
// Local usage has units=1 and nonnegative bytes. Private read requires read and
// selected, with checked sum(selected.bytes)=bytes <= read.max_bytes. Empty
// selected is valid only for absent continuation or transcript end/empty page.
// Nontranscript present heads select one object; transcript chunks are whole
// ordered occurrences. next_cursor occurs only when more chunks remain, equals
// the last selected sequence and advances cursor; an empty advancing page is
// invalid. Artifact read requires artifact, exact returned bytes and no other
// read fields. Other atomic operations omit all read fields. The service checks
// selected identities against the exact historical revision, never current heads.
// Private writes charge supplied bytes, task creation description bytes, other
// task/delegation operations zero. Owner administration/revocation/release and
// passive metadata have no local_used. Artifact disclosure verifies full hash/
// size and exact source body_refs, current authority and the store's real read
// allocation; missing/corrupt/collected do not become empty successful artifacts.
//
// PrivateScopedRevision reuses PrivateRevision's exact manifest/change rules
// and compact change key order, but expectation is >=1 (no initialization).
// Its scope head and transcript chain are shared as specified above.
// Receipt is last, immutable, and embeds the canonical submission below.
// Its digest is checked by re-encoding that submission. Receipt outcome is
// applied for atomic/control/task/private/read, admitted for admission,
// dispatched for dispatch, recorded for ending; unfamiliar well-formed outcomes
// survive codecs but the service must not assume applied effects. Version is
// the resulting authority/task/config/work/private version, or zero for reads.
// work_id is present exactly on work admission/dispatch/ending receipts. The
// receipt's own actual envelope sequence is not a payload field.
//
// Record and group rules (service-enforced)
//
// One Submit commit has one of these complete ordered shapes. C=F0 command,
// R=scoped_receipt, U=local_used, T=task_changed:
//
//	owner issue/revoke/configure   C, effect, R
//	agent issue                   C, grant_issued, U, R
//	agent revoke                  C, grant_revoked, R
//	owner task create             C, body_pin, T, R
//	agent task create             C, body_pin, T, U, R
//	owner nonterminal mutation    C, T, R
//	agent nonterminal mutation    C, T, U, R
//	owner terminal mutation       C, T, body_unpin, R
//	agent terminal mutation       C, T, body_unpin, U, R
//	work admission/dispatch/end   C, effect, R
//	agent private write           C, private.scoped_revision, U, R
//	agent private/artifact read   C, U, R
//
// No unrelated, extra, detached, missing or reordered record is accepted by
// producer preflight or Open. Each group has identical originating agent,
// session, context and operation_id=CommandID. Turn ID is absent outside work;
// work groups use their immutable original bound turn/origin, including Core
// endings. Command/receipt and grant/task/config actors are the derived
// owner/agent/core principal; work/private-revision/U/body-pin/unpin actors are
// core. No measurement_path. All body_refs are absent except creation body_pin
// and EVERY nonterminal task snapshot's description. Pin ID is task ID. Terminal
// snapshots retain description digest/size but omit body_refs and unpin, so
// historical replay does not require a collected description. SubjectRefs make
// no new retention promises and are not copied to body_refs. Private object
// hashes are not body-store refs. Sources and receipt/effect sequences are actual
// readback identities, never predicted before Submit.
//
// authority_version on U/admission/dispatch is the projection observed BEFORE
// the transition. Agent issuance U carries old, grant carries old+1, receipt
// returns old+1; apply all group changes atomically. Services reconstruct and
// compare canonical requests, group identities, prior versions/assignments,
// authority chain, scope sets, allowances, charges and resulting state both
// before Submit and during Open. Ledgerstore remains unchanged: it owns F0
// identities/pins/catalogue/body availability; Check, Reader and EncodeCommit
// do not enforce these service rules. A fixture dispatch or failure is not
// evidence of an executed file tool; there is no production executor here.
//
// Schema-2 canonical submission (producer rule)
//
// SHA-256 covers compact JSON in this exact key order:
//
//	schema (2), actor, origin, operation, target, grant_id?, expected, arguments
//	expected: version, assignment_id?, generation?, authority_version?
//
// Actor is owner/agent/core. grant_id is required for agent operations AND all
// work operations regardless of actor, including Core dispatch/end. Assignment
// ID/generation are both present or absent and only on task mutations; generation
// is positive. authority_version is present exactly for grant issue/revoke.
// Expected version is 0 for issue/task.create/turn.start/local.admit/reads, 1 for
// revoke/local.begin, nonnegative current configuration version for configure,
// positive current entity version otherwise; every advancing value is below
// MaxInt64. Issue/revoke expected authority is independent of entity version.
//
// The closed operations and ordered arguments are:
//
//	grant.issue: grant_id, grantee, parent_grant_id?, operations, resources,
//	  data_class, recipients, limits, delegation_depth, lifetime, task_binding?
//	grant.revoke: grant_id
//	capacity.configure: scope, limits
//	task.create: task_id, description, acceptance_sha256, dependencies,
//	  audience, assignment? {assignment_id, assignee}
//	task.claim/assign/start/wait/resume/submit/complete/fail/cancel/reassign:
//	  task_id, action, new_assignment? {assignment_id, assignee},
//	  wait_reason?, wait_artifact?, submission?, failure_cause?, failure_evidence?
//	turn.start: turn_id, task_binding?
//	local.admit: work_id, parent_turn_id, local_operation, arguments_sha256,
//	  tool_schema, max_bytes, task_binding?
//	local.begin: work_id, arguments_sha256
//	local.end / turn.end / turn.cancel: work_id, outcome
//	private.write: changes (F3-10 canonical change order, no previous_sha256)
//	private.read: read
//	artifact.read: artifact, max_bytes (at least artifact.bytes)
//
// New assignment input occurs exactly on claim/assign/reassign, optionally
// create; generation and assigner derive. Mutation conditional fields match the
// action above. Every supplied target/binding, expectation, argument/ref hash,
// byte size and limit is bound. Command ID, receipt sequence, observed authority,
// recomputed scope set, transcript predecessor and prepared handle are excluded.
// No private input bytes enter the submission. Control target is the changed
// grant's first canonical Resource (not extra authority), configured context or
// origin context for colony/agent config, exact task, original admitted-work
// Resource, or current private/artifact Resource. local_operation binds the
// actual file effect; local.admit alone does not authorize it. Dispatch/end select
// immutable original work rather than accepting a retargeted origin/turn.
// Same ID/new spelling cannot change meaning, and schema 1 stays byte-identical.
//
// The F3-11 golden at testdata/f3/11/log/00000000000000000001.jsonl contains
// thirty-nine commits and 130 records. Its first four commits are unchanged
// F3-10 identity shapes for G/W1/W2/W3, followed by complete F2 registration and
// dedicated scratch shapes. The scoped history includes every group shape,
// owner assignment (not claim) of A, A's private/read/admission/unknown/proven
// ending history and terminal unpin, then agent creation/reassignment/cancellation
// of B under two explicit owner roots. The unnamed creation root precedes B;
// B's exact mutation root follows its creation. Final colony capacity is reduced
// below consumed usage without a reset. Hash-named objects and bodies alongside
// the log contain all private/task/capture-input fixture bytes. TestGoldenF3_11
// generates every file with -update, with fixed UUIDv7s/times, real content and
// submission hashes, and EncodeCommit's actual chain. It claims no execution.
//
// # Review and failure decisions (F3-12)
//
// F3-12 is frozen (D144). It adds three version-1 types:
// runtime.review_requested, runtime.review_result and runtime.review_receipt.
// Defined contains forty-one types. Only internal/core/agents produces this
// family through Submit. Its access-filtered metadata identifies tasks,
// assignments, principals, authority and immutable subjects, never private
// content, credentials, capability handles or an unfiltered agent stream.
//
// An approve or fail review_result is a task transition that advances the
// task's version and state exactly as a task_changed complete/fail would,
// without a task_changed record; a consumer of tasks consumes both families in
// ledger order; a consumer of F3-11 alone cannot reconstruct the tasks of a log
// that holds F3-12 and must not be used on one: it keeps the task submitted and
// live and could accept a direct transition the result already closed.
// F3-11's text, bytes and golden are unchanged, and F3-12 groups embed no
// F3-11 record. Services dispatch F3-12 before the F3-10/F3-11 validators.
//
// # Codec rules and nested values
//
// Complete input (including whitespace and ignored extensions) and compact
// output must each fit MaxReviewPayloadBytes, 65536 bytes. The input bound is
// checked before parsing. Strict complete UTF-8 JSON, recursive duplicate-key
// rejection even inside ignored extensions, canonical lowercase UUIDv7s and
// nonzero lowercase SHA-256 digests apply. Required members cannot be absent;
// every known member forbids null. Unknown optional members are ignored, except
// that unfamiliar SubjectRef members are preserved as below. Integers use
// plain unsigned JSON decimals in 0..MaxInt64, never floats, exponent notation,
// strings or negative numbers. Errors wrap ErrInvalid.
//
// The payload cap and structure are frozen (D144); producer Limits
// ceilings are not. Required evidence has no decoder count ceiling beyond the
// payload cap; MaxReviewEvidence (default 16), object-byte and preparation
// ceilings constrain producers only. Historical larger values remain readable.
// The following lists give compact encoder key order; ? means absent or present,
// never null:
//
//	ReviewPrincipal: actor, origin, grant_id?
//	ReviewBinding: task_id, task_version, assignment_id, generation, assignee,
//	  reviewer, subject, acceptance_sha256, check_sha256, config_sha256,
//	  required_evidence, authorization_revision, deadline_utc_ns
//	PrivateRevisionRef: binding, scope, slot, revision, source_sequence,
//	  sha256, bytes
//	ReviewUsage: authority_version, grant_id, scopes, units, bytes
//
// Reviewer actor is the closed set owner/agent; origin is Origin, and grant_id
// is required exactly for agent. Requester and submission principals have the
// same rule. Assignee is Binding (agent_id, context_id), NOT Origin: assignment
// has no session. The service checks every registered binding and sole session.
// Binding task_version is positive and below MaxInt64; generation is positive,
// authorization_revision nonnegative, deadline_utc_ns positive. The three
// digests are required and nonzero. acceptance_sha256 equals the task's immutable
// acceptance digest. check_sha256 and config_sha256 identify the checklist and
// configuration the reviewer judged against; the service verifies neither, and
// no record here asserts a check ran.
//
// SubjectRef additionally recognizes kind private_revision, encoded as kind,
// private_revision (PrivateRevisionRef). Its binding is required; scope is the
// closed agent/context set. Agent slots are instructions/memory; context slots
// are continuation/handoff/memory/transcript. Revision and source_sequence are
// positive and bytes nonnegative. The value identifies one changed object or
// transcript chunk in a validated private.revision/private.scoped_revision:
// not a clear, absent object, mutable head or concatenated transcript. The
// service proves the exact binding/scope/slot/revision/sequence/digest/size
// occurrence; an equal hash elsewhere or a later head is not a substitute.
// PrivateRevisionRef is a comparable value. SubjectRef uses its typed pointer
// only for this kind and forbids Artifact/Members there. Artifact's historical
// encoding and ignored-extension behavior remain unchanged.
//
// SubjectRef.kind remains an open token. Unfamiliar Members retains object
// member order, values and number spelling without map sorting or float
// conversion; Members excludes kind and cannot accompany typed values.
// Direct MarshalJSON canonicalizes it with json.Marshal(json.RawMessage(raw)),
// so outer and direct encodings agree on <, >, &, U+2028 and U+2029 escaping.
// This correction rewrites no historical stored bytes or hashes. Services
// refuse unsupported kinds rather than interpreting them as artifacts.
//
// RequiredEvidence is nonempty and strictly ascending by each SubjectRef's
// compact encoded UTF-8 bytes. Duplicates and unsorted lists refuse; codecs
// never sort or deduplicate them. Source sequence, and private revision, are
// part of identity. Failure evidence must equal one of these exact references.
// Failure cause and receipt outcome are open tokens: ^[a-z][a-z0-9_]*$, at most
// 64 ASCII bytes. Unknown outcomes never imply applied state.
//
// Payloads (version 1)
//
//	runtime.review_requested:
//	  request_id, kind, version, requester_actor, requester,
//	  requester_grant_id?, binding, replaces_request_id?, failure_cause?,
//	  failing_evidence?, observed_utc_ns
//	runtime.review_result:
//	  request_id, request_sequence, expected_version, version, binding,
//	  verdict, cause?, failing_evidence?, task_version, observed_utc_ns
//	runtime.review_receipt:
//	  command_id, submission_sha256, submission, outcome, version,
//	  request_id, task_version, usage?
//
// Request kind is closed review/failure and version is exactly 1. Replacement
// cannot equal request_id; the service proves it names the current predecessor
// and that the new request ID has never been used. failure_cause and
// failing_evidence occur exactly when kind=failure.
// Result request_sequence is positive, expected_version=1 and version=2.
// Verdict is closed approve/reject/fail; cause and failing_evidence occur
// exactly when fail. Task_version equals binding.task_version+1 for approve/fail
// (checked arithmetic), and equals binding.task_version for reject.
// Observation and deadline codecs check positivity only, not their ordering.
// Receipt version is 1 or 2 and task_version positive; its submission digest
// equals SHA-256 of the decoded/re-encoded canonical submission. Usage occurs
// exactly for agent, with nonnegative authority_version, the submission's
// selected grant_id, sorted ScopeKeys (at least four), units=1 and bytes=0.
// The service recomputes the exact scopes and pre-operation authority version.
//
// Record and group rules (service-enforced)
//
// One Submit has one complete ordered shape, with C=F0 command, Q=request,
// V=result, U=F0 body_unpin(task ID), R=review receipt:
//
//	review.request / failure.request (including replacement): C,Q,R
//	review.resolve reject:                                  C,V,R
//	review.resolve approve / failure.resolve fail:            C,V,U,R
//
// Mixed families, detached effects, extra/missing/reordered records refuse both
// before publication and at Open. Every record has the same operation_id
// (command ID), agent/session/context of the actual principal, no turn_id or
// measurement_path, and absent body_refs. C/Q/V/R use the actual owner/agent
// actor; U uses core. Terminal results release only the existing task-description
// pin, exactly once. References add no pin or retention promise; collected bytes
// remain unavailable. Nothing here changes a work hold, turn or provider budget.
//
// Binding repeats the request's exact values, never a current substitute.
// Request_sequence names that committed request, and kind matches verdict:
// review.resolve writes approve/reject; failure.resolve writes fail with the
// request's exact cause/evidence. Receipt command/digest, request/task version
// and outcome agree with the effect. Producers write requested/approved/rejected/
// failed. Actual effect/receipt sequences are envelope data, never predicted
// payload self-sequences. A semantic judgment is an authorized reviewer's
// decision, not host evidence of a check or tool execution.
//
// Requests and callbacks require task.complete authority for review and
// task.fail for failure. An agent failure requester is the current assignee.
// The reviewer is a permitted recipient, and both participants need current
// read rights on the subject and every evidence occurrence. Private read rights
// are one current private.read chain per participant, never a cross-context
// share. Authorization_revision is the colony authority_version at request
// validation, bound and repeated; replay checks it against the prefix. A
// callback rechecks the exact selected reviewer chain and ancestors, the
// requester's recorded chain, assignment/generation and all read rights, but
// does not require the colony authority_version to remain unchanged.
//
// A request does not advance task version. One current slot guards a nonterminal
// task/version/assignment: pending, rejected and expired slots refuse direct
// complete/fail, owner included. Replacement names the current slot; cancellation,
// reassignment or re-versioning invalidates it. There is no timer or implicit
// retry. Approve/fail enter the task transition kernel atomically with the unpin
// and charge. Reject retains the guard. Each agent decision, including reject,
// charges one local unit in the existing colony/agent/optional-context/grant-
// and-ancestors book; owner administration and matched retries charge nothing.
//
// Deadline measures the final decision-eligibility observation immediately
// before Submit, not arrival or flush completion. New requests require
// observed_utc_ns < deadline; results allow equality. Recorded observations
// cannot precede the prior committed floor (all envelope event_time_ns and
// review observations). Live eligibility also respects the incarnation's
// highest sample, including refused operations. Replay, queries and matched
// retries read no clock; replay does not require observation <= this commit's
// later envelope time. A delayed flush cannot undo an admitted durable result.
//
// Schema-3 canonical submission (producer rule)
//
// SHA-256 covers compact JSON in order: schema (3), actor, origin, operation,
// grant_id?, arguments. The actor/origin derive from the authenticated entrance;
// an owner omits grant_id. Schema 2's closed operation table is unchanged.
// Schema 3's closed operations and ordered argument objects are:
//
//	review.request / failure.request:
//	  request_id, binding, replaces_request_id?, failure_cause?, failing_evidence?
//	review.resolve: request_id, request_sequence, binding, verdict
//	failure.resolve: request_id, request_sequence, binding, cause, failing_evidence
//
// Conditional failure fields follow the request/result rules above. Known
// argument members of another operation are forbidden, not authority-bearing
// extensions. Concrete ReviewRequestArguments, ReviewResolveArguments and
// FailureResolveArguments supply arguments; arbitrary maps/pointers do not.
// The binding supplies task/assignment/authority/deadline expectations once.
// There is no redundant task target, requester origin or request_sha256.
// Command ID, host observation, derived request/result/task versions, usage
// scopes/charges and envelope sequences are excluded; the already-observed
// request_sequence is included. Producer APIs sort caller evidence sets before
// fingerprinting. Unknown optional extensions confer no authority.
//
// The F3-12 golden at testdata/f3/12/log/00000000000000000001.jsonl contains
// twenty-five commits and ninety records: two registrations, three work contexts,
// three capacities, three assigned tasks, four owner roots, start/submit of A,
// agent request/reject, owner replacement/approve, owner private-memory failure
// request/fail and agent artifact failure request/fail. Hash-named objects and
// bodies contain the actual initial private bytes and task descriptions; no
// checklist/config surrogate tasks exist. Fixed UUIDv7s/times, actual encoded
// submissions and EncodeCommit generate every fixture only with
// TestGoldenF3_12 -update. The history records judgments, not executed checks.
//
// # F3-13: paid attempts and accounting (frozen (D148))
//
// C3 owns eight additional version-1 payloads: runtime.paid_route_configured,
// runtime.credential_changed, runtime.spend_configured, runtime.paid_reserved,
// runtime.paid_dispatch_intent, runtime.paid_result,
// runtime.paid_overrun_acknowledged and runtime.paid_receipt. Only Core's agents
// service produces them through ledgerstore.Submit. Their metadata and private
// object references are access-filtered; no authentication bytes, secret-derived
// digest, secret size, private text, body_refs or measurement_path occur.
//
// The additive shared-work and shared-book reading is:
//
// (a) a runtime.paid_reserved whose parent_turn_id names a turn is a child of
// that turn; F3-11's rule that a turn's released/cancelled/interrupted ending
// requires an ending record for every child counts paid children; a
// runtime.paid_result of any kind is that child's ending, unknown and
// released_unsent included, and unknown retains its money;
//
// (b) a runtime.paid_receipt carrying usage charges one local unit on the named
// existing F3-11 scopes, as runtime.review_receipt's usage does;
//
// (c) a consumer of F3-11 alone cannot reconstruct the turns, child endings or
// non-paid book of a log that holds F3-13 and must not be used on one: it would
// accept a turn ending while a paid child is unresolved and undercount consumed
// local units;
//
// (d) F3-13's admission is the first consumer of an external grant's model.call;
// F3-11's unavailable/producer_absent refusal stays for the other external
// operations;
//
// (e) F3-11's and F3-12's text, bytes, receipts, lookups, group shapes and goldens
// are unchanged, and F3-13 groups embed no F3-11 or F3-12 record.
//
// # Decoder rules and nested values
//
// Complete UTF-8 JSON, recursive duplicate-key rejection (even in ignored
// extensions), required keys, no null for known keys, unknown optional keys
// ignored, canonical UUIDv7s and nonzero lowercase 64-hex digests follow F0.
// Every complete payload or schema-4 submission, including whitespace and
// extensions, is at most 65,536 bytes. Encoder output has the same cap.
// Producer collection, stream and installation limits are not decoder limits.
// Money and counts are plain nonnegative JSON integers through MaxInt64, never
// floats, exponents or clamped values. Exact rate strings use 0|[1-9][0-9]*,
// at most MaxInt64; denominators are positive. Versions satisfy
// 0 <= expected_version < MaxInt64 and version = expected_version + 1.
//
// Result kind, credential action, spend mode, period kind, scope kind, profile
// schema and schema-4 operation are closed. Provider, outcome, error_class,
// source_kind and no_charge_rules members use the open token grammar
// ^[a-z][a-z0-9_]*$ (at most 64 bytes). Classes use dot-separated such tokens,
// at most 64 bytes overall. Unfamiliar tokens survive codecs, but confer no
// permission to dispatch or release money. In particular an unfamiliar
// no-charge rule is held as unknown, never released and never a refusal to Open.
// Ordered sets are nonnull, sorted bytewise and distinct. Object lists preserve
// order, are nonempty when present, and cannot repeat a digest.
//
// The following lists give exact compact key order; ? means absent or present,
// never null:
//
//	PaidScope: kind, id?
//	  Closed colony/agent/credential/task/grant. Colony omits id exactly;
//	  all others require UUIDv7. Scope sets sort by that kind order, then ID.
//	SpendTerms: enabled, mode, ceiling_micros?, period_kind, warning_ppm?
//	  finite requires ceiling >= 0; unlimited forbids ceiling and warning.
//	  warning is 1..1,000,000. Period is lifetime/utc_day/utc_month.
//	  Disabled terms retain the chosen mode, ceiling and period.
//	CredentialRef: scope_id, version
//	  Both UUIDv7. Version identifies immutable bytes, never a secret hash.
//	SemanticHeader: name, value
//	  Names are lowercase HTTP tokens, sorted and distinct; values are UTF-8
//	  without controls. Authentication and transport-framing headers are absent.
//	SealedDescriptor: provider, authority, model, region, method, path,
//	  requested_tier, credential, headers, body_sha256, body_bytes
//	  HTTPS/POST endpoint without userinfo/query/fragment/opaque/escaped
//	  ambiguity; authority <= 255 bytes, path <= 2,048, body_bytes > 0.
//	  Model/API/usage-schema/version identifiers are visible ASCII 1..128;
//	  region/tier/speed/source-topic use open tokens <= 64.
//	PricedDimension: classes, max_units, rate_numerator, rate_denominator
//	  Nonempty sorted distinct classes, max_units >= 0, exact rates.
//	CostBound: currency, maximum_micros, components, request_sha256,
//	  capability_version, tariff_version, tariff_sha256, quote_sha256
//	  USD, finite maximum >= 0, nonempty components with no overlapping class.
//	PaidUsage: classes, pricing_tier, served_tier?
//	  classes is sorted distinct {class, units}; units >= 0. Supported
//	  Anthropic usage includes all five exclusive classes exactly once:
//	  input.cache_read, input.cache_write_1h, input.cache_write_5m,
//	  input.uncached, output.generated. Reasoning is inside output once.
//	  OpenAI usage includes all eight classes exactly once:
//	  input.cached.long, input.cached.short, input.uncached.long,
//	  input.uncached.short, input.write.long, input.write.short,
//	  output.generated.long, output.generated.short. Inclusive input
//	  splits into cached, write and uncached; reasoning remains inside output.
//	  At most one band is nonzero. pricing_tier is default_short at input
//	  <=272000 or default_long at input >=278529; served_tier is default.
//	  Input 272001..278528 is unpriceable, not a third pricing tier.
//	NoChargeEvidence: rule, stop_reason, stop_details_type, category_kind,
//	  category?, complete, content_blocks, output_started, output_tokens?
//	  stop fields are refusal; complete true, content_blocks 0,
//	  output_started false, present output_tokens 0. category_kind is
//	  null/value: null forbids category, value requires a token other than
//	  bio/frontier_llm/reasoning_extraction. Rule is in the admission
//	  profile's no_charge_rules; the version-1 writer value is
//	  anthropic_refusal_before_output_2026_09_24.
//	Private objects reuse ObjectRef: sha256, bytes
//	  Evidence, sealed input and message objects have positive byte counts.
//	PaidLocalUsage: authority_version, grant_id, scopes, units, bytes
//	  Existing F3-11 ScopeKeys; units 1, bytes 0; delegation only.
//
// # Profile and digest encodings
//
// Profile's exact known-field order is schema (1), capability_version,
// tariff_version, provider, authority, method, path, api_version, model_id,
// region, requested_tier, allowed_served_tiers, speed, context_max, input_max,
// output_max, usage_schema, cache_classes, tool_choices, no_charge_rules, tariff,
// verified_utc_ns, expires_utc_ns, sources. api_version is always present.
// Tariff is {currency,rates}; each sorted rate is {class,numerator,denominator}.
// Each source is {topic,url,retrieved_utc_ns,quote}, sorted by (topic,url).
// URLs are HTTPS, <= 2,048 bytes, with no userinfo or controls; quotes are
// nonempty valid UTF-8 <= 4,096 bytes; 0 < retrieved <= verified < expires.
// Context/input/output maxima are positive int64. No quote self-digest or
// tool_prompt_tokens exists. Complete source verification, including the
// no_charge_billing topic, supported models/tiers/classes and the 32 KiB
// installation cap are producer rules, not narrower history caps.
//
// Profile SHA-256 covers compact JSON of these known fields in this exact order.
// Tariff SHA-256 covers compact {currency,rates} likewise. Encoding is Go
// encoding/json.Marshal's: <, >, &, U+2028 and U+2029 become \u003c, \u003e,
// \u0026, \u2028 and \u2029. Direct profile encoding and its nested route bytes
// agree. The profile occurs once, in the route record; the route submission
// binds profile_sha256 and later records carry digests/versions.
//
// Producer request hashing uses frame(b) = u64be(len(b)) || b and unnormalized
// UTF-8: frame each of router-provider-request/v1, provider, HTTPS authority,
// model, region, POST, escaped path, requested tier, credential scope ID and
// credential version; u32be(headerCount); frame each sorted header name/value;
// frame exact body bytes. Authentication is never an input. Anthropic version-1
// writers use exactly accept, anthropic-version and content-type semantic
// headers. OpenAI version-1 writers use exactly accept: text/event-stream and
// content-type: application/json; no synthetic API-version header is sealed.
//
// Quote hashing frames router-cost-quote/v1, raw 32-byte request hash, capability
// version, tariff version, lowercase-hex tariff hash text, USD and canonical
// decimal maximum; then u32be(componentCount). Each component is
// u32be(classCount), framed sorted class strings, framed canonical decimals
// maxUnits, numerator and denominator. Sort the complete component encodings
// bytewise and frame each. Duplicate/overlapping classes refuse. The producer
// reserves the full min(input_max,context_max) independently of requested output
// at the largest eligible input rate, plus requested output at the largest
// output rate, with one final upward rounding. No subtraction or double count.
// OpenAI's input dimension contains its six input classes; its output dimension
// contains both output.generated.long and output.generated.short, in sorted
// order. Its usage schema is openai-responses-text-30m-kguard/v1. Usage schema
// openai-responses-text-30m-kguard/v2 is accepted beside it: v2 also knows
// input_tokens_details.cache_write_tokens_details, whose ttl_12h_tokens must be
// zero and whose members must sum to cache_write_tokens, and it tolerates
// unknown usage members that are null, integer zero or object trees of those.
// Historical producers reconstruct the bound and actual prices from the
// admission profile, never from a later tariff or only the recorded component
// maxima.
//
// OpenAI's additive version-1 producer vocabulary includes denied, exhausted,
// rate_limited, overloaded, invalid_request, unavailable, conflict, timeout,
// unreachable, protocol_error, provider_error, refused and tariff_ambiguous.
// The ambiguous interval retains the full hold,
// with threshold_ambiguous diagnostic evidence, and creates no overrun fence.
// Missing mandatory terminal counters, unsupported served tiers, unknown
// nonzero billable extensions and nonempty tool usage remain held; no-charge
// rules are empty. Complete failed, incomplete or refused terminals may still
// supply billable usage. Unknown usage members may be null or integer zero;
// top-level tool_usage may be absent/null, an empty object or an object tree
// whose leaves are integer zero. This vocabulary is producer policy, not a
// new codec closed set. Chapter 14 is an additive F3-13 OpenAI sample alongside
// the unchanged Anthropic chapter 13.
//
// Record payloads and conditional rules
//
//	runtime.paid_route_configured:
//	  route_id, expected_version, version, enabled, credential_scope_id,
//	  profile, observed_utc_ns?
//	  Observation occurs exactly when enabled, verified <= observed < expires.
//	runtime.credential_changed:
//	  scope_id, provider, expected_version, version, action, credential_version,
//	  source_kind?, source_locator?, revoke_previous_version?
//	  bind requires kind and visible ASCII locator 1..256, optionally revoking
//	  a different currently authorized version. revoke forbids all three.
//	  Version-1 source kinds are environment/windows_credential_manager;
//	  synthetic fixtures use a clearly fake source and locator. No source_id.
//	runtime.spend_configured:
//	  scope, expected_version, version, terms, parent_grant_id?, authority_version
//	  Only grant scope permits parent; a child requires its exact F3-11 parent.
//	runtime.paid_reserved:
//	  attempt_id, version, authority_version, grant_id, parent_turn_id,
//	  task_binding?, route_id, route_version, profile_sha256, request, quote,
//	  admission_utc_ns, admission_period, scopes, input_object
//	  Version 1; positive admission time; period lifetime/YYYY-MM-DD/YYYY-MM;
//	  at least four scopes; input_object equals the descriptor's body identity.
//	runtime.paid_dispatch_intent:
//	  attempt_id, expected_version, version, authority_version, request_sha256,
//	  quote_sha256, credential, observed_utc_ns
//	  Expected 1/version 2; positive observation; exactly one intent.
//	runtime.paid_result:
//	  attempt_id, expected_version, version, kind, output_started?,
//	  provider_request_id?, provider_message_id?, evidence_sha256?,
//	  evidence_object?, message_objects?, usage?, no_charge?, cost_micros?,
//	  bound_violation, error_class?, previous_result_sequence?,
//	  previous_evidence_sha256?
//	runtime.paid_overrun_acknowledged:
//	  attempt_id, expected_version, version, result_sequence, evidence_sha256,
//	  retired_profile_sha256
//	runtime.paid_receipt:
//	  command_id, submission_sha256, submission, outcome, version, attempt_id?,
//	  usage?
//
// released_unsent has expected 1/version 2, no optional result members and
// bound_violation false. All other results have expected >= 2, evidence_object
// matching evidence_sha256, and output_started. Provider IDs are nonempty UTF-8
// <= 256 bytes without controls. unknown requires error_class, retains the full
// hold and has no usage, no_charge, cost, messages or predecessor fields.
// usage/no_charge/correction require cost_micros and provider_message_id.
// usage and no_charge are mutually exclusive; correction requires exactly one.
// Both predecessor fields occur together exactly for correction. Message objects
// occur only on usage or priced correction. No-charge has cost 0, no messages,
// no bound violation and no output-started witness. Cost overflow cannot be
// settled: the attempt stays held, never clamped. The observation artifact
// retains raw usage/stop/header/event/error metadata; it is not a ledger payload.
//
// Receipts bind the re-encoded schema-4 SHA-256 to F0's command and the resulting
// entity version. attempt_id occurs exactly for admit/dispatch/observe/release/
// correct/overrun acknowledgement. usage occurs exactly for agent delegation.
// Outcomes are open; writers use configured, bound, revoked, delegated, reserved,
// sent, released_unsent, unknown, settled, no_charge, corrected, acknowledged.
// Actual envelope positions supply Sequence and EffectSequence; no self-sequence.
//
// # Record and group rules enforced by services
//
// Every operation is one [F0 command, exactly one effect, paid_receipt] group.
// Controls use the actual owner/delegating-agent origin with no turn. Admission
// command/receipt are Agent, effect Core; intent/result/recovery/correction are
// all Core at the immutable admission origin and parent turn. Every record has
// the same operationID=commandID and origin/turn. Owner overrun acknowledgement
// has no turn. No mixed, detached, reordered or extra records; refusal has no ID.
//
// Route configuration is owner CAS. A capability version permanently binds its
// full profile hash; tariff version binds its tariff hash. Disable preserves
// scope/profile and changes no hold. Bind introduces a fresh credential-version
// ID; revocation is irreversible. Restart never silently rebinds mutable sources:
// the owner's host binds a new version per scope, optionally revoking the old.
//
// First colony spend configuration fixes the period kind, including disabled
// configuration; other scopes match. Period ID is derivable from admission_utc_ns
// and the colony's period kind, so a later owner kind change can be added by
// addition. Admission recomputes colony/agent/credential/optional-task/leaf-and-
// ancestor grant scopes, requires enabled terms and current chain/task authority,
// and installs one full finite hold atomically in all scopes. Unlimited removes
// only the cap. Delegation changes no hold and cannot expand parent terms.
// External resource_key is paid-route/<route UUIDv7>; no F3-11 kind is changed.
//
// Admission and intent check current authority, caps, credential, exact seal,
// route/profile identity and trusted nonregressing time strictly before expiry.
// One shared observation floor includes prior envelope times, review and paid
// observations and successful live samples, including refused calls. Replay,
// queries, matched retries, releases and settlement read no clock.
//
// Reservation -> intent/SENT or released_unsent; SENT -> unknown or financial
// settlement; unknown -> another evidence-bound unknown or final settlement;
// settled -> duplicate or explicit correction only. Unknown ends the child once
// but retains money. Same attempt/evidence digest is inert, even with a new
// command ID. Same command/different canonical submission refuses. Corrections
// name the exact prior result/evidence for the same message/request, replace cost
// by one delta in the original admission period, and never release twice.
// Output-started is monotone; prior known provider IDs never change.
//
// Exact usage and cost beyond the bound are recorded unclamped, with a violation
// fencing every intersecting scope under finite and unlimited modes. Unknown
// or unnormalizable/overflowing usage retains the hold. Only evidence-backed
// correction or owner acknowledgement clears its exact fence. Acknowledgement
// preserves charge/hold/history and retires that profile hash across routes;
// re-enabling needs a different supported verified profile and fitting caps.
//
// Open reduces ledger facts only and reads no paid artifact bytes: for a pending
// attempt it may at most stat the input object. Content verification occurs on
// demand and reports/blocks dependent new work, never changes exposure. No
// clock, source, credential, network or append occurs during replay. A missing
// artifact cannot erase a charge, release a hold or authorize a retry.
//
// Schema-4 canonical submission (producer rule)
//
// SHA-256 covers compact JSON in order schema (4), actor, origin, operation,
// grant_id?, arguments. Closed operations and exact argument key order:
//
//	paid.route.configure:
//	  route_id, expected_version, enabled, credential_scope_id, profile_sha256
//	paid.credential.bind:
//	  scope_id, expected_version, provider, credential_version, source_kind,
//	  source_locator, revoke_previous_version?
//	paid.credential.revoke:
//	  scope_id, expected_version, provider, credential_version
//	paid.spend.configure / paid.spend.delegate:
//	  scope, expected_version, terms, parent_grant_id?
//	paid.admit:
//	  attempt_id, grant_id, parent_turn_id, task_binding?, route_id,
//	  route_version, profile_sha256, request, quote, input_object
//	paid.dispatch / paid.release: attempt_id, expected_version
//	paid.observe:
//	  attempt_id, expected_version, kind, evidence_sha256, evidence_object,
//	  message_objects?
//	paid.correct:
//	  attempt_id, expected_version, previous_result_sequence,
//	  previous_evidence_sha256, evidence_sha256, evidence_object, message_objects?
//	paid.overrun.acknowledge:
//	  attempt_id, expected_version, result_sequence, evidence_sha256,
//	  retired_profile_sha256
//
// Top-level grant_id occurs exactly for Agent admit/delegate and Core dispatch/
// observe/release/correct. Other operations are owner-only. Admit's argument
// grant matches the top-level grant. Observe kind is unknown/usage/no_charge.
// An arguments object carrying a member of another operation's argument set is
// refused.
// Derived scopes, period, observations, authority, usage, cost and new versions,
// command ID, secrets and handles are not caller inputs. A completion command ID
// is supplied freshly after the network, never reserved during intent.
// Schemas 1-3 are unchanged. Producers use the concrete argument type, not maps.
// The F3-13 golden uses fixed IDs/times, encoded submissions and EncodeCommit,
// with loopback-only synthetic profiles and context-owned hash-named objects.
// TestGoldenF3_13 -update alone writes its new fixture files.
//
// # Registration and declaration records (F3-139)
//
// Ticket 139's four version-1 types and schema-5 canonical submission are
// frozen (D154). This addition does not change format version 1,
// F0, earlier families, schemas 1-4, grants, task lifecycle or authorization.
// Registrations and declarations are evidence, never permission. A resource
// registration is not a prerequisite for an existing external grant; a work
// domain is not a new F3-11 Resource kind. Record availability alone does not
// establish an operational Core producer or Ticket61 graph projection.
//
// Payload owner: C3. The designated producer and sole permitted appender caller
// is internal/core/agents, through ledgerstore.Submit on an owner transition.
// Consumers are Core replay and owner-authorized metadata queries, followed by
// Ticket61's independently authorized record consumer. All known payload
// fields, required group envelope bindings, canonical submission and actual
// effect/receipt sequences are replay-essential. Metadata is access-filtered:
// opaque identities, exact external addresses, versions, audiences, pinned
// sources, consumers and receipts, not private content or provider configuration.
// No labels, arbitrary descriptions, bodies, credential, account, locator,
// capability handle, measurement or resolver is introduced.
//
// # Codec rules
//
// MaxRegistrationPayloadBytes is 65536 for each complete payload and schema-5
// submission. Check received byte length BEFORE parsing, counting whitespace
// and ignored extensions, and check the compact encoded output independently.
// The receipt cap covers the whole receipt, not just its embedded submission.
// Input is one complete UTF-8 JSON object. Duplicate members are rejected at
// every depth, even inside ignored extensions. Every known field below is
// required and non-null. Unknown optional members are ignored at every level;
// encoders emit known fields only. A known member belonging to another kind
// or operation is forbidden even when null. Errors wrap ErrInvalid.
//
// UUIDs are canonical lowercase UUIDv7. Digests are nonzero lowercase SHA-256.
// Integers are plain unsigned JSON decimal integers through MaxInt64, never
// signed, fractional, exponent notation or strings. Expected zero creates
// version one; positive expected advances exactly one. Expected must be less
// than MaxInt64: check before addition. Reference versions are positive.
// The codecs enforce shape, set ordering, versions and receipt binding, not
// state/history existence, currentness, scope containment or population limits.
//
// Exact nested known-field order:
//
//	RegistrationRef: kind, id, version
//	  kind is resource or work_domain; ID namespaces are kind-scoped.
//	DeclarationConsumer task: kind ("task"), task_id
//	  binding is forbidden.
//	DeclarationConsumer context: kind ("context"), binding
//	  binding uses agent_id, context_id; task_id is forbidden.
//	Resource: kind ("external"), resource_key
//	  existing F3-11 Resource grammar; all other kinds/members are forbidden.
//	Origin: agent_id, session_id, context_id
//
// Audience is a required nonempty array, strictly UUID-byte ascending/distinct.
// Consumers is a required, possibly empty array, strictly ascending/distinct by
// compact known-field JSON UTF-8 bytes. Context sorts before task. No numeric
// fan-out limit is part of decoding; the complete encoded-size cap bounds both.
// The same UUID may identify a resource, a work domain and a declaration:
// there is no family-wide cross-kind exclusion.
//
// A resource registration binds an explicit Resource, not a key derived from
// its ResourceID. The external key retains F3-11's 1-256-byte UTF-8/no-control
// grammar. A supplied bare UUID need not equal ResourceID; paid-route/<UUID>
// and ordinary opaque non-UUID keys work equally. Compare exact bytes, without
// trimming, case folding, Unicode normalization, aliases, prefix interpretation,
// URL parsing or implicit fallback. The trusted owner/host supplies nonsecret
// opaque identity, never a URL, account identifier or credential. The byte
// grammar cannot prove that semantic precondition and is not a secret detector.
// A paid-route-shaped key is ordinary identity here: no route, credential,
// provider configuration, spend terms or provider invocation is required.
// Address immutability and uniqueness are service state rules below.
//
// Exact payload known-field order (all required):
//
//	runtime.resource_registered:
//	  resource_id, expected_version, version, resource, audience
//	runtime.work_domain_registered:
//	  work_domain_id, expected_version, version, audience
//	runtime.declaration_changed:
//	  declaration_id, expected_version, version, subject, audience, consumers
//	runtime.registration_receipt:
//	  command_id, submission_sha256, submission, outcome, version
//
// Each effect is a complete selected version, not just a creation notice.
// Subject is a RegistrationRef. Consumers replaces the entire declaration set;
// [] withdraws its edges without deleting its identity. Receipt version is the
// resulting target version and equals the submission expectation plus one.
// Outcome is an open token matching ^[a-z][a-z0-9_]*$, at most 64 ASCII bytes.
// Writers emit applied. Decoders preserve unfamiliar well-formed outcomes,
// but Core fails closed on them rather than applying them as success.
// All other documented kinds, actor and operations are closed.
//
// Schema-5 canonical submission (producer rule)
//
// Root order is schema (5), actor ("owner"), origin, operation, arguments.
// Exactly three operations and concrete argument types exist:
//
//	resource.set / ResourceRegistrationArguments:
//	  resource_id, expected_version, resource, audience
//	work_domain.set / WorkDomainRegistrationArguments:
//	  work_domain_id, expected_version, audience
//	declaration.set / DeclarationArguments:
//	  declaration_id, expected_version, subject, audience, consumers
//
// The operation and concrete arguments bind target kind and identity; no
// redundant target wrapper, register/revise operation or inferred Resource
// exists. Origin includes the actual registered sole session and context.
// SHA-256 covers exact compact known-field RegistrationSubmission.Encode bytes,
// using encoding/json ordering/escaping, including HTML and U+2028/U+2029
// escaping. Receipt decoding recomputes this digest. Unknown optional fields
// do not enter that preimage. Command ID, time, committed sequences, resulting
// version, views and ephemeral handles remain outside it. The exact explicit
// external address, audience, subject pin and consumers are inside it.
//
// Core preflight, replay and state rules (not codec history lookups)
//
// Each transition is one Submit and one complete three-record commit:
//
//	resource.set: ledger.command, runtime.resource_registered,
//	  runtime.registration_receipt
//	work_domain.set: ledger.command, runtime.work_domain_registered,
//	  runtime.registration_receipt
//	declaration.set: ledger.command, runtime.declaration_changed,
//	  runtime.registration_receipt
//
// All three envelopes have actor owner, identical registered agent/session/
// context origin and operation_id equal to command_id. turn_id, measurement_path
// and body_refs are absent. No extra, missing, reordered or mixed-family record
// is allowed. F0 and receipt command/digest agree. Effect identity, version,
// fields and receipt result reconstruct exactly from the canonical submission
// and pre-transition state. Preflight additionally checks the ORIGINAL normalized
// request, not merely an internally consistent altered group and digest.
//
// Only the trusted owner Service may mutate or query this family, never Agent.
// Caller-supplied Binding is not authentication: bind owner actor and current
// registered origin internally. Copy arrays before sorting, never deduplicate;
// duplicates are invalid. Normalize nil producer consumers to []. Bound copying
// by the payload-derived element-count guard and then check submission, effect
// and whole receipt encoded sizes.
//
// Global command deduplication precedes CAS, source-currentness and admission
// limits. A retry matches family, actor, origin, operation and canonical digest
// and returns the original actual receipt, even after subsequent revisions.
// Command IDs remain global across families. A new identity expects zero;
// a revision expects its current version and advances exactly one.
//
// One external address belongs to at most one resource registration in the
// colony and never changes for that identity. Resources, domains and
// declarations use separate kind-scoped identity namespaces. Every audience
// agent and every origin/context must be registered. A declaration pins the
// source version current at its commit. Its audience is contained in that
// pinned source audience and every named task's audience; each consumer
// context's agent belongs to the declaration audience. No inferred roles,
// future membership or automatic scope expansion is allowed.
//
// Task consumers name stable task IDs, not assignments or generations;
// unassigned and terminal tasks may be consumers. Context consumers name exact
// bindings, never reassignment-following aliases. Source revisions do not
// retarget, narrow or withdraw existing declarations. Explicit declaration
// replacement is required. Registration narrowing is neither grant revocation
// nor automatic edge withdrawal. Audience membership is evidence scope, not
// disclosure permission; later graph projection needs independent filtering.
//
// Only two future producer population defaults are specified:
// MaxRegistrations=256 counts resources plus domains; MaxDeclarations=1024
// counts declaration identities, including withdrawn ones. Revisions consume
// no new identity slot. These settings do not constrain decoding, replay or
// matching retries, and this contract adds no CoreLimits implementation.
// The default population times per-record ceilings is 80 MiB of encoded latest
// snapshots, not a measured Go heap bound or a capacity claim.
//
// Replay reconstructs validated committed facts without provider work or new
// registration appends. Invalid group/state is inconsistency, not absence.
// Preserve Close, cancellation and poisoning semantics. Read actual committed
// effect/receipt sequences and install through the committed boundary before
// acknowledgement; a lost acknowledgement does not undo a committed group.
// No predicted receipt, exactly-once delivery claim or durable ack marker exists.
//
// Owner queries expose isolated typed metadata only. Resource lookup uses the
// exact external Resource address; domain/declaration lookup uses its UUID.
// Exact-version lookup resolves the pinned RegistrationRef; command lookup
// returns the original receipt. Zero cursor means genesis; ToEnd captures one
// current durable boundary; numeric cursors must be complete commit boundaries
// at or before it. Reject future/mid-commit cursors. Historical selection scans
// validated complete commits and cannot borrow a later source version.
// No listing/search/raw-ledger endpoint, history cache or second authority store
// is specified here.
//
// # Golden
//
// TestGoldenF3_139 -update alone writes every file in testdata/f3/139:
// one generated log, real hash-named initialization objects and one task body.
// The fixed colony is 019a1390-0000-7000-8000-000000000001.
// Five prerequisite commits contain two F3-10 agent registrations, one work
// context, one owner unassigned F3-11 task and one owner root external grant:
// 23 records. The grant precedes registration and uses the exact opaque
// paid-route/<id(0x450)> address, independently of providers or route records.
// Fourteen new-family groups add 42 records: 19 commits/65 records total.
// The first new effect/receipt are 25/26 and the final pair is 64/65.
// The fixture covers both source kinds to task/context, source narrowing and
// widening, pinned revisions, mixed consumers and explicit empty withdrawals.
// Earlier frozen fixture bytes remain unchanged. Before joint freeze review,
// the complete fixture's earlier-family prerequisites must also pass actual
// ledgerstore.Open and agents.Open; reader/codec success alone is insufficient.
//
// # Bound turn and tool journal records (F3-15)
//
// Ticket 15's six version-1 types and canonical schema-6 submission are
// frozen (D165). They add to ledger-format version 1: Defined
// grows from 53 to 59 types. F0, F2, F-U3-61, F3-10/11/12/13/139, schemas 1-5,
// their codecs, group rules and golden bytes are unchanged. F3-15 groups embed
// no earlier-family record. Schema 6 lives only in runtime.loop_receipt,
// beside the earlier receipt types, never inside runtime.scoped_receipt.
//
// Payload owner and designated producer: C3, internal/core/agents, through
// the existing Core Submit boundary. Consumers are Core preflight/replay,
// private-head/history/source-reference readers, turn-child accounting and
// currently authorized context views. All known payload members, normalized
// submissions, envelope bindings and exact source/effect/receipt identities
// are replay-essential. Records hold access-filtered metadata and private
// object references, never raw input, arguments, results, final text, credential
// sources/values, secret-derived identities or body_refs. Views are not grants.
// Registration remains evidence, not permission. No unfiltered agent stream,
// new authority store, money book or appender caller is introduced.
//
// Additive shared-state readings:
//
// (a) private.turn_revision advances the SAME context revision and transcript
// predecessor chain as private.revision and private.scoped_revision. Consumers
// of private heads consume all three in ledger order, including live
// installation, queries, historical reconstruction, source-reference checks,
// integrity and history traversal. F3-10+F3-11-only private-head consumers
// MUST NOT be used on a log containing F3-15.
//
// (b) runtime.tool_started is a required-result child of its parent turn;
// exactly one runtime.tool_result ends it. Turn-child completion consumers
// consume F3-11 local work, F3-13 paid attempts and F3-15 tool invocations.
// F3-11 released/cancelled/interrupted turn endings require all three families'
// child endings. A tool result releases NO local or money accounting;
// unknown_effect ends the journal child but is not a successful required effect.
// Existing unknown local/paid holds remain held. An old-family-only child
// predicate MUST NOT be used on a log containing F3-15.
//
// (c) F3-11 arguments_sha256 remains an opaque digest. This producer supplies
// SHA-256 of the exact normalized invocation bytes described below and compares
// the same digest again at local.begin. This changes no F3-11 encoding or rule.
//
// # Codec rules and ordered members
//
// MaxLoopPayloadBytes is 65,536 bytes for each COMPLETE input and each compact
// output independently, including whitespace, ignored extensions and the
// entire receipt with its embedded submission. Check the received bound before
// parsing. One complete UTF-8 JSON object, recursively unique keys even in
// ignored extensions, required fields, no null known field, canonical lowercase
// UUIDv7s and nonzero lowercase SHA-256 digests follow F0. Unknown optional
// members are ignored; encoders emit known members only. Known members of
// another schema-6 operation are forbidden, even when null. Errors wrap
// ErrInvalid. Optional members are omitted when absent, not encoded as
// zero/empty/null. Integers are plain unsigned JSON decimals through MaxInt64,
// not strings, fractions, exponents or signed values. Positive sequences name
// actual ledger records; checked revision increments cannot overflow.
//
// ObjectRef is exactly F3-11's {sha256,bytes}, with nonnegative bytes.
// Origin is {agent_id,session_id,context_id}; Resource is unchanged F3-11.
// PrivateChange has the unchanged {slot,mode,sha256,bytes,previous_sha256?}
// order for append. Runtime byte/count ceilings are not extra decoder limits.
//
// tool_name matches ^[A-Za-z0-9_-]{1,64}$ (ASCII), not the operation grammar.
// provider_call_id is 1-256 visible ASCII bytes, 0x21 through 0x7e.
// tool_schema is positive; current writers emit 1. Outcome and reason are open
// ^[a-z][a-z0-9_]*$ tokens of at most 64 bytes. tool_started.operation is open
// dot-separated such tokens, at most 64 bytes overall. Unfamiliar well-formed
// tokens decode but consumers hold/refuse them as unsupported, never infer
// success, permission, completion or release. Schema, submission operation,
// actor, scope, slot and mode use the closed sets specified here.
//
// Exact payload key order (? means optional, not null):
//
//	private.turn_revision:
//	  scope, expected_version, revision, changes
//	runtime.turn_bound:
//	  turn_id, admission_sequence, input_object, policy_object, render_object
//	runtime.tool_started:
//	  tool_id, parent_turn_id, attempt_id, provider_call_id, tool_name,
//	  tool_schema, request_object, request_sha256, operation, target?,
//	  grant_id?, effect_command_id?
//	runtime.tool_result:
//	  tool_id, started_sequence, outcome, result_object, effect_receipt_sequence?
//	runtime.turn_result:
//	  turn_id, bound_sequence, outcome, reason, model_calls, tool_dispatches,
//	  final_message_object?, ending_sequence
//	runtime.loop_receipt:
//	  command_id, submission_sha256, submission, outcome, effect_sequence
//
// private.turn_revision requires scope=context, expected_version>=1,
// revision=expected_version+1, and exactly one transcript/append change.
// Transcript bytes are positive; predecessor is omitted only for the first
// chunk, otherwise the actual preceding chunk digest from the shared head.
// No other slot, initialization, replacement or clear is permitted.
//
// turn_bound selects three immutable owning-context objects after the ordinary
// F3-11 admission. admission_sequence identifies that work_admitted effect,
// whose work_id equals turn_id; its command is admission_command_id.
//
// tool_started.request_sha256 equals request_object.sha256. The tool ID is
// Core-minted, not derived from a provider string. The same (attempt_id,
// provider_call_id) selects only one tool ID and request digest, within the
// bound originating turn/context. A second start or changed meaning refuses.
// target is omitted for invalid_argument, unrecognized, tools.discover and
// task.list; never publish an inaccessible target. grant_id is required EXACTLY
// when effect_command_id is absent and operation is neither invalid_argument
// nor unrecognized: it is the leaf of the one current chain authorizing that
// passive/catalogue disclosure. Those two invalid operations omit target,
// grant_id and effect_command_id. A well-formed passive or catalogue request
// that no current chain authorizes is recorded as unrecognized, like an
// unknown one (D166). Native mutating/accounted entrances require
// effect_command_id and omit grant_id, relying on the native authorization
// record instead. Codec history-free checks cannot establish current authority.
//
// For file tools, effect_command_id names local.admit, while the matching
// tool_result.effect_receipt_sequence names local.end's receipt. The consumer
// verifies that ending's work was admitted by that command. For atomic and
// accounted read operations both name the one native command and its receipt.
// Passive/correction results omit effect_receipt_sequence; when present it is
// positive. A result whose start carries effect_command_id omits it only for a
// definite pre-commit refusal (never ok or unknown_effect), when that native
// command was never committed; a native receipt later found for that command
// ID is an integrity issue (D166). A result's started_sequence selects its
// exact unmatched tool start.
// No native command reuses the outer loop command ID.
//
// Current tool-result writer outcomes: ok, invalid_argument, denied, busy,
// conflict, too_large, unavailable, unknown_effect. busy describes only
// workspace-reported context_busy/merge_back_pending/scratch-busy facts.
// Admission/dispatch resource busy is waiting_resource in the scheduler,
// producing NO tool_result or model-visible error and consuming no counter;
// the pending tool retains its tool ID and fixed effect command ID.
//
// Current tool-operation writer values: file.read, file.search, file.write,
// file.edit, context.inspect, turn.inspect, task.inspect, task.start, task.wait,
// task.resume, task.submit, task.cancel, task.create, task.claim, task.assign,
// task.complete, task.fail, task.reassign, task.list, private.read, private.write,
// artifact.read, tools.discover, invalid_argument and unrecognized. An unknown
// syntactically valid provider name is journaled as unrecognized, never echoed;
// a name outside tool_name's grammar remains a content failure.
//
// Current turn-result outcomes: finished, cancelled, failed, interrupted.
// Current reasons: natural_stop, cancelled, shutdown, authority_lost, deadline,
// loop_limit, provider_error, content_invalid, unresolved_result, integrity,
// scratch_pending, request_too_large. The latter distinguishes a next sealed
// request/object/manifest that cannot fit from invalid content. finished
// requires a complete validated natural answer, selected final_message_object
// (an empty text-object list is allowed), and no required pending result.
// Cancelled/failed/interrupted omit final_message_object. Counts are nonnegative.
// This is neither task completion nor promotion. ending_sequence identifies
// the actual F3-11 work_ended effect, whose command is ending_command_id.
// Native outcome released accompanies normal/failed closure, cancelled
// cancellation, interrupted shutdown/interruption. released is NOT model
// success. Receipt writers emit applied; effect_sequence is the actual runtime
// effect (turn_result for loop.end), not the private revision or a prediction.
//
// # Schema-6 canonical submission
//
// SHA-256 covers compact LoopSubmission.Encode JSON in this exact key order:
//
//	schema (6), actor, origin, turn_id, operation, expected_version, arguments
//
// Exactly four submission operations exist, with concrete arguments:
//
//	loop.bind: turn_id, admission_command_id, input_object, policy_object,
//	  render_object
//	tool.begin: the exact runtime.tool_started order above
//	tool.result: the exact runtime.tool_result order above
//	loop.end: turn_id, bound_sequence, outcome, reason, model_calls,
//	  tool_dispatches, final_message_object?, ending_command_id
//
// Actor is owner for loop.bind, agent for tool.begin, core for tool.result and
// loop.end. expected_version is 0 for bind/begin, 1 for result/end: one immutable
// terminal per matching start/binding, NOT the independent private-head CAS.
// The argument turn_id/parent_turn_id equals the outer turn_id. Source sequences
// and IDs are bound, then verified against real readback identities. Private
// manifest/revision predecessors derive from current private state, not guesses.
// encoding/json's compact key order and escaping define the preimage; unknown
// optional members do not enter it. Receipt decoding re-encodes and checks it.
//
// Normalized invocation preimage, a producer rule, is compact
// {schema:1,tool_name,operation_schema:1,operation,target?,arguments}, with the
// concrete argument schema's key order and decimal integers. It binds tool
// path, schema revision and concrete target. request_object selects these
// exact bytes and request_sha256 hashes them; local.admit arguments_sha256
// and local.begin compare that same hash. Only declared model argument
// equivalents are normalized; ledger/law/launcher parsing remains strict.
// Malformed invocation evidence instead selects a private
// {schema:1,tool_name,raw_arguments_sha256} after the literal-secret check.
// Any retained raw rejected arguments are a bounded JSON string, not executable
// RawMessage. Its result is a static bounded correction, with zero repair calls.
//
// Global authorized outer-command lookup precedes minting internal admission,
// effect or ending IDs. A retry reuses the recorded IDs, original policy/
// deadline and render selections; no new meaning, deadline or effect is minted.
// Same command/principal/digest returns its receipt only after current
// disclosure authorization; mismatch refuses globally across schemas 1-6.
//
// # Service-enforced groups and replay
//
// Each complete transition is one Submit, with C=ledger.command and
// R=runtime.loop_receipt:
//
//	loop.bind:   C, runtime.turn_bound, R
//	tool.begin:  C, runtime.tool_started, R
//	tool.result: C, runtime.tool_result, R
//	loop.end:    C, private.turn_revision, runtime.turn_result, R
//
// All envelopes bind identical registered agent/session/context/turn and
// operation_id=command_id. Effects and private revision have actor core;
// command/receipt actors equal the submission actor above. No measurement_path,
// body_refs, unrelated, extra, missing, reordered or mixed-family records.
// F0 command and receipt digests agree; submitted effects and all prior links
// reconstruct exactly from the ORIGINAL normalized input and pre-transition
// state. Actual readback sequences, not predicted cursors, establish identity.
// One reducer validates live preflight and Open; uncertainty poisons the service.
// Reader/EncodeCommit/ledgerstore do not enforce these producer history rules.
//
// Publish private input/request/result/final/manifest objects before selecting
// them. The append-only runtime/paid journal is canonical during an active turn;
// publish ONE bounded turn-manifest transcript append at loop.end, not a private
// revision per journal event. Ordinary read-to-CAS survives mere journal traffic;
// an actual intervening private write still conflicts. Loop closure follows
// native EndTurn, after all children end and writers are observed/closed.
//
// Bind follows separate ordinary StartTurn admission and precedes first effect.
// Busy admission commits no binding, retains no input and queues nothing. A
// crash between admission and bind leaves an orphan admission, recovery_pending
// and nondispatchable; retries query fixed IDs rather than silently queueing.
// Definite pre-bind publication failure honestly ends the never-dispatched
// admission; uncertainty poisons and requires recovery. A tool-start records
// accepted invocation, NOT execution authorization. Each native operation still
// obtains fresh current authority/admission; every model call uses its own
// separately admitted F3-13 paid attempt. Never resend an already-started tool
// because its result is absent. Unknown unpaired work remains recovery_pending.
//
// Ordinary Open reduces metadata but never dispatches, reads credential
// sources or paid objects, resumes waits, or auto-executes tools. Current and
// historical private integrity checks cover runtime-owned transcript objects.
// No recovery bookkeeping is evidence of ticket 16's mid-turn crash/race proof.
//
// Not frozen by this family: turn-manifest chunk schema, final-message manifest,
// request/result/replay object encodings, TurnPolicy/defaults, session interface
// and terminal settings. Legacy opaque transcript chunks are never reinterpreted
// as a turn manifest. The new golden is testdata/f3/15, generated only by
// TestGoldenF3_15 -update with synthetic fixed IDs/times and encoded hashes.
// It exercises all six types and all four ordered groups; earlier goldens
// remain byte-identical. Before review its earlier-family setup must pass real
// Core Open with no error, no issue and unchanged durable sequence. A codec
// golden alone does not establish an operational loop or provider acceptance.
//
// # F3-16: durable continuation owner
//
// Ticket 16's runtime.continuation_changed and runtime.continuation_receipt,
// both version 1, and canonical schema 7 are frozen (D169).
// Defined grows from 59 to 61. This is an additive family: every earlier
// codec, schema, group and golden remains unchanged. No Core producer consumed
// the family before its freeze. C3 owns the payloads; the designated
// producer is internal/core/agents through the existing Core Submit boundary.
// Consumers are preflight/Open, the single continuation owner, exact-origin
// views, cancellation and recovery. All known members and envelope bindings
// are replay-essential. Records contain metadata and private object references,
// never private text, provider continuation artifacts, credential sources,
// secret-derived identities, locators, native handles or body_refs.
//
// MaxContinuationPayloadBytes is 65,536 bytes for each COMPLETE input and each
// compact output independently, including ignored extensions and the entire
// receipt with its embedded submission. Schema 7 itself has the same bound.
// F3-15's complete UTF-8 JSON, recursive duplicate-key refusal (including
// ignored extensions), canonical UUIDv7/nonzero digest and integer rules apply.
// Integers are plain unsigned decimals through MaxInt64; version and generation
// increments are checked, never wrapped. Optional members are omitted, never
// null, empty or zero. Required pending and notice_cursors arrays may be empty
// but are never null. Unknown optional members are ignored and do not enter the
// canonical preimage. Errors wrap ErrInvalid. Origin and ObjectRef are unchanged.
//
// Exact key order (? denotes omission when absent):
//
//	runtime.continuation_changed:
//	  turn_id, bound_sequence, expected_version, version, expected_state,
//	  state, resume_state?, operation_id, generation, wait_reason, model_calls,
//	  tool_dispatches, deadline_utc_ns, pending, stop_reason?, cancel_command_id?,
//	  final_message_object?, native_ending_sequence?, notice_cursors
//	pending element:
//	  kind, id, command_id?, expected_version, source_sequence?
//	notice_cursors element:
//	  workspace_id, scratch_id, after_sequence
//	runtime.continuation_receipt:
//	  command_id, submission_sha256, submission, outcome, effect_sequence
//
// Pending is strictly ordered by (kind,id,command_id), cursors by
// (workspace_id,scratch_id), on raw token/UUID bytes. Absent command_id sorts
// before a present ID. Duplicate keys and out-of-order arrays refuse on both
// encode and decode; callers supply canonical order. Runtime's 128-entry pending
// ceiling is a producer guard, not a historical decoder limit.
//
// Current pending kinds are paid_attempt, tool, local_work, scratch_observation
// and merge_back. command_id is present EXACTLY when the child has a native
// command: paid_attempt names paid.admit; tool names effect_command_id (absent
// for passive/correction tools); local_work names local.admit; scratch_observation
// and merge_back name the F2 command. id is the corresponding attempt, tool,
// local work, observation or merge-back identity; expected_version binds that
// child's expected state. Positive source_sequence selects its exact committed
// source. An entry with no source is pre-minted, never dispatched at recovery,
// and is dropped by owner.reconcile. Source, child, command and originating
// context identity are checked against actual history, not a predicted cursor.
//
// States, expected states, resume states, wait/stop reasons, pending kinds and
// receipt outcomes are open ^[a-z][a-z0-9_]*$ tokens of at most 64 bytes.
// Unknown well-formed values survive decoding as unsupported; they never imply
// dispatch, success, completion, permission, release or an executable transition.
// Current states: ready, model_pending, tool_pending, waiting_resource, finishing,
// stopping, reconciling, terminal. absent is reserved for expectation version 0,
// not a snapshot state. resume_state is required for waiting_resource and omitted
// for other known states; current values are model_pending, tool_pending,
// finishing and reconciling. A wait requires a non-none wait_reason.
// Current wait reasons: none, paid_exchange, paid_prepare, local_slot,
// local_buffer, workspace_capture, workspace_merge_back, child_quiescence and
// effect_reconciliation. ledger_tail is NOT a writer wait reason: a moved-tail
// refusal reruns the quantum and never parks or writes a transition. Ordinary
// nonwaiting phases use none; stopping/reconciling may identify child/effect
// reconciliation. Writers persist a park before waiting, and resume only after
// the resource claim succeeds, before the resumed phase's first effect.
// A spurious wake or a still-busy rerun writes no transition.
//
// # Schema-7 canonical submission and group
//
// SHA-256 covers compact ContinuationSubmission.Encode JSON, in this order:
//
//	schema (7), actor, origin, turn_id, operation, expected_version,
//	  expected_state, arguments
//	arguments:
//	  bound_sequence, state, resume_state?, operation_id, generation, wait_reason,
//	  model_calls, tool_dispatches, deadline_utc_ns, pending, stop_reason?,
//	  cancel_command_id?, final_message_object?, native_ending_sequence?,
//	  notice_cursors
//
// The arguments are one concrete snapshot, never arbitrary JSON. turn_id,
// expected_version, version and expected_state are forbidden argument members,
// even when null. Schema, actor and operation are closed. The five operations
// are owner.initialize, owner.advance, owner.stop, owner.reconcile, owner.close.
// Actor is core except a Session-originated owner.stop uses owner and that
// Session command ID. No agent entrance is added. Every operation has exactly
// one group, [ledger.command, runtime.continuation_changed,
// runtime.continuation_receipt], in one SubmitAt at the preflighted tail.
// Three moved-tail attempts then return BusyError{ledger_tail}.
//
// All envelopes bind the same original agent/session/context/turn and have
// operation_id equal to THIS transition's command_id. The change actor is core;
// command and receipt actors match the submission. In contrast, the PAYLOAD's
// operation_id is the command ID of the transition that opened the logical
// operation: initialize, advance from ready, stop or a new recovery operation.
// Later park/resume/progress/close transitions retain it with their own envelope
// command IDs. Generation starts at 1, stays fixed within an operation and
// increments when a new operation replaces it. No counter is an incarnation's
// ephemeral wake generation.
//
// Receipt writers emit recorded. effect_sequence is positive and names the
// actual change record after authoritative readback. Receipt decoding re-encodes
// schema 7 and verifies submission_sha256. All effects reconstruct from the
// original normalized submission and pre-transition state. No unrelated, mixed,
// extra, reordered or missing records, measurement_path or body_refs.
// Reader/EncodeCommit/ledgerstore do not enforce these producer history rules.
// Global command lookup extends schemas 1-6 to 1-7: same ID/principal/digest
// returns the original receipt after current disclosure authorization; changed
// meaning is ErrCommandMismatch before state CAS.
//
// # Owner transitions, stopping and recovery
//
// Initialize expects 0/absent and an exact unmatched F3-15 binding, writes
// version 1/generation 1 in ready (live) or reconciling (recovery), before enqueue.
// Later snapshots compare exact expected version/state, operation and generation;
// version=expected_version+1. Original origin/turn/bound_sequence/deadline never
// change; counters and notice cursors never regress. Allowed known-state edges:
//
//	ready -> model_pending/tool_pending/finishing: advance, new operation;
//	model_pending/tool_pending -> ready/finishing: advance, matching child result;
//	model_pending/tool_pending/finishing/reconciling -> waiting_resource:
//	  same operation, resume_state is the preceding phase;
//	waiting_resource -> its resume_state: same operation, successful fresh claim;
//	nonterminal without native ending -> stopping: stop, new operation;
//	stopping -> finishing/reconciling: same stop operation, bookkeeping only;
//	inert nonterminal -> reconciling: reconcile, new operation unless already so;
//	finishing/reconciling -> terminal: close, same operation, matching endings;
//	state -> same state: advance/reconcile for actual child/counter/cursor progress.
//
// Other known edges refuse; no change means no new record. Terminal never
// reopens. Callback tokens bind origin, turn, operation, generation, expected
// phase/version and result identity. A stale callback returns
// ErrContinuationStale without a record, counter, wake or effect; exact retries
// return their original receipt. Post-stop results are reconciliation evidence,
// never a ready edge. Counters fold actual committed paid dispatch intents and
// completed tool invocations exactly once; a crashed started tool counts once
// when recovery journals its unavailable/unknown result. Waiting counts nothing.
//
// Every non-natural ending uses ONE owner.stop edge, generation+1, retaining
// pending identities. stop_reason is an F3-15 reason other than natural_stop:
// cancelled, shutdown, deadline, loop_limit, authority_lost, integrity,
// provider_error, content_invalid, request_too_large, unresolved_result or
// scratch_pending. The first durable stop wins; later stops return current state
// without another record, and shutdown never relabels a persisted cancellation
// or committed ending. cancel_command_id is present exactly with cancelled and
// names the pre-minted F3-11 turn.cancel command, DISTINCT from the owner.stop
// envelope/Session command ID. Historical schema-2 cancellation retries retain
// their original receipt through global lookup.
//
// Stop publication and native ending serialize under the context scope gate.
// A stop finding any committed native ending is too_late and writes no record;
// a finish finding a stop uses that stop's reason and fixed native cancellation
// ID. Persist before signaling; persistence failure is not reported as success.
// final_message_object is selected in the original context only for validated
// natural finish, before native EndTurn; a pre-ending stop removes it.
// native_ending_sequence appears only after the exact F3-11 ending exists and
// is required at terminal. owner.close additionally requires the matching
// F3-15 terminal and no executable pending work. Unknown financial holds may
// remain; waits/terminal bookkeeping never release them or reuse unconfirmed
// child resources. A native ending already committed fixes the original outcome.
//
// Open reconstructs inert owners/waits and interrupted outcomes, without clock,
// credentials, dispatch, scheduler, tool execution or automatically resumed work.
// Explicit reconciliation records child endings first (F3-13/F3-11/F3-15),
// then F3-11 native ending, F3-15 loop.end and F3-16 owner.close. Existing native
// journals remain the sole authorities for effects and holds. Recovery never
// re-executes a started tool, model exchange, observation or merge-back. Unpaired
// legacy bindings initialize reconciling, not ready. Repeated bookkeeping cannot
// duplicate an ending, charge or observation.
//
// Additive guarded-turn reading, ONLY where an F3-16 owner exists: local.begin,
// paid.dispatch intent, tool.begin and atomic effect commands require current
// phase/operation/generation, exact pending identity and no persisted stop in
// BOTH producer preflight and replay. The promised-native-ID directory supplies
// the turn for atomic groups omitting turn_id. local.end, paid observe/release/
// result/correction, tool.result, turn.end and turn.cancel retain their original
// IDs and remain allowed after stop. Old ownerless logs are unaffected.
//
// Notice cursors advance in the first owner.advance after the turn's first
// committed F3-13 dispatch intent. Busy/failed pre-intent starts consume nothing;
// a crash before cursor publication may repeat the same notice on the next
// natural turn. Cursors survive terminal eviction through ledger/directory
// reconstruction. They are presentation progress, never deletion, authority or
// a completion-triggered model call. Status needs no cursor.
//
// testdata/f3/16 is generated only by TestGoldenF3_16 -update, using synthetic
// fixed IDs/times, encoded hashes and real earlier-family setup. It covers all
// five operations, wait, cancellation, unchanged-prefix stale refusal and
// recovery/terminal snapshots. Earlier fixtures remain byte-identical. Codec
// fixtures alone do not prove runtime recovery, concurrency or child shutdown.
// No runtime policy, pending ceiling, scheduler setting, private object schema,
// Session API or native process mechanism is frozen by this family.
//
// # F3-90: generic agent eligibility
//
// Ticket 90's runtime.eligibility_changed and runtime.eligibility_receipt,
// both version 1, and canonical schema 8 are frozen (D174).
// Defined adds two entries to the preceding families. Earlier codecs, schemas,
// groups and goldens stay byte-identical. C3 owns this metadata-only family;
// internal/core/agents is its designated producer through SubmitAt after freeze.
// Consumers are Core preflight/Open, scheduling/admission and authorized views;
// later C6/C7/C8 producers inherit the same Core boundary. No skill, alignment,
// human approval, service-loss lifecycle or new authority is defined here.
// All known members and envelope bindings are replay-essential. No raw input,
// Purpose, private text, credential, approval token, native handle, locator,
// measurement_path or body_refs belongs in this family.
//
// MaxEligibilityPayloadBytes is 65,536 bytes independently for each COMPLETE
// input and compact output, including ignored extensions and the receipt's
// embedded submission. Schema 8 has the same bound. F3-16's complete UTF-8 JSON,
// recursive duplicate-key refusal (including ignored extensions), canonical
// UUIDv7/nonzero digest and unsigned decimal integer rules apply. Integers run
// through MaxInt64; version increments are checked, never wrapped. Absent
// optionals are omitted, never null. Unknown optional members are ignored and
// excluded from canonicalization. Malformed known values wrap ErrInvalid.
//
// Exact compact member order (? denotes omission when absent):
//
//	runtime.eligibility_changed:
//	  expected_version, version, state, reason, operation_id, first_action?
//	first_action:
//	  action_id, initial_turn, permitted_turn, refusal?
//	initial_turn / permitted_turn / submission turn:
//	  context_id, turn_id, start_command_id, admission_command_id,
//	  input_sha256, task_binding?
//	task_binding:
//	  task_id, assignment_id, generation
//	refusal:
//	  turn_id, start_command_id, stage, code
//	runtime.eligibility_receipt:
//	  command_id, submission_sha256, submission, outcome, effect_sequence
//
// State is CLOSED: eligible, ordinary_work_ineligible, first_action_required.
// Unknown states fail decoding/Open, never become eligible. Reason, refusal
// code and receipt outcome are open ^[a-z][a-z0-9_]*$ tokens, at most 64 bytes.
// Current reasons are none, explicit_hold and first_action. Eligible uses none;
// held states use a non-none reason. Any well-formed unfamiliar non-none reason
// remains held. Writers emit receipt outcome recorded. Unfamiliar outcomes
// remain opaque, not an implied release, success or executable transition.
//
// # Schema-8 canonical submission and group
//
// SHA-256 covers compact EligibilitySubmission.Encode JSON, in this order:
//
//	schema (8), actor, origin, operation, expected_version,
//	  expected_operation_id?, arguments
//
// Schema, actor and operation are CLOSED. Arguments is the operation's concrete
// value, never arbitrary JSON. Its exact order and actor are:
//
//	eligibility.hold           owner  {reason}
//	eligibility.require_first  owner  {reason, action_id, turn}
//	eligibility.first_next     owner  {action_id, turn}
//	eligibility.release        owner  {}
//	eligibility.first_refused  core   {action_id, turn_id, start_command_id,
//	                                  stage, code}
//
// Known argument members belonging to another operation are forbidden even
// when null. expected_operation_id is absent when hold/require_first opens a
// new operation from eligible, and required when changing an existing hold:
// hold from first_action_required, require_first from ordinary_work_ineligible,
// first_next, release or first_refused. Version 0 denotes implicit eligibility;
// later expectations are positive, including eligible after release.
// Only the trusted owner Service seam sets/requires/advances/releases; there
// is no Agent setter, role-derived permission, grant class or model tool.
// A later Core-originated hold (ticket 131) requires an additive operation/actor
// at that ticket's own freeze. Ticket 90 pre-authorizes no such transition.
//
// Every operation is exactly [ledger.command, runtime.eligibility_changed,
// runtime.eligibility_receipt], in one SubmitAt at the preflighted tail, with
// three moved-tail attempts then BusyError{ledger_tail}. Command/receipt actors
// match the submission; change actor is core. All envelopes have the target
// agent's registered sole session and general-context origin, no turn_id, and
// operation_id equal to THIS command ID. Payload operation_id instead names
// the held operation. No mixed, extra, missing or reordered records are allowed.
// effect_sequence is positive and names the actual change after authoritative
// readback. Receipt decoding re-encodes schema 8 and verifies submission_sha256.
// The snapshot derives from the normalized command and preceding state, never
// caller-supplied state. Prepare/decode/plan/stage/install is atomic before the
// projection frontier advances; an invalid group installs nothing. The generic
// Reader/EncodeCommit/ledgerstore do not enforce these producer history rules.
//
// Global command lookup extends schemas 1-7 to 1-8. A matching same-ID canonical
// request returns its original receipt before CAS, after current disclosure
// authorization; changed meaning is ErrCommandMismatch across all families.
// A distinct no-change request is ErrNoEffect without a record. Version
// disagreement is RevisionError; wrong operation/action identity or a
// non-executable edge is ErrEligibilityStale; malformed shape is ErrInvalid.
// Persistence uncertainty returns the real persistence failure.
//
// # Eligibility transitions, first identity and recovery
//
// Registration implies version 0, eligible, reason none without an initialization
// record. Every recorded change increments eligibility version exactly once.
// Hold from eligible opens operation_id equal to its command ID, writes
// ordinary_work_ineligible and clears any historical declaration. Require-first
// from eligible likewise opens an operation, writing first_action_required.
// Require-first from an ordinary hold without a declaration retains the hold
// operation. Hold from first_action_required retains that operation and the
// declaration/refusal as history, writes ordinary_work_ineligible with the
// supplied non-none reason, and withdraws the permitted turn's exception.
// Release compares both current version and operation ID, writes eligible with
// reason none, and retains the released operation/declaration as history only.
// A later hold gets a new operation: a late release cannot release that hold.
// No received task, message, verdict, natural ending or settlement changes
// eligibility. Release means only this generic gate is released, not that any
// alignment, grant, budget, activation, service restoration or promotion passed.
//
// first_action is required in first_action_required and absent on a new ordinary
// hold. Its initial_turn is immutable within action_id; permitted_turn initially
// equals it. Require-first/next validate a registered same-agent context and
// exact task/assignment when present, with no existing work under the declared
// turn/command identities. General context has no task binding; work context
// requires one. Start/admission command IDs are distinct and unused.
//
// first_next is valid only in first_action_required for the same action_id,
// after the permitted turn's native terminal ending and, when bound, its F3-15
// ending/F3-16 terminal; alternatively a recorded turn_admission refusal with
// no native work ends that attempt. It keeps ContextID and TaskBinding, chooses
// fresh turn/start/admission IDs and input digest, preserves initial declaration,
// first-admitted identity and hold operation, and clears the previous refusal.
// It never runs automatically and never releases ordinary work.
//
// input_sha256 hashes the compact owner request in this order: schema (1),
// origin {agent_id,session_id,context_id}, command_id (StartCommandID), turn_id,
// text, artifacts, task_binding?. Nil artifacts normalize only to []; supplied
// order and text are preserved. This is not the stored owner_input object,
// actual rendered prompt or a permission. The live start verifies this digest
// before admission and on same-ID retry. The loop.bind planner in preflight
// and replay checks ledger identity only: while the agent's projection at that
// prefix is first_action_required, a bind of the declared TurnID must carry the
// declaration's AdmissionCommandID and origin/context, and a bind of any other
// turn is held. No planner or Open reads a private object for this digest.
//
// first_refused is Core's observation, not permission: stage is CLOSED to
// turn_admission, before any native admission or loop binding, for the current
// exact declaration. It increments version but keeps state, reason and all
// hold/action/turn identities. Only r.start writes it for a definite refusal,
// with code denied, unavailable, invalid_argument, too_large or integrity.
// Busy and ledger_tail write no refusal; raw Agent.StartTurn writes none.
// Duplicate identical refusal writes nothing. Native admission supersedes it;
// after binding, F3-16/F3-13 alone describe waits, stops and endings, with no
// paid_admission refusal stage or observer. A release/next/admission that wins
// first makes an old refusal observation stale and it writes nothing.
//
// Native admission consumes the declared identity. A same-ID unadmitted request
// may be explicitly retried under ordinary admission rules; an admitted one is
// never re-admitted on retransmission/restart. First-action dispositions derive
// from journals: not_admitted, admission_refused, admitted_unbound, bound,
// terminal or unsupported. First-admitted identity remains the first actual
// native admission even across first_next. Open reconstructs state and identities
// without writes, starts, paid admissions or enqueues. Explicit existing
// reconciliation only ends/reconciles work; it cannot release eligibility.
//
// # Additive eligibility consumption
//
// ONLY where an F3-90 record exists for the agent at the historical prefix,
// these commands by or for it require eligible or its exact permitted first turn
// in BOTH producer preflight and replay: F3-11 turn.start (exact origin, TurnID,
// AdmissionCommandID and TaskBinding), task.start (assignee agent, any actor),
// local.admit, local.begin and atomic effect commands, with or without a promised
// turn; F3-13 paid.admit and the paid.dispatch intent; F3-15 tool.begin. Under
// the first-action exception local/paid/tool/effect dispatch additionally needs
// the permitted turn's loop.bind in the current projection. A raw native
// admission dispatches nothing. Ordinary grants, integrity, capacity and
// BudgetBook admission remain necessary; declaration reserves nothing.
//
// Always allowed regardless of eligibility: every ending, result, settlement,
// observe/release/correct, turn.end, turn.cancel, loop.end, every F3-16 owner
// transition, task create/claim/assign/reassign/wait/resume/submit/complete/fail/
// cancel, reads/inspection under ordinary grants, registration, grant and
// credential administration. A dispatch/effect committed before a hold is
// already in flight and may finish; a hold committed first prevents a new one.
// A hold itself cancels nothing, extends no deadline, clears no reservation or
// unknown exposure and retries nothing. Held is not busy, a park or a stop.
// Agents without F3-90 records and older logs replay unchanged.
//
// testdata/f3/90 is generated only by TestGoldenF3_90 -update with fixed synthetic
// identities/times and encoded hashes. It covers all five operations, both
// require-first edges, refusal, a normally granted F3-11/F3-15/F3-16 first turn,
// explicit next after terminal, hold from first-required, release and inert
// reconstruction. Wrong-operation refusal leaves the prefix unchanged.
// These codec/group fixtures are not runtime dispatch, recovery or money proof.
// No Core/Session API, scheduler policy or private object schema is frozen here.
//
// # F3-91 service loss and restoration (frozen, D175)
//
// F3-91 adds five version-1 types to Defined, without changing any earlier
// family: runtime.service_observed (E), runtime.service_cause_changed (K),
// runtime.service_agent_changed (M), runtime.service_outage_notice (N), and
// runtime.service_receipt (R). ServiceSubmission uses schema 9, following
// F3-16's schema 7 and F3-90's schema 8. The five types and schema 9 are
// frozen (D175), before any Core producer imports them.
//
// A source group is exactly [ledger.command, E, R]. A state group is
// [ledger.command, K?, M?, N?, R], in that order, with at least one effect,
// at most one of each, and N only with M. No other family's effect shares
// either group. Every envelope has actor core, operation_id = command_id,
// and no agent, execution-session, context, turn, body_refs or measurement
// path. E and M carry agent_id in their payloads; K, N and R are colony-scoped.
// This is not an exception to an earlier family's origin rule. The authoritative
// consumer stages these groups separately from private traversals and routes
// views by their payloads.
//
// Each payload and submission independently caps both complete input and
// compact canonical output at 65,536 bytes, including the embedded submission
// in R. UUIDs are canonical lowercase UUIDv7, digests are nonzero lowercase
// SHA-256, and naturals are plain JSON integers through MaxInt64, not exponent,
// fraction, negative zero or overflow. All known nulls and recursive duplicate
// keys (including escaped aliases and unknown extensions) are rejected.
// Unknown optional keys are ignored and excluded from canonical preimages.
// Inapplicable members are omitted, never null, empty or zero placeholders.
// Route region is the sole permitted empty string.
//
// Open tokens match [a-z][a-z0-9_]*, at most 64 ASCII bytes. Unknown cause,
// scope, source, reason, provenance, condition, phase, permit, terminal,
// coverage, edge or outcome tokens are retained as unsupported. They never
// mean healthy, lost, restored, authority, permission, or release of money.
// Schema, actor, operations and the reused PaidScope/period/mode grammar
// remain closed. Historical decoding has no runtime producer-count cap.
//
// Canonical CauseSpec key order:
//
//	family, code, scope_kind, provider?, credential_scope_id?,
//	credential_version?, model?, region?, authority_sha256?, agent_id?,
//	context_id?, private_scope?, revision?
//
// CauseKey is SHA-256 of this compact known-field encoding. Families are
// provider, money and execution. Known code/scope/field combinations are:
//
//	credential_denied: provider / credential; provider, credential_scope_id,
//	  credential_version.
//	scoped_denied: provider / account_route; provider, credential_scope_id,
//	  credential_version, model, region, authority_sha256.
//	quota_exhausted: provider / account; provider, credential_scope_id,
//	  authority_sha256, with no model, region or credential_version.
//	rate_limited: provider / account_route; provider, credential_scope_id,
//	  model, region, authority_sha256, without credential_version.
//	overloaded: provider / provider_route; provider, model, region,
//	  authority_sha256.
//	unreachable: provider / endpoint; provider, authority_sha256 only.
//	owner_money_exhausted: money / colony (no scope IDs), agent (agent_id),
//	  or credential (provider, credential_scope_id, without version).
//	private_integrity: execution / private; agent_id, private_scope,
//	  revision, and context_id exactly when private_scope is context.
//	process_unconfirmed: execution / colony, the workspace Git Owner.
//
// Unlisted fields are absent for those known combinations. Model and region
// use the exact sealed nonsecret F3-13 grammar; region is present even when
// empty for route scopes. authority_sha256 hashes the sealed authority, never
// a credential value, locator, account name, header, path or raw error.
// E reserves the immutable CauseKey-to-CauseID association before K exists.
// The same key cannot acquire a second ID; an ID cannot acquire a new spec.
//
// The provider writer diagnosis table is exhaustive and evidence-bounded:
// 401 denied is immediate credential_denied; 403 denied is immediate
// scoped_denied. Anthropic 402/enforced_spend_limit_reached/usage-limit
// exhaustion and OpenAI insufficient_quota or the supported spend/usage
// exhaustion codes yield quota_exhausted, immediate unless one validated
// stated reset exists. Rate-limited and overloaded classifier results use
// grace. Unreachable uses grace only for transport.Do or read net.Error with
// an uncancelled exchange context; transport timeout and unreachable map to
// code unreachable. HTTP-504 timeout, provider_error, invalid_request,
// unavailable, conflict, protocol_error, refused, tariff_ambiguous, caller
// cancellation, local deadline and capture refusal establish nothing and
// write no E. Vendor fields cannot widen the scope. A reset is one unambiguous
// retry-after delta or HTTP date, future at observation; merely passing it
// never clears a cause.
//
// Source key order:
//
//	kind, observation_id, observed_through, source_sequence?, command_id?,
//	submission_sha256?, origin?, turn_id?, attempt_id?, object?,
//	observed_utc_ns, clock_held, reason, provenance?, reset_utc_ns?, money?,
//	completion?, workset_sha256?, failed_work_count?, execution_run_id?,
//	execution_proof?
//
// Known kinds are paid_call, money_refusal, integrity_refusal, process_refusal,
// cause_clear, successful_call, grace_expiry and demand_withdrawn. Reason writer
// values are rate_limited, overloaded, unreachable, credential_denied,
// scoped_denied, quota_exhausted, money_exhausted, private_integrity,
// process_unconfirmed, provider_completed, budget_available, integrity_verified,
// process_closed, all_work_moved and grace_due. owner_cancelled is not a writer
// value. Withdrawal is only a pre-due clearance of a pending generation.
//
// observed_utc_ns is positive and clock_held is a required Boolean. The live
// host uses the existing review-clock floor, holding a backwards sample at the
// maximum of review floor, review sample and last accepted ledger event time,
// setting clock_held without raising reviewSample. Paid dispatch's clock rule
// is unchanged. observed_through is a complete validated prior prefix.
// source_sequence selects an actual prior effect, never a predicted cursor.
// Paid sources require full Origin, turn_id, attempt_id, source_sequence
// selecting the dispatch intent, and the original context-owned ObjectRef.
// Origin, turn, request and attempt must agree with the native records.
// Their provenance is host-fixed provider_response or transport. Refusals
// without a native effect instead require command_id, submission_sha256,
// Origin and turn_id. A supplied command/digest is always a pair.
// reset_utc_ns is future and only on rate/quota evidence or UTC-period money
// refusal. Workset digest/count are paired moved-work evidence; the count is
// zero for restoration. execution_run_id is copied only when the actual report
// supplies a RunID, never fabricated. execution_proof is present exactly for
// process_closed and is live, retained_job or name_absent.
//
// Money occurs exactly on money_refusal or reason budget_available, in order:
//
//	scope, period, spend_sequence, mode, occupied_micros, ceiling_micros?,
//	coverage, workset_sha256?, eligible_work_count?, failed_work_count?
//
// Scope/period/mode reuse F3-13. Only colony, agent and credential scopes
// qualify. Amounts are canonical nonnegative decimal strings: occupied may
// exceed MaxInt64 up to the payload cap; ceiling may not. Finite requires a
// ceiling and unlimited forbids it. Colony/agent use mandatory_scope without
// workset fields; credential uses complete_workset with digest and both counts.
// Refusal requires finite occupied >= ceiling, including unknown holds and
// zero ceilings, and complete_workset requires eligible = failed > 0.
// Quote-too-large alone, task/grant exposure, authority denial and paid_violation
// do not establish money loss. Lifetime has no reset; UTC day/month uses the
// next boundary. Credential coverage needs the enabled exhaustive loop registry.
// budget_available binds a real book/configuration/native proof; it neither
// charges nor releases money.
//
// Completion occurs exactly on successful_call, in order:
//
//	provider, terminal, native_result_sequence?, financial_kind?, permit?
//
// The financial members occur together only if the native result already
// exists, never predict one, and authorize no financial change. Usage and
// financially unknown may both qualify. The successful terminal predicate
// is the existing paid derivation gate: complete HTTP 200, valid provider
// message ID, the requested model, and no content/capture error or substituted
// capture. Anthropic accepts end_turn, max_tokens, stop_sequence, tool_use and
// model_context_window_exceeded; OpenAI accepts completed. Refusal, failed,
// incomplete, partial/cancelled execution, bare usage and correction do not
// qualify. Tool-use completion is not authority for the later tool.
//
// Before E, publish a separate immutable service_completion object under the
// original owning-context private object layout, bounded by the existing 1 MiB
// object cap. Its exact order is schema, kind, observation_id, through_sequence,
// workset_sha256?, permits; schema is 1 and kind is service_completion.
// Permits are sorted/distinct by CauseID, with entry order cause_id,
// cause_version, member_generation, kind, proof_sequence?, failed_work_count?.
// Kinds are same_scope, cleared, credential_replaced, all_work_moved,
// execution_clear and blocked. Same_scope/blocked omit proof/count;
// cleared/credential_replaced/execution_clear require an actual prior proof
// sequence. All_work_moved requires failed_work_count=0 and the recorded
// exhaustive workset digest. Neither a caller Boolean nor one switched turn
// proves that all configured and durable nonterminal work left a failed scope.
//
// Canonical record key orders:
//
//	E: observation_id, agent_id, cause_id?, cause_key?, cause?, source,
//	   completion_permits_object?
//	K: cause_id, cause_key, expected_version, version, cause, condition, source
//	M: agent_id, cause_id, expected_version, version, generation, phase,
//	   first_source, due_utc_ns?, outage_id?, source
//	N: outage_id, cause_id, edge, notice_id, member_source_sequence
//	R: command_id, submission_sha256, submission, outcome, effects,
//	   source_through
//	effects entry: type, sequence
//
// Per-record presence is fixed. Failure E requires cause_id/key/spec and
// forbids completion_permits_object. Successful-call E forbids all cause
// fields, requires completion_permits_object and omits completion.permit:
// one E may satisfy several causes. Deterministic clear/withdraw E names its
// existing cause by cause_id alone, forbidding cause_key, cause and permits
// object. E accepts the seven non-timer known source kinds; grace_expiry
// occurs only in service.expire and its state records, never E/service.record.
// In every successful-call state source (service.complete or service.clear,
// including K/M), completion.permit is required and selects that cause's
// captured entry. Codec presence checks do not replace the consumer's check
// that the selected entry exactly matches E's object. The selected permit
// names the cause_id of its submission, K or M; its cause_version equals the
// submission's expected_cause_version, and its member_generation equals a
// member submission's expected_generation and M's generation.
// E's observation_id is its group's command ID and its source's
// observation_id; a grace_expiry source's observation_id names no E. A
// source's Origin, when present, names the agent_id of the E, M or member
// submission carrying it, and observe's first_source and source name one
// observation. source_sequence, spend_sequence, native_result_sequence and
// proof_sequence are positive and at most the source's observed_through.
//
// K condition is active or cleared. Creation is expected_version=0,
// version=1, active; real later changes increment exactly once. M phase is
// pending, lost, restored or withdrawn. Generation begins at 1 and advances
// only for a new demand after restoration/withdrawal. first_source is the
// immutable first refusal within that generation. Grace due is checked
// first_source.observed_utc_ns + 120,000,000,000; no refreshed onset on reopen.
// Due is present exactly for grace-bearing generations, including history.
// The first refusal's reason decides: rate_limited, overloaded and unreachable
// always bear grace; quota_exhausted and money_exhausted exactly when that
// refusal carries reset_utc_ns; credential_denied, scoped_denied,
// private_integrity and process_unconfirmed never; any other reason fixes no
// presence. Pending and withdrawn snapshots always carry due.
// outage_id is absent while pending/withdrawn and retained on lost/restored.
// A fresh live timer sample must reach due even when clock_held; pure replay
// validates recorded arithmetic, never samples time, arms a timer or appends.
//
// N edge is began or ended. member_source_sequence selects the actual M
// envelope in that same commit, not a payload self-sequence. There is one open
// incident per cause: the first lost member atomically emits began; later
// demanded members join its cumulative affected set without another begin;
// the last restoration emits ended and fixes the final set. Neither a
// membership list nor a delivery-ack store is serialized in N.
// R outcome is recorded for new transitions only. Effects exactly name the
// present envelopes in planned-tail order; no exact-no-op group is written.
// A clear's effects are exactly K; every other state operation's include M.
// source_through is below its group's command sequence.
//
// Canonical schema-9 submission order:
//
//	schema, actor, operation, cause_id?, agent_id?, expected_cause_version?,
//	expected_member_version?, expected_generation?, arguments
//	record arguments: observation_id, cause_key?, cause?, source,
//	  completion_permits_object?
//	state arguments: cause_key, cause, source, first_source?, due_utc_ns?,
//	  outage_id?, notice_id?
//
// Actor is host-fixed core. The six operations are closed. service.record
// requires agent_id, applies E's cause/source presence, and omits all three
// expected_* fields. service.clear requires cause_id and expected_cause_version,
// forbids agent_id, and omits expected_member_version and expected_generation.
// observe/expire/complete/withdraw require cause_id, agent_id and all three
// expectations. An applicable zero means absence; an inapplicable expectation
// is omitted, not zero. Only observe admits a zero: cause version zero for a
// new cause, and member version and generation zero together for a new member;
// clear, expire, complete and withdraw name existing nonzero versions.
// State cause_key/spec are required and must match.
// Observe supplies a failure source and first_source, due exactly for grace,
// and an outage for immediate loss; only the first lost member adds notice_id.
// Expire/complete/withdraw omit first_source/due and reuse the stored witness.
// Expire uses grace_expiry; complete uses successful_call; clear uses
// cause_clear or successful_call; withdraw uses demand_withdrawn. Clear and
// withdraw cannot carry an incident edge. Expire/complete name the outage and
// carry notice_id only for the computed first/last member edge. The planner,
// not caller-supplied state, determines K/M/N and checks prefix CAS.
// submission_sha256 hashes compact known-field schema 9. Receipt matching
// precedes fresh clock/CAS checks; changed content under a command identity
// remains a cross-schema command mismatch.
//
// Clear never means restored. Provider clearance is an exact-scope qualifying
// completion (K with the member restoration) or positive credential replacement:
// owner rebind at the same scope retires/revokes the refused version and a
// successful call uses the recorded replacement. Money clearance is a real
// budget_available book recheck at expiry or succeeding-call admission.
// Integrity clearance is revision-bound integrity_verified from the agents
// Open report at openedThrough, written by explicit ReconcileServiceEvidence,
// or a live entrance pass. Process clearance is process_closed from positive
// workspace ProcessClosures or a successful workspace operation. Open writes
// none of these. Reset/rebind/raise/top-up/repair/process closure only permit a
// call; restoration still needs an actual normal agent-originated successful
// call observed after this membership's onset and a current scope-clear or
// ceased-applying witness. Unknown held financial exposure remains unchanged.
//
// Paid ordering is E -> unchanged F3-13 financial result -> source drain
// K/M/N -> acknowledgement. Publish service_completion outside Service.mu
// under the context gate before E. E and the financial group are submitted
// under the existing service hold; the drain uses its own cycles and loads
// selected service metadata outside that hold. source_through covers only
// complete E commits whose applicable actions all committed, not financial
// traffic. A multi-cause completion keeps coverage before E until the final
// per-cause action; partial recovery matches prior receipts rather than
// repeating effects. A definite service failure after financial commit is a
// retained service-recovery condition visible in status and retried by the
// next drain, never a financial error or rewrite. Uncertain appends poison as
// before. Recovery consumes recorded E, never resends a paid request, reopens
// a paid object to reinterpret it, or infers success from later configuration.
//
// Service.changed belongs only to the session bound to M.agent_id. Every
// bound session surfaces N as outage.began/outage.ended and lost-entering M
// on an open incident as outage.joined. The terminal deduplicates NoticeID
// and (OutageID, AgentID, generation), listing the complete affected set at one
// through-sequence, including late joiners. Passive queries append nothing;
// the trusted host snapshot is not an Agent-handle capability.
// F3-91 changes only its projection/directory and wakes views: it gates no
// admission, cancels/retries nothing, transfers no authority and releases no
// reservation. Eligibility, grants, tasks, holders, continuations and the
// original financial groups remain unchanged.
//
// testdata/f3/91 is generated only by TestGoldenF3_91 -update. Its earlier
// native families remain service-admissible, including calls after service
// records; an isolated real Core Open proof removes only F3-91 groups.
// It covers grace/immediate causes, three demanded incident members, a late
// join's cumulative listing, clear without restoration, per-member completion
// and exactly one begin/end per incident. Negative group rows cover duplicate
// identity, source, actor, origin, order and planned-tail notice binding.
// Codec fixtures and FuzzDecodeF3_91 are contract proof, not runtime timer,
// dispatch, recovery, permission, financial or terminal proof.
//
// # Owner setup records (F3-150)
//
// Ticket 150's two version-1 types and schema-12 canonical submission are
// frozen (D187). This addition changes no earlier family, schema,
// group, golden, authorization or format version. Schema 12 is independently
// reserved; it need not follow the largest schema already present.
//
// Payload owner: C3. The sole producer and reader is internal/core/setup,
// through ledgerstore.Submit/SubmitAt and a ledger.Reader. Agents and workspace
// skip this family; it gates, cancels and authorizes no other family's command.
// All known fields, group envelopes, submission and receipt are replay-essential
// owner evidence or configuration, never permission. Private instructions,
// transcripts, secrets and the colony directory are not stored in this family.
//
// Codec rules follow F3-139: one complete UTF-8 JSON object, no repeated member
// at any depth (including ignored extensions), no known null, canonical
// lowercase UUIDv7 and nonzero lowercase SHA-256. Unknown optional members are
// ignored at every depth and omitted from encoding and digest preimages.
// MaxSetupPayloadBytes is 65536 for each complete payload and submission:
// check input bytes before parsing, including whitespace and ignored members,
// and independently check compact output bytes. Errors wrap ErrInvalid.
// Integers are plain unsigned JSON decimals through MaxInt64, never signed,
// fractional, exponential or quoted. expected_revision is below MaxInt64.
// Optional members are omitted, never null; all other listed members are
// required. A known argument of another operation is forbidden, even when null.
//
// Exact known-field encoding order:
//
//	SetupSubmission: schema (12), actor ("owner"), operation, target,
//	  expected_revision, arguments
//	SetupTarget: kind, id, agent_id?
//	  kind is colony, workspace or context; agent_id is present exactly
//	  for context and names its owning agent.
//	colony.create / colony / expected_revision 0:
//	  arguments {}
//	workspace.register / workspace / expected_revision 0:
//	  arguments {capture_inputs_sha256}
//	workspace.inputs / workspace / expected_revision at least 1:
//	  arguments {capture_inputs_sha256}
//	context.set / context:
//	  arguments {display_title, task_binding?, route_id,
//	    expected_route_version, credential_scope_id, max_output_tokens,
//	    turn_policy}
//	startup.set / colony:
//	  arguments {settings_sha256, settings_bytes}
//	checks.publish / colony:
//	  arguments {body_set_sha256, body_count}
//	turn_policy: max_model_calls, max_tool_calls, timeout_ms,
//	  max_argument_bytes, max_result_bytes, max_discovery_entries,
//	  max_search_entries, max_search_matches, max_live_bytes
//	task_binding: the frozen TaskBinding's task_id, assignment_id, generation
//	runtime.setup_applied: submission
//	runtime.setup_receipt: command_id, submission_sha256, outcome, revision
//
// The six operations, actor and target kinds are closed. display_title is
// 1-128 UTF-8 bytes without control characters. Route version, output ceiling,
// all nine turn-policy members, settings_bytes, body_count and task generation
// are positive. The codecs check shape, not current turn-policy maxima,
// existence, currentness, native context kind or cross-family relationships.
// Outcome is open: ^[a-z][a-z0-9_]*$, at most 64 ASCII bytes. Writers emit
// applied; the service refuses unfamiliar outcomes rather than applying them.
// Receipt revision equals expected_revision plus one, checked by the service
// against the group's submission, not by a standalone receipt codec.
//
// submission_sha256 is SHA-256 of compact SetupSubmission.Encode known-field
// bytes, with encoding/json ordering/escaping (including HTML and U+2028/U+2029).
// It equals the ledger.command digest. Command ID, time, sequence and resulting
// revision are outside the preimage. body_set_sha256 hashes the published body
// digests as lowercase hex in strictly ascending order, each followed by one LF.
// startup.set identifies the exact owner-approved host-settings document by
// digest and byte size, not a frozen settings member list or ticket-17 type.
//
// Service group and state rules (not codec history lookups)
//
// Each transition is one complete commit, with no extra or reordered records:
//
//	ledger.command, runtime.setup_applied, ledger.body_pin?,
//	runtime.setup_receipt
//
// The pin is present exactly for startup.set and checks.publish. Every envelope
// has actor owner and operation_id equal to command_id; agent, session, context,
// turn and measurement paths are absent. body_refs occur only on setup_applied
// and the pin, identical on both: [settings_sha256] for startup.set, the strictly
// ascending published set of body_count digests for checks.publish. All records
// of the other four operations have no body_refs. Pins are permanent and
// additive. The command and receipt command IDs and digests agree; the service
// recomputes the submission digest and checks the resulting revision.
//
// colony.create is the log's first commit, at most once, targets the envelope
// colony and follows C1's native colony.json publication. Every other setup
// group requires it. workspace.register and workspace.inputs follow their
// unchanged native F2-05 commits and attest the native capture-input digest
// and revision. Other setup groups configure targets that already exist.
// Revision counters are per target and class: register/inputs share the native
// F2-05 workspace revision; context.set has one counter per context; startup.set
// and checks.publish each have their own counter per colony.
//
// Command deduplication precedes revision checks and any native call. An exact
// duplicate returns the original receipt even after later target revisions;
// changed meaning under the same command ID refuses. Each native and setup
// step has its own commit and command identity where the native family provides
// one. A crash between steps leaves the earlier step done and the later not
// done; explicit re-apply completes the later step, never automatic replay.
//
// Native agent registration precedes context.set for its General context;
// native work-context creation precedes its context.set. A native context
// without setup configuration is not enabled. Enabled-context consumers fold
// context.set in ledger order. Preflight requires the native owning agent and
// context, General's fixed title and absent task binding, a work context's task
// binding, and current policy bounds. Busy/recovery-pending contexts refuse.
// Once a context has run a turn, only title, accepted route version, output
// ceiling and turn limits may change; route, credential scope, task binding,
// provider and model stay fixed. Missing cross-family references are checked
// at preflight where required and by doctor, not by replay; route, credential
// scope and task IDs may be forward references and confer no authority.
//
// TestGoldenF3_150 -update alone writes testdata/f3/150. Its one history starts
// with colony.create, then native workspace registration/update and their setup
// receipts, native agent/work-context creation and their configurations, a
// second work-context configuration at revision 2, startup.set and checks.publish
// with retained bodies and pins. The exact body-set preimage is beside the log.
// Earlier-family groups remain service-admissible; the real Core Open probe
// verifies that agents skips setup groups without changing its earlier rules.
//
// # F7-30 launcher bindings (frozen, D188)
//
// C7 owns launcher.command_bound v1 and launcher.command_receipt v1.
// internal/core/agents is the designated producer through the existing
// serialized SubmitAt path. Core replay, command lookup and authenticated
// original-request status consume them. These are immutable operational
// bindings retained for the log's lifetime, not grants, admissions, queues,
// shutdown intents or execution results. No text, credential, private object
// bytes, input_object, host_id, body_refs or measurement_path is recorded.
// All known members and envelope bindings are replay-essential.
//
// Each complete payload and canonical submission is bounded independently to
// 65,536 bytes, including ignored extensions and embedded submissions. Existing
// ledger JSON, recursive duplicate refusal, canonical UUIDv7, nonzero digest,
// unsigned decimal integer through MaxInt64 and absent-not-null rules apply.
// Unknown optional members are ignored and excluded from canonicalization;
// known members forbidden for a variant are refused even when null.
//
// Exact compact member order (? means absent by omission):
//
//	launcher.command_bound:
//	  request_id, principal, origin, operation, turn_id?, task_binding?,
//	  target, request_sha256, native_admission_id?
//	launcher.command_receipt:
//	  command_id, submission_sha256, submission, outcome, effect_sequence
//	principal (closed variants):
//	  {kind:"windows_logon", user_sid, authentication_id}
//	  {kind:"unix_uid", uid}
//	target:
//	  kind, id, revision
//
// Principal is captured from the native peer, never a client identity claim.
// user_sid is a canonical SID; authentication_id is the LUID's high then low
// 32-bit words, encoded as 16 lowercase hexadecimal digits. uid is an unsigned
// 32-bit integer, including zero. Stable identity compares kind and user_sid
// or uid, never authentication_id.
// Origin is the existing {agent_id,session_id,context_id}; colony identity is
// supplied by the envelope. task_binding is the existing
// {task_id,assignment_id,generation}, resolved by Core.
//
// Operation is CLOSED to message.send, turn.cancel and promotion.request.
// turn_id is present exactly for message.send and turn.cancel.
// native_admission_id is present exactly for message.send. task_binding is
// present only for message.send in a work context, and must equal its current
// assignment; it is absent for a general context and other operations.
// Target kind is CLOSED to context and continuation, revision is nonnegative.
// message.send and promotion.request target the origin context;
// turn.cancel targets its turn's continuation owner version.
//
// LauncherSubmissionSchema is 11, independently reserved for this family.
// Canonical LauncherSubmission members are schema, actor, origin, operation,
// request_id, request_sha256, arguments. Actor is CLOSED to owner and operation
// to launcher.bind. Arguments are the bound record's members after request_id
// in their exact order; top-level origin and request_sha256 equal the nested
// members. SHA-256 of Encode's compact JSON is submission_sha256.
//
// Exactly one group per request_id is [ledger.command,
// launcher.command_bound, launcher.command_receipt]. All three envelopes have
// actor owner, the registered full origin, no turn_id, and operation_id equal
// to the fresh Core-generated group command ID. This ID, request_id and the
// optional native_admission_id are distinct and checked against consumed and
// promised command IDs. Receipt outcome is CLOSED to recorded;
// effect_sequence names the group's second record. Submission, command,
// effect and receipt identities/digests must agree. A producer submits at its
// preflighted tail, with three moved-tail attempts then BusyError{ledger_tail}.
// Generic Reader/EncodeCommit/ledgerstore do not enforce producer history rules.
//
// # F7-30 request meaning and promised native identities
//
// LauncherMeaningVersion is 1 and numbers this frozen preimage, not any
// negotiated launcher wire. Members are version, origin, task_binding?,
// command_id, operation, expected, arguments. expected uses LauncherTarget.
// The three frozen argument shapes, in exact order, are:
//
//	message.send       {turn_id,text,artifacts}
//	turn.cancel        {turn_id}
//	promotion.request  {workspace_id,head,restart_from?}
//
// Artifact items are {source_sequence,sha256,bytes}, with positive integers
// and nonzero digests. Omitted artifacts normalize to []; nothing else does.
// Preserve text, array order and restart_from exactly. The meaning digest is
// SHA-256 of compact LauncherMeaning.Encode JSON. It excludes the principal,
// every connection/RPC identity, transport ordinal, title and focus value.
// The preimage is hashed, never stored, and not subject to the 65,536-byte
// payload/submission cap; the producer's input limits bound it.
// Same-ID retry compares this digest and stable principal identity BEFORE
// revision evaluation, with current disclosure authorization still required.
// A changed bound operation meaning needs a new record version by addition;
// no frozen type depends on contract/launcher or its draft version.
//
// Where a launcher.command_bound exists, its request_id may be consumed only
// by loop.bind (message.send) or owner.stop (turn.cancel) with the bound
// origin/turn, and its native_admission_id only by that message's turn.start,
// in producer preflight and replay. Logs without F7-30 replay unchanged;
// native submission bytes, receipts and ticket 90's declared identity are
// untouched. Global command lookup extends by schema 11, not a contiguous
// schema-number assumption or a second UUID database.
//
// A bound request with authoritative proof of no native admission may be
// explicitly retried with identical meaning under current guards. Busy, held,
// stale and unavailable leave it retryable; startup never retries it.
// An admitted or uncertain operation is never re-executed. Status is derived
// from actual native witnesses; this family adds no command_result or resolve.
// host.shutdown has no F7 record and no colony-only origin variant.
//
// testdata/f7/30 contains one log generated by TestLauncherBindGolden -update:
// a message binding, its native turn.start and loop.bind, then a cancellation
// binding and its owner.stop, on the service-ordered F3-16 prefix. The prefix's
// private object bytes remain those of testdata/f3/16, not new F7 objects.
//
// # F4-17: required validation checks
//
// F4-17 is proposed for freeze (D125). C4 owns these eleven version-1
// validation.* types. Core agents is their only producer/appender caller.
// Core validation, promotion, check governance and authorized inspection
// consume the records; no payload is a promotion acceptance, permission grant,
// isolation claim, field event or instruction to retry an execution.
//
// Configuration objects are closed UTF-8 JSON, with duplicate keys, null,
// unknown members, trailing values and malformed integers refused. Unsupported
// format/schema/adapter pairs return ErrUnsupportedValidation, distinct from
// ErrInvalid. WorkspaceChecks and EnvironmentPolicy are at most MaxRecordBytes.
// Their loaders hash exact accepted bytes, not a normalized encoding. Encoders
// emit compact json.Marshal bytes in the declared struct order, with HTML
// escaping, omitted optional members and no map-based hashing or RFC 8785.
//
// WorkspaceChecks order is format, schema, workspace_id, revision, required,
// checks, hooks. Format is dwarf-harness/workspace-checks, schema is 1.
// Required is nonempty, distinct and in execution order. Checks is strictly
// check_id-sorted and has exactly that set: no waiver, exclusion or filtered
// gate can be represented. Hooks is hook_id-sorted; each hook contains hook_id,
// event, with event exactly observation.committed.
//
// CheckSpec order is check_id, adapter, adapter_version, executable_id,
// package, working_directory, defining_files, inventory, canary,
// environment_id, network_grant_refs, secret_grant_refs, wall_ms, output_bytes,
// processes. Dispatch first reads the common adapter identity, then that
// adapter's closed member set. Only go-test-json version 1 is defined here.
// Package is "." or one safe "./dir", never an expansion or flag template.
// Working_directory is "." or a safe subject-relative directory.
// Network/secret references are empty in v1. Limits are positive, finite int64
// values; a producer also checks its explicitly supplied capacity, with no
// zero-as-unlimited or default-enabled runner.
//
// File order is path, mode, bytes, sha256. Paths are safe relative paths,
// strictly raw-byte sorted, distinct and without case collisions; modes are
// 100644 or 100755 and byte counts are nonnegative. A SHA-256 binds exact raw
// bytes, never a Git object identifier. Inventory order is package, name,
// parent?, expected. Nodes are nonempty and strictly (package,name)-sorted;
// parent links resolve within the same package inventory; expected is passed.
// Canary order is protocol, package_name, file, test_name, template_kind,
// template_sha256. Protocol is go-fail-v1 and template_kind testing-error-v1.
// File is dwarf_canary_v1_test.go and test_name is TestDwarfCanaryV1.
// Package_name is an ASCII identifier of at most 64 bytes other than "_".
// No defining-file basename may ASCII-case-fold to a dwarf_canary_ prefix;
// no inventory name's first slash component may begin TestDwarfCanary.
// Unknown protocol/template kind is unsupported; misspellings/collisions are
// invalid. The canary never replaces or waives a required node.
//
// EnvironmentPolicy order is format, schema, workspace_id, revision,
// environments; format is dwarf-harness/environment-policy, schema 1.
// Environments is nonempty and environment_id-sorted. EnvironmentSpec order
// is environment_id, os, arch, executable_id, executable_path,
// executable_sha256, toolchain_version, toolchain_root,
// toolchain_manifest_sha256, dependency_manifest_sha256, lock_files,
// variables, gomaxprocs, memory_reservation_bytes. Absolute executable and
// toolchain paths are declarations, not resolved by these codecs. Lock files
// bind go.mod and any present go.sum. Availability, integrity and complete
// environment assembly belong to the runner; no implicit download is allowed.
// Variable order is name followed by exactly one of value or run_path.
// Names are ASCII identifiers of 1..64 bytes, strictly raw-name-sorted and
// distinct under ASCII case folding. Values contain 1..1024 UTF-8 bytes and
// no byte below 0x20 or equal to 0x7f. Run_path is home, temp, gocache,
// gopath, gomodcache or gotmp. Variables are public policy, never secret input.
// Reserved under case folding: every GO* and CGO_* name, PATH, TMP, TEMP,
// TMPDIR, HOME and USERPROFILE. A Windows spec requires literal SystemRoot;
// WINDIR is optional but literal. No permitted-name enumeration is frozen.
// S2 checks host matching and loader values, and refuses anything it cannot
// honor. It supplies GOENV=off, GOTOOLCHAIN=local, GOFLAGS="", GOWORK=off,
// GOPROXY=off, GOSUMDB=off, GOVCS=*:off, CGO_ENABLED=0; GOOS, GOARCH,
// GOMAXPROCS and GOROOT come from the spec. Fresh GOCACHE, GOPATH,
// GOMODCACHE, GOTMPDIR and platform home/temp roots replace inheritance.
// Other reserved names, including PATH and GODEBUG, are absent in v1.
//
// FileManifest order is format, schema, files, with format
// dwarf-harness/file-manifest and schema 1. LoadFileManifest streams File rows
// under a caller-supplied positive row cap. The store's body-byte cap applies,
// not the configuration's one-record cap. The caller bounds the body reader.
// Visitor results are provisional until the complete document is accepted;
// a refused input returns no digest and the caller discards collected rows.
//
// Digest preimages (compact typed JSON, in exactly this order):
//
//	check_sha256:
//	  schema:1, check_spec:<complete CheckSpec>
//	environment_sha256:
//	  schema:1, environment_spec:<complete EnvironmentSpec>,
//	  toolchain_manifest_sha256, dependency_manifest_sha256
//	inventory_sha256:
//	  schema:1, inventory:<ordered nodes>
//	binding_sha256:
//	  schema:1, colony_id, origin, trigger, subject, activation_record_hash,
//	  epoch, field_law_sha256, validation_policy_hash, check_policy_hash,
//	  environment_policy_hash, check_sha256, environment_sha256,
//	  inventory_sha256, runner_sha256, adapter_sha256
//	comparison_sha256:
//	  schema:1, subject, check_sha256, environment_sha256, inventory_sha256,
//	  field_law_sha256, validation_policy_hash, runner_sha256, adapter_sha256
//
// The golden retains exact bytes for the four named check, environment,
// binding and comparison preimages. A comparison excludes origin, trigger,
// activation revision, nonce and run roots, not actual subject or oracle
// inputs. It permits comparison only, never execution reuse or disclosure.
// The logical run-path names remain in the environment preimage; actual sorted
// child variables have their separate concrete_environment_sha256.
// capacity_profile_sha256 is an opaque digest of host settings. This contract
// defines no preimage for it and it is NEVER replay-checkable against current
// settings. runner_sha256 hashes the host executable, not child-reported bytes.
//
// Record key order is the corresponding exported struct's order. Origins
// carry agent_id, execution_session_id, context_id, turn_id? and agree with
// registered F3 provenance and the agent/session/context of every envelope in
// their transition. No validation-group envelope carries turn_id, including
// command, effects, pins and receipt. Trigger kinds are closed: explicit, hook,
// promotion, reproduction. A nonexplicit trigger names its actual committed
// source sequence/hash; only hook carries hook_id. Requested decoding requires
// origin.turn_id for hook, refuses it for explicit and reproduction, and permits
// it for promotion. A hook subject must be observation and its source sequence
// and hash must equal the trigger's. Authorization contains principal (owner
// or hook) and source_permissions_sha256. It identifies the retained
// authorization snapshot but never substitutes for current authority.
//
// The authorization document has format dwarf-harness/validation-authorization,
// schema 1, then principal, context_id, check_policy_hash?, hook_id?,
// source_sequence?, source_record_hash?, in that order. Owner has only
// context_id after principal. Hook requires all four additional members.
// ValidationRequested.AuthorizationDocument is the shared derivation: the
// context comes from origin; hook values come from check_policy_hash and
// trigger. LoadValidationAuthorization is strict and requires exactly the
// compact canonical encoding, without a newline. Unknown format/schema is
// unsupported. Requested decoding recomputes this document's digest and
// requires principal hook exactly for a hook trigger. The body remains
// published and pinned, despite being reconstructible from records.
// Visibility's authorization digest equals its request's authorization
// digest; replay checks this join, and current reads still recheck authority.
//
// Subject kinds are canonical, observation and generation. All require
// kind, workspace_id, object_format, source_sequence, source_record_hash,
// commit, tree, capture_inputs_revision, capture_inputs_sha256. Git IDs have
// 40 lowercase hex digits for sha1, 64 for sha256. Other members are refused
// unless listed for that kind:
//
//	canonical: no further members; source is the latest promotion_accepted,
//	  or workspace.registered before any acceptance.
//	observation: source_manifest_sha256; source is scratch_changed.
//	generation: source_manifest_sha256, request_id, capture_id,
//	  submission_tree, submission_base, generation_id, generation_sequence,
//	  generation_hash, predecessor_commit, predecessor_tree, integration_rules;
//	  optional predecessor_request_id and predecessor_generation_id together;
//	  optional predecessor_witness_seq only without that pair.
//
// Generation source is promotion_admitted; generation_sequence/hash name its
// clean generation_derived. Commit/tree are derived, submission_tree/base are
// the admission's sealed_tree/delta_base_tree, and the capture-inputs pair is
// what the merge used. Decoders check presence/form; S3 checks actual F2 joins.
// Lineage is origin_kind, author_context_id?, clone_root_context_id?,
// derived_from_check_sha256?, model_family?. Its only origin_kind is
// owner_policy. Ticket 17 writers omit the optional members because the
// owner configuration supplies no such provenance.
//
// Record payload member order (all names below are version 1):
//
//	check_defined: definition_id, workspace_id, check_id, check_sha256,
//	  check_policy_hash, environment_policy_hash, environment_sha256,
//	  inventory_sha256, defining_files, lineage, pin_id
//	requested: validation_id, origin, trigger, subject, authorization,
//	  activation_record_hash, epoch, field_law_sha256, validation_policy_hash,
//	  check_policy_hash, environment_policy_hash, required_checks,
//	  capacity_profile_sha256, incident_id?, incident_version?,
//	  reproduction_plan?
//	request_changed: validation_id, expected_version, state, reason,
//	  closed_attempts
//	attempt_queued: validation_id, attempt_id, run_id, ordinal, subject,
//	  definition_id, check_sha256, environment_sha256, inventory_sha256,
//	  runner_sha256, adapter_sha256, binding_sha256, incident_id?,
//	  incident_execution_index?
//	attempt_started: validation_id, attempt_id, run_id,
//	  materialized_manifest_sha256, overlay_sha256, canary_sha256,
//	  nonce_sha256, concrete_environment_sha256
//	attempt_finished: validation_id, attempt_id, run_id, outcome, reason,
//	  started, exit_known, exit_code?, root_exited, tree_closed, io_joined,
//	  closure_proof, canary, required_results, artifacts, input_integrity,
//	  runner_suspect, root_pid?, process_run_id?
//	evidence: evidence_id, validation_id, subject, definition_id, check_sha256,
//	  environment_sha256, comparison_sha256, binding_sha256, attempt_ids,
//	  summary, canonical, failure_grade, incident_id?, failure_count,
//	  pass_count, required_count, prior, confidence, adjacent?
//	request_ended: validation_id, expected_version, outcome, reason,
//	  evidence_ids, not_launched_check_ids
//	visibility_changed: validation_id, attempt_ids, expected_version, state,
//	  authorization_sha256, predecessor_acceptance_sequence?,
//	  predecessor_acceptance_hash?
//	hook_refused: hook_id, workspace_id, source_sequence, source_record_hash,
//	  reason
//	receipt: command_id, submission_sha256, submission, validation_id?,
//	  definition_id?, operation, version, effect_sequence
//
// Required_checks entries are check_id, definition_id, check_sha256,
// environment_sha256, inventory_sha256. Reproduction-plan entries are subject,
// definition_id, executions. Closed-attempt entries are attempt_id,
// process_run_id?, proof. Artifact entries are kind, sha256, bytes, complete,
// sorted by kind (stdout/stderr/events/manifest/overlay/integrity). Required
// results are package, name, outcome in inventory order; outcomes are passed,
// failed, skipped, missing (no run) and unfinished (run without terminal).
// Closure proofs are live, retained_job, name_absent, unconfirmed, not_started;
// input_integrity is verified, changed, missing or unavailable.
//
// Prior/confidence are unreduced {numerator,denominator}, both plain positive
// uint64 decimals through 18446744073709551615, numerator < denominator.
// They copy flake_rate_prior/failure_repair_confidence verbatim. All other
// F4 integers are int64. Adjacent contains promotion_request_id,
// acceptance_sequence, acceptance_hash, before_commit, before_tree,
// after_commit, after_tree, before_evidence_ids, after_evidence_ids, complete;
// it is a factual immediate-predecessor packet, never a culprit verdict.
//
// Body-reference sets, sorted and distinct, are shared by ValidationBodyRefs:
//
//	check_defined: both configuration policy hashes, defining_files hashes,
//	  canary.template_sha256, toolchain_manifest_sha256,
//	  dependency_manifest_sha256 from the selected check/environment.
//	requested: authorization.source_permissions_sha256.
//	attempt_started: materialized_manifest_sha256, overlay_sha256; both name
//	  file-manifest bodies.
//	attempt_finished: artifacts[].sha256, absent for no artifacts.
//	visibility_changed: authorization_sha256.
//	every other type, including receipt: absent.
//
// There are no preimage bodies: check/environment/inventory/binding/comparison
// digests are derived identities. Also excluded: runner/adapter/executable and
// lock-file hashes, canary/nonce/marker/concrete-environment/capacity hashes,
// record hashes and Git IDs. Activation owns the law and policy pins; F2 owns
// capture/source manifests. Definition transitivity is one level only: not
// another check, manifest file contents or a whole recursive bundle. Without
// the two loaded specifications the shared helper gives replay's payload-
// visible minimum; readers check coverage and pin equality. Producers supply
// both specifications and get the exact three additional digests.
// The permanent definition pin has precisely the definition's set. Request
// pins have precisely the authorization body. A failure pin has the union of
// that attempt's started/finished sets and is never released by ticket 17.
// End releases a request pin exactly when none of its evidence has canonical
// grade F1/F2. Publishing always precedes naming/pinning a body.
//
// ValidationSubmission order is schema:10, actor, origin, operation,
// expected_version, arguments. Submission origin is the established Origin
// {agent_id,session_id,context_id}, distinct from the record's provenance shape.
// Operations have the validation. prefix. Arguments repeat effect payloads,
// in their member order, but omit expected_version/version, unpins and receipt.
// Unlisted argument members are refused, even null. The operation table:
//
//	define | core | check_defined payload | C,check_defined,body_pin,R
//	request | owner explicit; core hook/promotion |
//	  {requested,attempts,pin_id} | C,requested,queued*,body_pin,R
//	reproduce | owner | {requested,attempts,pin_id} |
//	  C,requested,queued in plan order,body_pin,R
//	launch | core | attempt_started payload | C,attempt_started,R
//	finish | core | {attempt_finished,evidence,failure_pin_id?} |
//	  C,attempt_finished,evidence,failure body_pin?,R
//	advance | core | {validation_id,reason} | C,request_changed running,R
//	cancel | owner or core | {validation_id,reason} |
//	  C,request_changed stopping,R
//	end | core | {validation_id,outcome,reason,evidence_ids,
//	  not_launched_check_ids} | C,request_ended,request body_unpin?,R
//	visibility | core | {validation_id,attempt_ids,state,authorization_sha256,
//	  predecessor_acceptance_sequence?,predecessor_acceptance_hash?} |
//	  C,visibility_changed,R
//	reconcile | core | {validation_id,attempt_finished?,changed?,ended?} |
//	  C,interrupted attempt_finished?,request_changed stopping?,
//	  request_ended?,matching request body_unpin?,R
//	refuse | core | hook_refused payload | C,hook_refused,R
//
// Here C is ledger.command and R validation.receipt. Effects are core; C/R
// use the submission actor, and every envelope preserves its registered origin
// and command operation_id. Status is a read. Request attempts are nonempty,
// ordinal 1 onward and match the full required set, identities and subject.
// Reproduction attempts exactly fill each plan entry and reserve consecutive
// incident indices. Finish evidence ends in that attempt ID. Its failure pin
// is present exactly for canonical evidence and a failed attempt. Reconcile
// has at least one optional member, no evidence or failure pin, and only an
// interrupted incomplete attempt. Changed is {reason,closed_attempts};
// ended is {outcome,reason,evidence_ids,not_launched_check_ids}.
//
// Expected_version is always present, in 0..MaxInt64-1: zero for define,
// request, reproduce, refuse; positive otherwise. Requested creates version 1;
// request_changed, request_ended and visibility_changed each increment it.
// Launch/finish preserve it; a finish after cancel uses the post-cancel value.
// In a multi-effect reconciliation each request-level record consumes the
// previous effect's resulting version. Receipt.version is zero for define/
// refuse, one for request/reproduce, unchanged for launch/finish, plus one
// for advance/cancel/end/visibility, and plus the request-level record count
// for reconcile. Effect_sequence names the first validation.* effect.
// Define carries only definition_id; refuse neither ID; other receipts only
// validation_id. The embedded submission/hash are checked and the entire
// receipt, not only each component, must fit MaxRecordBytes.
//
// Requested carries incident_id, incident_version and reproduction_plan
// together or none. Ordinary required_checks is the complete current required
// order; reproduction selects the incident's one retained check and a plan of
// one same-subject entry or two ordered adjacent entries. The producer derives
// the plan from real sources and current policy, not caller-supplied subjects.
// Its version is the incident version the request was admitted against.
// Incident version starts at 1 and advances once per admitted reproduction.
//
// The initial canonical admissible failure mints the incident and counts as
// execution 1; no incident existed when that attempt was queued. An incident
// attempt_queued has incident_id exactly with incident_execution_index, at
// least 2. Replay requires consecutive indices from 2 and charges at queue
// commit, even if the attempt is never launched, cancelled or interrupted.
// Neither restart nor another command identity refunds a reservation.
// Admission compares the whole plan with current incident capacity; replay
// never compares historical indices or capacity digests with current settings.
//
// Each attempt progresses queued -> started -> finished once. Started is the
// single durable launch intent, before Owner.Run; it binds the materialized
// manifest, overlay, canary, nonce and concrete environment. Optional root_pid
// and process_run_id are finish facts, never a second started phase.
// Passed requires started, exit_known, root_exited, tree_closed and io_joined,
// closure_proof live, canary.state expected_failed, input_integrity verified,
// runner_suspect false, nonempty required_results all passed, and complete
// published artifacts. A recovered exit or an absent process is never a pass.
// Attempt outcomes are passed, failed, skipped, timeout, incomplete,
// infra_failure and no_status. Evidence summaries are passed, failed, flaky
// and inconclusive; failure grades are none, F1 and F2. A timeout cannot mint F1.
// Canary-result order is state, template_sha256, canary_sha256?, nonce_sha256?,
// observed_pid?, marker_sha256?. States are expected_failed, passed, missing,
// ambiguous, unbound and not_run. Canary/nonce hashes occur together and are
// required when started; their presence for a never-launched attempt depends
// on its actual launch-intent record (S3 checks), never a fabricated nonce.
// Marker/PID occur together, required for expected_failed/unbound, forbidden
// for missing/ambiguous/not_run and optional for passed. Passed/failed/skipped
// outcomes require expected_failed; infra_failure requires not_run. Timeout,
// incomplete and no_status may carry any state. Passed/missing/ambiguous/
// unbound canaries require runner_suspect. A false started flag allows only
// infra_failure/incomplete with not_run, closure not_started, all four other
// proof booleans false, no exit/PID/process ID and empty required_results.
// A true started flag requires nonempty required_results.
//
// Request_changed states running/stopping are durable before launch or stop
// signalling. closed_attempts is normally empty; reconciliation supplies only
// newly positively closed, already-finished attempts with retained_job or
// name_absent proof. Their original nonpassing outcomes are never rewritten.
// A batch cannot end while a root remains unconfirmed. Request_ended outcomes
// are finished, cancelled, interrupted and unavailable; unlaunched check IDs
// remain in declared execution order and never acquire fabricated attempts.
//
// Generation diagnostics start sealed. A withheld result exposes only its
// validation identity, generic lifecycle and sealed state, not hashes, paths,
// verdicts, artifacts or command details. Release needs the exact predecessor's
// durable acceptance, all non-own-input permissions, no invalidation and
// current requester authority. audit_only_obsolete never later releases.
// These checks precede every status, Since, body/artifact and natural-turn read.
// Definition pins and canonical F1/F2 obligations are permanent in this family;
// ordinary diagnostic bodies follow the existing finite body-retention policy.
// Missing collected content is artifact_unavailable, never a fabricated pass.
//
// Hooks fire only after a live turn's own observation and ending commit, never
// during replay or at tool completion. Service replay requires the trigger's
// source to be workspace.scratch_changed in origin.context_id and the subject's
// workspace. A runtime.turn_result for origin.turn_id must have the same
// agent/session/context and satisfy bound_sequence < trigger.source_sequence
// < turn_result sequence < request command sequence. If that turn has an F3-16
// owner, the request follows owner.close. For each (hook_id,source_sequence),
// history contains at most one hook-triggered request or one hook_refused.
// A refused hook has no job or validation ID and does not change the turn's
// outcome. Disabled/unsupported runners do not fire hooks. Open reconstructs
// only; explicit startup reconciliation writes missing interruption/closure
// bookkeeping without executing checks, inventing hooks or repeating effects.
//
// Reasons are open lowercase tokens of at most 64 bytes. The following is the
// v1 writer set, not a closed decoding enumeration. A later producer slice may
// add a token by addition and update this table in that same slice. Consumers
// never interpret an unknown reason as success. Each row names its associated
// outcome/state, cause and owner's next action; no terminal surface prints
// private paths, digests, run roots or child output to explain a refusal.
//
//	token | outcome/state | cause | owner action
//	required_passed | passed | every required node passed | none
//	required_failed | failed | required assertion failed | inspect and repair
//	required_skipped | skipped | required node skipped | remove skip or repair
//	no_execution | no_status | no required execution observed | inspect runner
//	terminal_missing | incomplete/no_status | terminal event absent | inspect run
//	cached_output | incomplete/no_status | output claims cache reuse | fix inputs
//	invalid_output | incomplete/no_status | malformed/conflicting events | inspect
//	canary_passed | incomplete/no_status | sentinel did not fail | repair runner
//	canary_missing | incomplete/no_status | sentinel absent | repair runner
//	canary_ambiguous | incomplete/no_status | duplicate sentinel/marker | inspect
//	canary_unbound | incomplete/no_status | marker identity mismatch | inspect
//	exit_mismatch | incomplete/no_status | exit and events disagree | inspect
//	input_changed | incomplete/no_status | pinned input mutated | restore inputs
//	input_missing | incomplete/no_status | pinned input missing | republish input
//	integrity_unavailable | incomplete/no_status | integrity unproven | inspect
//	build_failed | infra_failure | suite could not build | repair build inputs
//	environment_unavailable | infra_failure/refused | pins unavailable | republish
//	definition_unavailable | refused/unavailable | definition missing | republish
//	launch_failed | infra_failure | native launch failed | inspect host error
//	timeout | timeout | wall limit reached | inspect suite or adjust capacity
//	cancelled | stopping/cancelled/incomplete | owner cancelled | none
//	interrupted | interrupted/incomplete | host lost execution | inspect ending
//	superseded | stopping/incomplete | bound inputs no longer current | request anew
//	authority_lost | stopping/incomplete | current authority lost | restore scope
//	shutdown | stopping/incomplete | host is shutting down | inspect on restart
//	recovery_pending | stopping/incomplete | closure unproven | resolve host fence
//	busy | refused | finite capacity reserved | wait for capacity, request anew
//	eligibility_held | refused/stopping | origin not eligible | resolve its hold
//	validation_disabled | unavailable | capacity absent | configure capacity
//	unsupported_platform | unavailable | native proof unavailable | use supported host
//	unsupported_adapter | unavailable | adapter version unsupported | install support
//	incident_executions_unset | unavailable | incident cap absent | configure cap
//	budget_exhausted | refused | whole plan exceeds incident cap | review cap/plan
//	revision_mismatch | refused | expected version stale | refresh request state
//	subject_unavailable | refused/unavailable | immutable source unavailable | select valid source
//	acceptance_unavailable | refused/unavailable | adjacency lacks witness | select real acceptance
//	artifact_unavailable | read refused | retained identity lacks bytes | inspect retention
//	started | running | first live attempt admitted | none
//	completed | finished | all planned bookkeeping complete | inspect results
//	closure_confirmed | running/stopping | retained roots positively closed | none
//	released | released | disclosure predicate satisfied | none
//	obsolete | audit_only_obsolete | speculative lineage invalidated | request anew
package ledger
