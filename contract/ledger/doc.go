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
// the four F2-05 types, the three F2-06 types and the two F2-07 types below,
// each at version 1.
// F2-05 joined by the owner's additive freeze (D115); it does not change F0
// or format version 1. F2-06 is ticket 06's part of F2, frozen (D118),
// also by addition to format version 1.
// F2-07 is ticket 07's part of F2, frozen (D119),
// also by addition to format version 1.
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
package ledger
