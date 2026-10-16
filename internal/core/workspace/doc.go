// Package workspace provides Core's registered Git workspaces, bound private
// scratches, exact capture, bounded checkpoints and integration generations.
// The service writes frozen F2-05, F2-06 and F2-07 records through the colony store.
// Nothing here accepts a promotion. C2's product never admits, captures,
// integrates or moves main; admit, integrate and the record-free canonical
// primitives are reachable only from package tests until C4 reserves real
// validation. No exported seal or merge exists.
//
// # Git and its environments
//
// Core resolves git to an absolute executable path. Git 2.45 or later is
// required: the leading major.minor.patch triple is checked, and the complete
// version text after "git version " (including vendor suffixes) is pinned.
// Missing, old or malformed Git gives ErrGitUnavailable at inspection. Version
// mismatch gives inputs_stale, naming both pinned and running versions. A Git
// upgrade therefore requires the owner's input reapproval before captures can
// resume. Open still succeeds when Git is unavailable.
// Persisted capture-inputs/1 documents keep their original Git 2.42 validation
// floor so old bytes still replay; this does not lower the executable's floor.
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
// Four explicit environments separate observation from execution:
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
//   - Integration uses the capture environment without GIT_WORK_TREE or
//     GIT_INDEX_FILE, with -c core.bare=true and workspaces/<wdir>/tmp as cwd.
//     Pinned inputs are rematerialized before use. Only pinned info/attributes
//     and the pinned attributes file apply; tree .gitattributes and the
//     repository's configured merge drivers do not.
//
// core.fsync=committed is objects|reference in Git's code at 2.45 and 2.55,
// although Git's documentation describes it as objects. Ticket 08's Git probes
// (818-820, 1285-1288), cited by its design ruling P14, verified the reference
// flush on the pinned Windows Git. Changing the pinned Git re-verifies that
// fact; there is no second per-command fsync recipe.
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
// UUIDv7 IDs, reserving one dedicated scratch per context/workspace pair.
// Mechanics create refs/heads/dwarf-harness/<dir> at an explicit base commit,
// never main itself. Dedicated creation passes main's tip; a temporary forks
// its dedicated parent's acknowledged private tip. An anchor ref
// refs/dwarf-harness/anchors/<scratch ID> retains exactly that base commit.
// Creation uses worktree add --no-checkout followed by private read-tree -u
// --reset against the anchor tree, writing the real index under pinned conversion.
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
// Captures retain trees under refs/dwarf-harness/captures/<capture ID> until
// request retention permits release. Anchors are released after scratch removal.
// Objects precede refs, which precede the service's record. This is not a
// power-loss guarantee or a same-user security boundary (C10).
// Scratch-commit refs refs/dwarf-harness/scratch-commits/<ID> retain private
// observation and merge-back commits by the same zero-ID create and recorded-ID
// delete CAS as generation refs. The service retains every observation commit
// while its scratch is open, including parent observations from which open
// temporaries forked. Closing a parent with an open child is forbidden.
// An admission retains only the observation its scratch_observation_id names and the merge-back commits its merge_back_ids name; a named merge-back's source history is reached through its target commit (a fast-forward's is the source commit) and the source's moved branch, which removal never deletes, never through a restored observation ref.
// A request retains its named provenance, the observation its scratch_observation_id names and the merge-back commits its merge_back_ids name, exactly as long as it retains its capture: added together in retainedRefs and released by the same compare-and-swap when the request leaves retention. An unresolved preparation retains its prepared commit until it is resolved, and an open scratch retains its own observations and applied merge-backs until it closes. An ended request retains nothing; its records stay authoritative and moved branches keep the history.
//
// # Typed writers and observations
//
// BeginWrite takes WriteBinding, not a scratch ID. Required IDs are UUIDv7;
// ResumeScratchID may be zero. Different AgentID and TaskAgentID give ErrNotBound.
// These are caller-stated bindings, authenticated by C3: only the live Writer
// keeps the plain binding for equality checks, never an authority store, an
// agent-keyed index or a ledger agent_id; Close drops that binding.
// The same live WriterID with the same binding returns the same handle; a
// changed binding gives ErrWriterMismatch. A second live writer on
// (ContextID, TaskScratchID) gives context_busy. One context may hold a writer
// on its own scratch and one on another task simultaneously.
//
// Only the task's context receives its dedicated scratch. Another context of
// the same agent always receives a fresh temporary, even when the task is idle.
// The per-task routing lock protects its acknowledged fork tip, never main,
// mutable HEAD, the real index or the task's uncommitted files. A pending child
// is reserved before Git work so closure sees it. Temporaries name their parent,
// coexist with their author's dedicated scratch and never enter the pair map.
// ResumeScratchID attaches only to an open temporary of that context and task;
// a foreign binding gives ErrNotBound and a closed temporary gives ErrClosed.
// Resume creates no directory or record; omitting it never reuses old work.
// Pool waits precede the task lock, which permits only immediate-or-busy reserves;
// BeginWrite drops routing across its pool wait.
//
// Writer.Lease protects each physical tool edit through the shared pool and
// returns an idempotent release. Writer.Close marks the handle closed at once,
// without revoking physical leases. Logical occupancy and the service operation
// drain only when outstanding calls and leases return. A nil, copied, forged or
// wrong-service handle gives ErrInvalid; a closed or successfully observed
// handle gives ErrClosed. Scratch returns zero for those handles or a poisoned
// service; Close is harmless and idempotent even for an invalid handle.
// Lease runs no Git and refuses while the most recent tip repair failed.
//
// A concurrent second CommitObserved on the same writer answers
// *BusyError{context_busy}.
//
// CommitObserved freezes new leases, waits within ctx for its existing leases,
// then reserves observation exclusion and MaxBytes immediately or returns busy.
// A lease during observation waits within its own ctx; failure thaws the writer,
// while an acknowledged observation permits only Close. Capture uses the latest
// acknowledged tip tree, with anchor-tree union tip-tree trusted names.
// written_paths is the previous-tip difference; changed_paths is the full
// anchor difference. Both are byte-sorted, deduplicated, NUL-delimited Git
// diff-tree --no-renames path sets, with invalid paths refused as invalid_path.
// The envelope encoder preflights the whole commit against MaxRecordBytes and
// MaxCommitBytes, including worst-case envelope widths, before commit/ref/body
// publication. Overflow gives pathset_cap, never truncation or chunking.
//
// scratch-rules/1 is the frozen F2-07 name for pinned host observation, trusted
// anchor/tip names, raw-manifest versus canonical-tree identity, deterministic
// single-parent commits, and retention-ref/record/private-branch ordering.
// Every observation, including an unchanged tree, gets a new Core commit on the
// previous tip. Its retention ref precedes manifest publication, scratch_changed,
// the immediate body_pin, and the immediate previous-observation body_unpin
// when present. Only after acknowledgment does branch CAS project the new tip;
// the real index is never updated. Sequence is the appender's actual First.
// Definite append failure CAS-deletes the new ref; uncertainty poisons and retains.
//
// Open and pre-use tip repair accept the recorded new tip, CAS the recorded
// previous tip to it, or restore an absent branch from zero. They verify the
// workspace, anchor and worktree admin identity and never overwrite any other
// value: scratch_tip_mismatch blocks routing, observation, admission and closure,
// and appears in Open's Pending. Repair changes no files, index or record.
//
// # Merge-back, retry and task notices
//
// MergeBack accepts an observed temporary after its author closes the Writer.
// It reserves the source first, observes its acknowledged tip, then waits within
// ctx for the task's logical turn and sole mutation exclusion. The routing lock
// is never held while waiting for the physical gate. The task is observed under
// that gate; either scratch's unobserved edits give ErrUncommittedEdits.
// A live parent request is allowed: only its protected capture window excludes
// merge-back, and merge-back in turn makes a capture immediately scratch_busy.
//
// F is the temporary's recorded fork, P the task's acknowledged tip and S the
// source's latest observation. Only P == F fast-forwards to S. Otherwise Git
// merges F.tree, P.tree and S.tree with F explicitly supplied as the base and
// creates a single-parent target on P, even when the trees happen to be equal.
// A merge or derived-tree case conflict pins a report on the task, records a new
// conflict ID and changes neither scratch. The source remains resumable or
// abandonable; a later attempt uses a fresh ID.
//
// Before mutation, the target ref and parent's raw pre-write manifest precede
// prepared plus an immediate pin of the manifest and the recorded revision's
// exact input bodies. The inputs lock bridges snapshot to preparation pin so
// an intervening UpdateInputs cannot release those bodies before preparation.
// Neither scratch may be observed, closed or prepared again while unresolved;
// normal routing and admission return ErrMergeBackPending after a failed write.
// Materialization is followed by an applied record and preparation unpin only
// after full files/index/ref verification. The source then closes with
// temporary_merged_back, releasing its pins and directory but not its moved
// branch. Repeated MergeBack returns the original applied result and finishes
// any outstanding source removal, never applying the work twice.
//
// Post-preparation failures record git, index_lock, ref_mismatch, verification,
// cancelled or interference. Cancellation uses context.WithoutCancel only for
// that bookkeeping, never for files or refs. Unavailable workspace, Git or
// pinned inputs leave the plan pending without inventing a failure token.
// RetryMergeBack alone resumes a pending ID: it reads its pinned historical
// inputs, retains the recorded target and accepts only P or target at the branch.
// Its observed intermediate tree must have, at every path, exactly the old or
// target entry. Third values give merge_back_interference without overwriting.
// Retry removes an index lock only when its regular, unchanged file carries
// this attempt's exact ownership marker; a foreign lock remains untouched.
// Open reports merge_back_pending and validates P/target without moving either;
// it never retries or materializes an unresolved plan.
//
// ScratchNotices is a context-and-task-bound, record-only, replayable cursor over
// outcomes other than prepared. It returns copies, even for a closed task, and
// carries the source's author and written paths separately from the parent's
// full changed paths. It schedules no model turn or callback. Admission records
// the latest task observation and, in applied-sequence order, exactly those
// merge-back IDs after the preceding admission (creation for a root/new root)
// and before this admission; sealed request trees remain immutable.
//
// A merge-back's applied record and its source's closure are separate commits. When the closure commit is missing after the applied acknowledgement (a crash, a cancellation or a definite append failure), Open reports merge_back_closure_pending <temporary ID> and writes nothing; the next MergeBack of that temporary, or RetryMergeBack of its attempt, re-observes the source against its acknowledged tip, appends exactly the missing temporary_merged_back closure with its observation unpin and removes the directory, never a second applied record; an applied source answers ErrInvalid to abandoned.
//
// # Exact capture
//
// A capture takes a physical scratch, a trusted baseline tree, a live reserved
// token and observe or seal mode. Roots use the anchor tree; successors use
// their latest admitted local parent's sealed tree. Scratch HEAD, its index,
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
// Private observations may additionally supply trusted path names, not a union
// tree: an anchor file and a later tip's descendant cannot coexist in one tree.
// Present trusted names remain eligible despite ignores, including inside an
// ignored directory; absent names and names replaced by directories are
// deletions. Admission supplies no additional names and keeps its baseline
// rules unchanged.
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
// attribute, ignore and live-config changes and git gc --prune=now; request
// endings release refs only after their durable acknowledgement.
//
// # Reservation lifetimes
//
// reserve takes a kind. Closure takes scratch exclusion and adds MaxBytes only
// before observation. Observation takes exclusion and MaxBytes, but no slot.
// Admission takes exclusion, MaxBytes and one provisional checkpoint slot,
// all or none under one mutex. It never waits for a writer or
// another exclusive holder, queues a retry or interrupts a writer. Refusals are
// ordered: no_capacity_profile, scratch_busy, checkpoint_slots, capture_budget.
// Closure of an absent directory still needs neither a profile nor bytes.
// Writer.Lease holders are shared; physical writers wait context-boundedly for
// an exclusive holder to release and never interrupt its capture.
// The record-free sole mutation writer waits within ctx until both the shared
// writer count and exclusive holder are clear, then holds the same exclusion.
// Capture, closure and admission meeting it return scratch_busy immediately;
// another writer waits on the pool's changed channel. Cancellation while
// waiting acquires no count, byte allowance or slot.
//
// The pool alone mints the unexported tokens. Under the same mutex, capture
// refuses nil, unminted, wrong-scratch, used, released or bytes-less tokens with
// a typed internal error matching ErrInvalid. Marking is atomic: one token
// permits one capture, including concurrent attempts. The caller defers release
// of exclusion through its entire operation: observe, durable close and
// removal, or seal and admission record. The engine returns only the bytes
// share on every outcome. release is idempotent and returns whatever parts
// remain, including bytes when the engine was never reached. The exclusive
// admission token holds its provisional slot; release without keepSlot returns
// it. keepSlot transfers it at most once into the pair's live count, after which
// release returns only exclusion and bytes. freeSlot ends one live request's
// claim; setLive restores counts at Open. C4 adds the validator to this same
// token, not a second reservation mechanism. K is per open context/workspace
// scratch; workspace and colony checkpoint capacity are sums, not separate caps.
// reserveCaptureUnder lends a live exclusive guard one subordinate capture
// token at a time. The child owns only MaxBytes, no exclusion or checkpoint
// slot, and is accepted only while both it and its guard are registered.
// Capture returns its bytes and unregisters the child; reuse is refused.
// Releasing the guard invalidates its child and returns bytes exactly once.
//
// # Record-free canonical primitives
//
// lockTrain is the per-workspace exclusion shared by integration and canonical
// mutation. It never holds the service mutex across Git or a channel wait.
// guardedMainUpdate requires the files ref backend, a nonsymbolic main checked
// out nowhere, and the exact target tree with old as its only parent. It issues
// one update-ref --no-deref --end-of-options refs/heads/main <target> <old>.
// Regardless of the command's exit, a cancellation-independent ref read decides
// moved, refused or third. No retry, rollback or foreign-lock removal occurs.
//
// hardenCanonicalStorage Lstat-validates real directories and calls
// durable.SyncDir on existing object fanouts and objects/pack, then objects,
// refs/heads, refs and the common directory, child before parent. It returns
// every error and re-reads main's exact target commit and tree after flushing.
// Git already flushed the ref file; Core does not reopen and sync it again.
// A ref effect or completed flush is not ledger acceptance. These primitives
// write no ledger record and have no product caller.
//
// # Record-free integration primitives
//
// mergeTrees applies the sealed submission's delta from its explicit delta
// base to a predecessor tree. It runs merge-tree --write-tree -z --name-only
// --no-messages --merge-base=<base> --end-of-options <predecessor> <submission>
// in the integration environment, with expected exits 0 and 1. Exit 0 must
// carry exactly a NUL-terminated tree ID; exit 1 must also carry at least one
// distinct, nonempty, NUL-terminated conflict path. Shape/exit mismatches return
// no partial result. Exit status, never marker text, decides a merge conflict,
// whose result tree is discarded. A clean tree's recursive ls-tree listing in
// the repository environment is checked for path and directory-prefix case
// collisions on every OS; the first pair becomes a case_collision conflict.
//
// integrationCommit uses commit-tree -p <parent> -m <message> --end-of-options
// <tree> in the integration environment. The predecessor is its only parent.
// Author and committer are DwarfHarness Core <core@dwarf-harness.invalid>; both
// dates are the generation UUIDv7 milliseconds divided by 1000, with +0000.
// The message is "dwarf-harness integration", a blank line, "request <ID>" and
// "generation <ID>". No wall clock or host identity enters the commit.
// Generation refs refs/dwarf-harness/generations/<ID> are created by CAS against
// the format-sized zero ID and deleted by CAS against the recorded commit,
// using the repository environment.
//
// integration-rules/1 names these merge, environment, case-check and commit
// rules. Its private configuration leaves merge.renormalize,
// merge.conflictStyle and merge.default unset; changing these rules requires a
// new rules value, not merely a new inputs revision.
//
// The shared deterministic commit primitive also creates single-parent private
// observation and merge-back commits, even for an unchanged tree. Their dates
// derive from the operation UUIDv7. Messages are "dwarf-harness scratch
// observation", a blank line, "scratch <ID>", "observation <ID>"; and
// "dwarf-harness scratch merge-back", a blank line, "scratch <temporary ID>",
// "merge-back <ID>". An observation moves HEAD without updating the real
// index; Core never reads that index as capture authority. Merge-back installs
// a verified index instead.
//
// Record-free materialization takes an already-observed actual tree under the
// sole mutation writer, a target tree and private branch commits. A fresh
// private index loads actual with read-tree and update-index --refresh.
// Filesystem preflight refuses target additions and file/directory replacements
// obstructed by unrepresented entries, including ignored files or directories,
// as RefusalError with reason obstructed_path and the path, without mutation.
// read-tree -m -u --end-of-options <actual> <target> performs the checkout;
// neither reset nor the real index is used. Re-observation with actual/target
// trusted names must produce exactly the target before any ref write. A Git
// exit of zero is not proof that a locked deleted file disappeared.
//
// Index installation verifies the worktree's regular gitfile, common-directory
// identity, backpointer and symbolic private HEAD. It exclusively creates
// index.lock (0600) with "dwarf-harness scratch-index <ID>" plus LF, syncs and
// closes it, renames the private index onto index and removes only its own
// unchanged lock. A foreign lock remains untouched and gives scratch_busy.
// A copied installed index must write-tree to target; branch CAS is last and
// uses the repository environment. Partial failure preserves the actual disk
// state and leaves refs unmoved. The retry form takes a verified intermediate O
// built from disk, then applies O to target by the same two-tree recipe; it
// never resets the directory. The frozen policy name for these primitives and
// the service's prepared/result protocol is scratch-merge-rules/1.
//
// Conflict reports return deterministic JSON Lines bytes and their SHA-256,
// without publishing or pinning. The header has format
// dwarf-harness/conflict-report, version 1, cause (merge or case_collision) and
// paths (the true distinct total). Up to 10,000 {"path":"..."} rows follow in
// path-byte order, each once. Any non-UTF-8 path gives ErrInvalid and no result,
// including a path beyond the row limit. The service publishes and pins each
// conflict report in its generation's commit. These primitives move no main ref.
//
// # Requests, identity and lineages
//
// Request carries a UUIDv7 RequestID, Actor (owner or agent), ContextID,
// WorkspaceID, Head and RestartFrom. Same-ID identity is exact equality of
// context, workspace, head and restart; Actor is not a binding. An admitted ID
// returns its recorded state before scratch, capacity or Git checks, including
// after closure and restart. Changed bindings give ErrRequestMismatch. An ID
// being admitted gives request_in_flight immediately. Other refusals consume no
// ID and write nothing. RequestState exposes only the context's own submission,
// lineage, checkpoint, failed local request and terminal conflict report.
//
// The latest admitted request is the scratch's head. A root has no prior head;
// a successor names the unfailed head and keeps its lineage at checkpoint n+1.
// After failure a new root names the failed head in RestartFrom, mints a new
// lineage and starts at checkpoint 1. Roots and new roots use the represented
// tree for eligibility and delta base: the latest successful request's sealed
// tree, else the anchor. Successors bind the parent's sealed tree for both.
// Neither a foreign train predecessor nor the mutable
// scratch index supplies eligibility. HeadError reveals only this scratch's
// head and whether restart is required.
//
// beginRequest checks service usability, binding, identity, open scratch and
// head, then reserves exclusion, checkpoint slot and bytes. Under the exclusion
// it rechecks usability, scratch and head before availability or capture.
// Availability proceeds through Git, pinned inputs, workspace, scratch and
// anchor. RequestPromotion then unconditionally returns no_validator, with no
// seam a test could satisfy, and releases without retaining a slot. It never
// captures, records or moves refs.
//
// # Admission
//
// admit marks the ID in flight, seals against the bound local baseline,
// publishes the manifest and verifies its digest. Its commit is admission
// immediately followed by the manifest pin, with the same body_refs. Objects
// precede the capture ref, which precedes body publication and the record.
// The append rule repeats the head check: a parent failed during capture gives
// HeadError with RestartRequired. Definite failure releases the capture ref by
// CAS and returns the provisional slot; uncertain append poisons the service
// and retains the ref for Open. Only acknowledgement transfers the slot with
// keepSlot, under the service lock. Endings free each slot once after their
// acknowledgement; Open restores counts, never checking the current K policy.
//
// # The train and generations
//
// Each workspace's train is its live requests in admission-record order.
// integrate serializes calls per workspace with context-bounded waiting and
// re-reads the train and main after each acknowledged derivation. Only the
// leading GenerationWindow positions receive generations. Their predecessor
// is the nearest live request's current clean generation or canonical main.
// Currency compares predecessor commits, not trees: a changed predecessor
// requires a new generation even if the tree stays identical.
//
// A derivation binds current inputs, merges the submission's local delta over
// the predecessor, and records either the clean integration commit and its CAS
// ref, or a pinned conflict report. Supersession releases the old report pin
// in the superseding commit, and the old generation ref after acknowledgement.
// A speculative conflict stays pending and private; requests behind it wait.
// A leading conflict ends that request and every live higher checkpoint of its
// lineage, in checkpoint order, as dependency_failed. The same commit unpins
// their manifests in ending order, never their conflict reports. Surviving
// foreign lineages rebuild over changed predecessors; they are not ended.
// A repeated integrate with nothing to do records nothing and runs no merge.
//
// One journalled rule set checks F2-06 at append and Open: bindings, identities,
// lineage/head/baseline, historical input digests, format-sized Git IDs,
// immediate pins, current generations, nearest-live predecessors, supersession
// unpins, whole failure suffixes and ordered manifest/closure unpins. K and the
// window are policy, not replay validity. integrate binds observed main commit
// and tree at append. Open checks no live predecessor and absent predecessor
// IDs, not a historical main tip, which F2-06 does not record.
//
// # Mechanical finalization (F2-08, frozen D123)
//
// finalize and noChange are unexported and package-test-only until C4; the
// product's unconditional no_validator is unchanged. Under lockTrain they bind
// the leading live request, its current clean generation and successful local
// parent. The latest promotion_accepted is the workspace's witness; subsequent
// canonical predecessors must match its sequence, commit and tree. A generation
// built over a train predecessor stays current when that exact generation has
// since become the latest accepted witness. A main moved outside that witness
// blocks publication rather than reanchoring.
//
// Before any intent, finalize verifies Git preflight, computes the exact
// predecessor-to-target path set (deletions and both rename names, distinct and
// byte-sorted), and encodes the complete acceptance/unpin commit against ledger
// caps. pathset_cap writes no intent. Tree equality dispatches to noChangeLocked:
// one promotion_no_change and manifest unpin, no intent or canonical update.
// A child of its unaccepted derived commit must rebuild against actual main.
//
// Otherwise the order is acknowledged finalization_intended, one guarded CAS,
// canonical storage durability, then acknowledged promotion_accepted and its
// manifest unpin. Only that acknowledgement releases the K-slot and blocker,
// then the train lock, then unneeded refs by recorded-object CAS. A refused CAS
// observed at the old tip ends interrupted with the live local suffix
// dependency_failed in the same commit. A third tip, durability error or failed
// acceptance append holds finalizing and finalization_pending without another
// update-ref. Open replays that blocker; S3a does not resolve held intents.
//
// Both success kinds advance representation to the admission's sealed tree,
// never the cumulative derived tree. They leave the base, anchor, private tip,
// files and mutable index unchanged, take no writer/task lock and do not close
// the scratch. Replay enforces F2-08 from records alone, including witness
// presence/equality, intent identity/currency and its sole resolution,
// cancellation authority, interrupted suffixes, and manifest-only unpins.
// Terminal record kind decides success: an unknown promotion_failed token is
// unknown_failure with FailureOutcome, even when its text spells accepted.
//
// # Request retention and replay sweep
//
// A capture is retained while its request is live, a live child names it as
// parent, or it is an open dedicated scratch's unfailed head or represented
// request. The represented capture survives a later head's failure. Current
// clean generations, including an unresolved intent's, retain their refs;
// success releases generation refs after acknowledgement (accepted commits are
// retained by main). Terminal requests drop generation payloads.
// Post-acknowledgement release deletes only the recorded object by CAS; a
// failed deletion waits for Open. Conflict report pins survive terminal state
// until closure unless a superseding generation already released them.
//
// Open lists capture, generation and scratch-commit namespaces once each per
// usable workspace, deletes unretained refs by CAS, and recreates missing
// retained refs against zero at recorded objects. Missing objects give
// capture_missing <request>, generation_missing <generation> or
// observation_missing <observation> in Pending. A ref at a different object is
// reported and never overwritten. Open reads no manifest or clock, starts no
// capture or integration and writes no record. Live requests retain their slots,
// captures and current generations. Observation retention walks open scratches
// and their own observation lists; a closed scratch drops its list.
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
// open dedicated scratch per pair, branch == refs/heads/dwarf-harness/<dir>,
// Git ID lengths matching the workspace format, and at-most-once closure.
// Parent-qualified creation requires an open dedicated parent of the same
// workspace and a different context, at exactly its acknowledged commit/tree.
// scratch_changed checks fresh observation identity, open scratch binding,
// creation parent, previous tip, recorded inputs digest/revision, exact manifest
// body_refs, immediate pin and previous-observation unpin. Closures reject open
// children and require all owned pins released once in original pin order.
// The same journalled rule set runs before append and during Open.
// A violation gives InconsistentError matching ErrInconsistent and its sequence.
// Unknown closure reasons remain readable closures; the producer writes only
// abandoned and temporary_merged_back. A newer consumed record version returns
// the ledger reader's UnsupportedVersionError.
//
// Current bodies are verified by ReadBody, decoded and compared with body_refs.
// Missing or unreadable current bodies and unknown document versions make that
// workspace inputs_unavailable, with digest/error detail, without failing Open.
// A malformed known document or a contradictory exact set fails Open. Git
// directories and colony markers are checked before operations. Reconciliation
// empties disposable tmp directories, removes never-exposed orphans, retries
// durable closures whose directory, branch or anchor remains, restores missing
// open anchors when their objects survive, and reports missing scratches and
// unfinished repairs. Pruning runs once per usable workspace, not per closed
// scratch. Reconciliation writes no record and reads no clock. Unrecorded
// anchors and unretained capture/generation/scratch-commit refs are swept in usable
// repositories. An orphan's regular .git file identifies its registered workspace
// by os.SameFile; exactly one matching UUID-suffix anchor proves its base.
// Missing or ambiguous proof permits directory removal only (and pruning only
// in a verified workspace); the branch is retained and named in Pending. A moved
// orphan branch is also retained and reported. Anchors for still-present orphan
// directories survive the stray sweep, preserving retry proof. Stray branches
// without directories are never swept. Every ref deletion uses an old-value CAS.
//
// Close drains operations, logical writers and outstanding physical leases,
// rejects later service calls with ErrClosed, and is idempotent. Existing writers
// may still finish their leased tools and observation while shutdown waits.
// The host closes the store separately.
// appender.ErrUncertain or appender.ErrPoisoned poisons the service: every later
// call except Close returns ErrPoisoned. Recovery requires the host to recover
// the store and open a new service, never an in-memory guess about a commit.
// Projections retain historical request identities and scratch names, UUID-only
// generation, capture and lineage IDs, and each inputs revision's digest and
// exact body_refs set; the latter is bounded by recorded inputs revisions.
// Memory grows with history, including merge-back plans and passive notices.
// Superseded input bodies are read only by an explicit retry whose preparation
// pin retains them, never for identity checks. Other facts are per-scratch live
// counts, current manifest pins and each active report pin's owning scratch.
// Observation IDs remain UUID-only historical facts after scratch closure,
// except for observations directly named by admitted provenance.
//
// # Removal boundary
//
// CloseScratch proves a scratch has no unrepresented eligible edits before
// durably recording scratch_closed. A dedicated scratch compares with its
// represented sealed tree, initially the anchor, using anchor, private-tip and
// represented names as trusted eligibility. Admission and private observation
// alone never advance representation. A temporary's abandoned closure instead
// compares to its acknowledged private tip, initially its fork tree.
// Unrepresented edits give ErrUnrepresentedEdits. An owner or the bound author
// may close it.
// Authority, live requests and open/pending children are checked before and
// under exclusion; they give ErrNotBound, ErrLiveRequests or ErrTemporaryChildren.
// Live writers give scratch_busy. Closure records scratch_closed followed by all
// still-active observation and F2-06 report unpins in original pin order.
// Ignored entries are scratch-local and discarded;
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
// # Capacity profile and measurements
//
// CapacityProfile is Core's capture and checkpoint capacity authority, extended
// by C4 rather than replaced with a second profile. MaxFiles, MaxBytes,
// CheckpointsPerPair and GenerationWindow must be positive; BytesInFlight must
// be at least MaxBytes. Otherwise configuration gives ErrInvalid. A nil
// Config.Capacity gives no_capacity_profile. ProvisionalCapacityProfile returns
// 100,000 files, 1 GiB per capture, 20 GiB in flight, four live checkpoints per
// pair and a four-generation workspace window, enforced over each train.
// These are calibration inputs, not measured capacity, a colony cap or a
// service level.
//
// Reads are sequential in v1. The opt-in DWARF_CAPTURE_WORKLOAD_OUT harness
// records creation, fresh and warm observe, a seal after ten edits, closure
// observation, root and successor admission, two integrations and their rebuild
// after a same-tree main advance, plus observe_task, temporary_first_write and
// observe_temporary, for 1,000 and 10,000 roughly 800-byte files. Each row includes
// manifest bytes, Git's loose object count and KiB, and retained scratch/body
// footprint. Service observations compare files, index, refs and ledger against
// independent Git outside the timers. Results and work
// directories stay outside the repository. It skips under -race, leaves
// Defender enabled and adds no exclusions, compares trees with reference Git,
// and has no timing threshold. Burst concurrency remains C4's feasibility gate.
// Windows long paths Git cannot list give unobservable_path; sharing violations
// give unreadable. With filemode=false, tracked executable modes are preserved
// and new files get 100644. Linux and macOS CI require Git at least 2.45.
//
// # Loose objects
//
// Observe and refused captures can leave loose objects. Core runs no object
// maintenance: no gc, prune or repack. Git worktree prune only removes stale
// worktree registration, not objects. The harness measures object cost rather
// than assuming automatic collection; maintenance policy is revisited at C4's
// capacity gate, not silently applied to the owner's repository.
package workspace
