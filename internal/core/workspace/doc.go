// Package workspace provides Core's registered Git workspaces, bound private
// scratches, exact capture and capture reservations. The service writes the four
// frozen F2-05 workspace record types through the colony store. Nothing here
// accepts a promotion or moves main; there is no capture-only product command.
//
// # Git and its environments
//
// Core resolves git to an absolute executable path. Git 2.42 or later is
// required: the leading major.minor.patch triple is checked, and the complete
// version text after "git version " (including vendor suffixes) is pinned.
// Missing, old or malformed Git gives ErrGitUnavailable at inspection. Version
// mismatch gives inputs_stale, naming both pinned and running versions. A Git
// upgrade therefore requires the owner's input reapproval before captures can
// resume. Open still succeeds when Git is unavailable.
//
// Commands use exec.CommandContext, a five-second WaitDelay and at most 64 KiB
// of stderr. Each call declares its expected exit codes; an unexpected exit is
// a GitError matching ErrGit. Git message text never determines an outcome.
// GitError also preserves cancellation and process-start or wait errors for
// errors.Is; cancellation never becomes an expected exit.
// Cancellation detected outside Git returns the context error directly; use
// errors.Is with context.Canceled or context.DeadlineExceeded in either case.
// Listings are NUL-delimited, and parsers return no partial list on malformed
// output. Object IDs are lowercase hex: 40 digits for sha1, 64 for sha256.
// Core never fetches missing objects.
//
// Three explicit environments separate observation from execution:
//
//   - Inspection preserves the owner's live configuration, including Git config
//     environment variables, but removes GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE,
//     GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_COMMON_DIR,
//     GIT_NAMESPACE, GIT_CEILING_DIRECTORIES and
//     GIT_DISCOVERY_ACROSS_FILESYSTEM. It sets GIT_TERMINAL_PROMPT=0 and
//     GIT_OPTIONAL_LOCKS=0.
//   - Repository operations remove every GIT_* variable (case-insensitively on
//     Windows), including GIT_CONFIG_PARAMETERS and GIT_CONFIG_COUNT/KEY_n/
//     VALUE_n. They set GIT_CONFIG_NOSYSTEM=1,
//     GIT_CONFIG_GLOBAL=<colony>/workspaces/empty, GIT_NO_REPLACE_OBJECTS=1,
//     GIT_TERMINAL_PROMPT=0 and GIT_OPTIONAL_LOCKS=0. Arguments select the common
//     Git directory and force core.hooksPath to workspaces/nohooks,
//     core.fsmonitor=false, core.fsync=committed and core.fsyncMethod=fsync.
//     Repository configuration is still read, but these operations do not
//     convert file content. Marker hashing explicitly uses --no-filters.
//   - Capture/materialization uses the repository environment without its
//     --git-dir argument, adding GIT_DIR=<inputs>/git, GIT_WORK_TREE=<scratch>,
//     GIT_INDEX_FILE=<private index>, GIT_OBJECT_DIRECTORY=<common>/objects and
//     GIT_ATTR_NOSYSTEM=1. The private Git configuration replaces live workspace
//     configuration, attributes, excludes, hooks and refs.
//
// Paths are absolute at the filesystem boundary; repository-relative paths use
// slash separators. Positional revisions and paths follow --end-of-options.
// A -C option's directory and --git-dir=<directory> are option values, not
// positional paths. Plain rev-parse echoes --end-of-options, so discovery
// (--path-format=absolute --git-common-dir --show-object-format) has no revision
// argument or boundary; every revision resolution uses --verify and
// --end-of-options. Commands taking paths on NUL-delimited stdin need no
// command-line path boundary. No listing is parsed by newline.
//
// # Inspection and pinned inputs
//
// Inspect is read-only. It refuses a nonrepository, a missing main commit, or
// main checked out in any worktree (D48). Bare repositories are allowed. The
// service repeats this check at registration; scratch creation repeats it
// independently, immediately before creating a private branch.
//
// Proposal preserves Git's absolute, forward-slash common-directory string
// verbatim, including on Windows. It also holds the Git version, object format,
// seven normalization settings and five HostFiles. Absent files are distinct
// from empty ones. Each file is bounded at 1 MiB. Inspection imports:
//
//   - core.autocrlf (default false), core.eol (native), core.filemode (true),
//     core.ignorecase (false), core.longpaths (false),
//     core.precomposeunicode (false) and core.symlinks (true);
//   - common-dir info/exclude and info/attributes;
//   - core.excludesFile, or XDG_CONFIG_HOME/git/ignore when XDG_CONFIG_HOME is
//     nonempty, otherwise HOME/.config/git/ignore (os.UserHomeDir if HOME is
//     unset);
//   - the global and system attribute paths reported by git var
//     GIT_ATTR_GLOBAL and GIT_ATTR_SYSTEM.
//
// The Core-internal capture-input document is compact JSON, without a trailing
// newline, in this exact key order: format, version, rules, git_version,
// object_format, core.autocrlf, core.eol, core.filemode, core.ignorecase,
// core.longpaths, core.precomposeunicode, core.symlinks, then the digests of
// present local_excludes, global_excludes, local_attributes, global_attributes
// and system_attributes. Format is dwarf-harness/capture-inputs, version is 1,
// and the current rules are capture-rules/1. File and document digests are
// SHA-256. Unknown, missing, repeated and null keys are rejected. The decoder
// must keep accepting every past document version because old digests remain
// replayable forever. This document is not a new ledger contract.
//
// The service publishes the document and present files to the body store and
// pins the current revision; those bodies, never materialized files, are
// authority. The owner alone approves revisions. Later revisions affect future
// captures, never an existing sealed capture.
//
// workspaces/<wdir>/inputs/<revision> holds git/HEAD (ref: refs/heads/capture
// plus LF), git/refs, git/info/exclude, git/info/attributes, git/config,
// global-excludes and attributes. Every use compares all materialized bytes
// with pinned bytes and repairs a missing or changed file through a temporary
// file and rename. Files use mode 0600 and directories 0700 where supported.
//
// The private config uses the pinned object format and seven settings;
// core.safecrlf=false, bare=false, fsmonitor=false, untrackedCache=false,
// splitIndex=false, quotePath=false, fsync=committed and fsyncMethod=fsync.
// Missing local files become empty; missing global excludes point at
// workspaces/empty. attributes concatenates system bytes, an LF if needed, then
// global bytes, so global rules override system rules. No filter is granted.
// The capture engine must verify raw bytes against blob or crlf(blob), rejecting
// irreversible conversion; it must not guess that a lone CR is normalized.
//
// # Private worktrees and retention
//
// The service mints never-reused eight-lowercase-hex directory names from its
// UUIDv7 IDs, reserving one scratch per context/workspace pair. Mechanics create
// refs/heads/dwarf-harness/<dir> at main's tip, never main itself. An anchor ref
// refs/dwarf-harness/anchors/<scratch ID> retains that base commit. Creation uses
// worktree add --no-checkout followed by private read-tree -u --reset against
// the anchor tree, writing the worktree's real index under pinned conversion.
// A creation failure attempts cleanup; reconciliation later owns any orphan.
// Before materialization, a recursive NUL-delimited tree listing rejects case
// collisions over paths and directory prefixes on every platform, naming both
// paths. An unmaterializable baseline is never checked out first.
//
// refs/dwarf-harness/colony holds the raw blob "dwarf-harness colony <ID>" plus
// LF. Creation uses hash-object -w --stdin --no-filters then update-ref with the
// format-sized zero old ID. A lost race rereads the marker; another colony is
// refused. A matching marker allows interrupted registration to resume. Marker,
// anchor and capture ref creation is compare-and-swap, never overwrite.
// Captures retain trees under refs/dwarf-harness/captures/<capture ID>; their
// later owners release them. Anchors are released after scratch removal.
// Objects precede refs, which precede the service's record. This is not a
// power-loss guarantee or a same-user security boundary (C10).
//
// # Exact capture
//
// A capture takes a physical scratch, a trusted baseline tree, a live reserved
// token and observe or seal mode. The baseline is the anchor in ticket 05;
// ticket 06 can supply a local parent's sealed tree. Scratch HEAD, its index,
// assume-unchanged, skip-worktree and stat-only hints are never authority.
// Preconditions are an explicit profile, available Git at the pinned version,
// a usable workspace with this colony's marker, and a present scratch directory.
//
// Each capture rematerializes the pinned inputs and creates a UUIDv7 private
// index at workspaces/<wdir>/tmp/<capture ID>.idx. read-tree loads the baseline.
// Baseline gitlinks and symlinks are unsupported_kind. Baseline path and
// directory-prefix collisions give case_collision before the walk, on every OS.
// Eligible untracked paths come from ls-files -z -o --exclude-standard; a
// trailing slash identifies an unsupported nested repository. The ignored
// listing additionally uses -i --directory. IgnoredEntries counts its entries:
// a wholly ignored directory is one entry, not its recursive file count. No
// ignored names are returned.
//
// Before raw candidate reads or Git candidate hashing, Core walks without
// following links. DirEntry.Type
// and Lstat classify regular files and real directories; ModeSymlink gives
// unsupported_kind, and ModeIrregular, pipes, sockets and other special kinds
// give special_file. Only the root .git file and exactly named, wholly ignored
// real directories are skipped. Core reconciles exact path bytes against Git:
// tracked-but-absent paths are deletions, except a case-only spelling under
// pinned ignorecase=true gives name_mismatch. Git-only eligible paths also give
// name_mismatch; Core-only nonignored files give unobservable_path. Candidate
// names must be UTF-8, otherwise invalid_path. Tracked paths stay eligible
// despite ignore rules;
// same-capture .gitignore edits and negations govern new files under Git's
// normal precedence with pinned local and global excludes.
//
// File count and walked raw-size caps give file_cap and byte_cap respectively,
// before the first raw read.
// check-attr then rejects filter as unsupported_filter and working-tree-encoding
// or ident as unsupported_conversion unless their values are unspecified or
// unset. No driver is granted, including an unknown or empty-valued filter.
// Core reads every candidate in path-byte order, streaming SHA-256 and actual
// byte counts. A running byte-cap breach gives byte_cap; open/read errors give
// unreadable; changed size or modification time gives scratch_changed.
//
// Git removes deleted entries from the private index, adds every present
// candidate under pinned conversion, and writes the tree. Core verifies the
// exact path set, regular modes 100644/100755, and final case collisions over
// paths and every directory prefix using strings.ToUpper. Equivalence to NTFS
// folding is an inference, not a Unicode normalization guarantee.
//
// cat-file --batch streams each distinct blob through raw and CRLF SHA-256
// hashers. Per path, raw must equal blob or crlf(blob), where crlf inserts CR
// before LF only if the preceding byte is not CR, including across chunks.
// Otherwise the result is scratch_changed when metadata changed, or
// lossy_normalization. Whole blobs are never buffered. Every raw-read file is
// restatted before sealing. Under autocrlf=true, irreversible mixed endings
// are refused; lone CR bytes are not special-cased and may be stored verbatim.
//
// The raw manifest is JSON Lines. Its first line has keys format, version,
// object_format, with format dwarf-harness/capture-manifest and version 1.
// Following rows are path-byte-sorted and have keys path, mode, id, size,
// sha256 in that order. Raw size and SHA-256 remain distinct from the Git blob
// ID. A manifest over ledgerstore.MaxBodyBytes gives manifest_cap. The bytes
// and their SHA-256 are returned, not published. The result also carries the
// capture ID, tree, baseline, inputs revision and SHA-256, file and raw-byte
// counts, and the seal-only retention ref.
//
// Seal creates refs/dwarf-harness/captures/<capture ID> with a zero old ID.
// Observe creates no retention ref. The private index and its lock artifact
// are removed on every outcome. A refusal returns no result and retains
// nothing. A retained tree and its blobs survive scratch, index, HEAD,
// attribute, ignore and live-config changes and git gc --prune=now; ticket 06
// and later terminal-state owners release the retention ref.
//
// # Reservation lifetimes
//
// Scratch exclusion and the colony-wide MaxBytes share are acquired under one
// mutex, both or neither. Exclusive acquisition never waits for a writer or
// another exclusive holder: scratch_busy returns without a snapshot, queued
// retry or interrupted writer, even if the request context has already ended.
// An insufficient bytes pool gives capture_budget without retaining exclusion.
// BeginWrite holders are shared; a writer waits context-boundedly for an
// exclusive holder to release and never interrupts its capture.
//
// Only reserve mints the unexported token. Under the same mutex, capture refuses
// nil, unminted, wrong-scratch, used, released or bytes-less tokens with a typed
// internal error matching ErrInvalid. Marking is atomic: one token permits one
// capture, including concurrent attempts. The caller owns and defers release
// of exclusion through its entire operation: observe, durable close and
// removal, or seal and admission record. The engine returns only the bytes
// share on every outcome. release is idempotent and returns whatever parts
// remain, including bytes when the engine was never reached. Closure can take
// exclusion alone for an absent directory, adding bytes only when it observes.
// Ticket 06 adds its longer-lived K-slot to this token, not a second mechanism.
//
// # Service lifecycle and records
//
// Open takes the host-owned ledgerstore store (opened with ledger.Defined) and
// verifies its log directory with os.SameFile. One service owns a colony's
// workspace projection and Git runner. Register rechecks the observed part of
// the owner's Proposal and records exactly the five reviewed files. It refuses
// duplicate common directories, including filesystem aliases, and another
// colony's marker. A matching marker permits interrupted registration to resume.
// Register atomically writes registered, capture_inputs_pinned revision 1, and
// body_pin. UpdateInputs is an owner-only expected-revision compare-and-swap:
// inputs revision n+1, its body_pin, and the previous body's body_unpin are one
// commit. In-flight captures retain their inputs; later ones see the new revision.
// The caller authenticates the owner; no stand-in authenticator exists in C2.
//
// Scratch creates one worktree per (context, workspace) pair, serializing first
// uses of that pair with context-bounded waiting, without serializing Git work
// for other pairs. UUIDv7 suffix names are never reused from recorded history;
// at most three IDs are tried around history, directory and branch collisions.
// Objects, anchor ref and materialized worktree precede scratch_created. A
// definite append failure rolls creation back. An uncertain append retains the
// worktree until replay determines whether the record exists.
// Workspace materialization directories likewise use the ID's last eight hex
// digits. Register mints around recorded suffixes and existing directories,
// at most three attempts, then ErrInvalid. Replay rejects a repeated workspace
// suffix with ErrInconsistent; no alternative directory mapping exists.
//
// At Open the service enforces every record rule the log decides; the exact-set
// rule of capture_inputs_pinned is enforced at append and, at Open, for the
// current revision only; superseded revisions' bodies are never read. Checks
// include actor and context binding, commit shape and pin matching, revision
// sequencing, workspace identity, never-reused scratch identities/names, one
// open scratch per pair, branch == refs/heads/dwarf-harness/<dir>, both Git ID
// lengths matching the workspace format, and at-most-once closure. A violation
// gives InconsistentError matching ErrInconsistent and naming its sequence.
// Unknown closure reasons remain readable closures; the producer writes only
// abandoned. A newer consumed record version returns the ledger reader's
// UnsupportedVersionError.
//
// Current bodies are verified by ReadBody, decoded and compared with body_refs.
// Missing or unreadable current bodies and unknown document versions make that
// workspace inputs_unavailable, with digest/error detail, without failing Open.
// A malformed known document or a contradictory exact set fails Open. Git
// directories and colony markers are checked before operations. Reconciliation
// empties disposable tmp directories, removes never-exposed orphans, retries
// durable closures, restores missing open anchors when their objects survive,
// and reports missing scratches and unfinished repairs. It writes no record and
// reads no clock. Unrecorded anchor and capture refs are swept in usable
// repositories. An orphan's regular .git file identifies its registered workspace
// by os.SameFile; exactly one matching UUID-suffix anchor proves its base.
// Missing or ambiguous proof permits directory removal only (and pruning only
// in a verified workspace); the branch is retained and named in Pending. A moved
// orphan branch is also retained and reported. Anchors for still-present orphan
// directories survive the stray sweep, preserving retry proof. Stray branches
// without directories are never swept. Every ref deletion uses an old-value CAS.
//
// Close drains operations and outstanding BeginWrite leases, rejects later calls
// with ErrClosed, and is idempotent. The host closes the store separately.
// appender.ErrUncertain or appender.ErrPoisoned poisons the service: every later
// call except Close returns ErrPoisoned. Recovery requires the host to recover
// the store and open a new service, never an in-memory guess about a commit.
// Projections retain historical identities and names; memory grows with history.
//
// # Removal boundary
//
// CloseScratch proves a scratch has no unrepresented eligible edits before
// durably recording scratch_closed. Its only represented tree in ticket 05 is
// its anchor; a capture refusal blocks closure. An owner, or the bound context's
// agent, may close it. Ignored entries are scratch-local and discarded;
// ClosureResult.IgnoredEntries counts Git's ignored listing, with an ignored
// directory counted once and no path reported. Removal-only retries report zero.
// A missing directory may close without observation.
//
// After durable closure, or for an orphan never handed to a context, Core owns
// removal. A DirEntry/Lstat walk unlinks symlinks and ModeIrregular reparse
// points with os.Remove without descending, descends only real directories,
// and unlinks files. The root regular .git file is preserved until every other
// child is removed, so a pending orphan keeps its workspace identity for retry.
// Only then is .git unlinked and os.RemoveAll removes the empty root. Core
// never asks git worktree remove --force to traverse an unverified directory.
// Removal failure gives ErrRemovalPending; registration is not pruned until
// removal completes. Then git worktree prune runs, the
// scratch branch is deleted by compare-and-swap only if still at its base, and
// the anchor is deleted by CAS against its observed ID. Moved branches retain
// their history. A lost anchor CAS is ErrRemovalPending, never assumed success.
// Closed scratches and Open's orphan sweep retry this same primitive.
// Same-user check/use races remain C10's boundary.
//
// # Profile, measurements and later admission
//
// CaptureProfile is the capture subset of CORE:862's CapacityProfile, extended
// by ticket 06 and C4 rather than a second capacity authority. MaxFiles and
// MaxBytes must be positive, and BytesInFlight must be at least MaxBytes;
// otherwise configuration gives ErrInvalid. A nil Config.Capture gives
// no_capacity_profile. ProvisionalCaptureProfile returns 100,000 files, 1 GiB
// per capture and 20 GiB in flight. These are calibration inputs, not measured
// capacity, a colony cap or a service level.
//
// Reads are sequential in v1. The opt-in DWARF_CAPTURE_WORKLOAD_OUT harness
// records creation, fresh and warm observe, a seal after ten edits, and closure
// observation for 1,000 and 10,000 roughly 800-byte files. Results and work
// directories stay outside the repository. It skips under -race, leaves
// Defender enabled and adds no exclusions, compares trees with reference Git,
// and has no timing threshold. Burst concurrency remains C4's feasibility gate.
// Windows long paths Git cannot list give unobservable_path; sharing violations
// give unreadable. With filemode=false, tracked executable modes are preserved
// and new files get 100644. Linux and macOS CI require Git at least 2.42.
//
// C2 has no validator: no_validator is unconditional, with no injectable seam,
// until C4 wires the real validator. RequestPromotion checks binding, then the
// profile, then immediate reservation, then Git, workspace and scratch
// availability. It returns unavailable and releases. It never captures, records,
// waits, queues, retries or changes refs.
// Replay reads no clock. Observe and refused captures can leave loose objects;
// plumbing never triggers gc --auto. Their maintenance policy remains open for
// ticket 06 or later, not 05.
package workspace
