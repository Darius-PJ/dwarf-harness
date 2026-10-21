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
// the four F2-05 types, the three F2-06 types, the two F2-07 types, the three
// F2-08 types, the four F2-09 types, the four F-U3-61 types and the four
// F3-10 types below, twenty-eight in all, each at version 1.
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
// F3-10 is ticket 10's part of F3, proposed for freeze (D125),
// also by addition to format version 1.
// The type namespace "ledger." is reserved for F0
// record types: no other record family may use it. The namespace "fixture."
// is reserved for this contract's golden fixtures and tests: no record
// family may use it, and Defined never contains it.
// The namespace "workspace." is reserved for C2's record families.
// The namespace "graph." is reserved for U3's record families.
// The namespaces "runtime." and "private." are reserved for C3's Core runtime.
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
// These four version-1 types are ticket 10's part of F3, proposed for freeze
// (D125). C3 owns their payload specifications. Only Core's agent service
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
package ledger
