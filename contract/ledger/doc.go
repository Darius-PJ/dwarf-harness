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
// # Catalogue
//
// A Catalogue lists the record type versions a writer may append. Defined
// returns those this contract version defines: the four F0 record types,
// the four F2-05 types and the three F2-06 types below, each at version 1.
// F2-05 joined by the owner's additive freeze (D115); it does not change F0
// or format version 1. F2-06 is ticket 06's part of F2, proposed for the
// owner's freeze (D113), also by addition to format version 1.
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
//
// The F2-05 golden at testdata/f2/05/log/00000000000000000001.jsonl holds
// six commits and twelve records: two registrations and their pins, two
// scratches, one input revision update and its pin/unpin, then one closure.
// It pins a drive-rooted path and a POSIX path, both Git ID lengths and
// revisions 1 and 2. Tests cover the UNC form separately.
//
// # F2-06 record types
//
// These three version-1 types are ticket 06's part of F2, proposed for the
// owner's freeze (D113), by addition to format version 1. C2 owns their
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
// outcomes' actors. request_id is admitted and not ended. For conflict,
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
package ledger
