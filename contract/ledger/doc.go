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
// F2-08 types, the four F2-09 types, the four F-U3-61 types, the four
// F3-10 types, the ten F3-11 types, the three F3-12 types and the eight
// F3-13 types below, forty-nine in all, each at version 1.
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
// frame exact body bytes. Authentication is never an input. Version-1 writers
// use exactly accept, anthropic-version and content-type semantic headers.
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
package ledger
