// Package workspace provides Core's record-free Git, capture-input and private
// worktree mechanics. Ticket 05 S1a writes no ledger record and exposes no
// promotion or capture-only command. The service, capture engine and reservation
// layer are separate slices. Nothing here accepts a promotion or moves main.
//
// # Git and its environments
//
// Core resolves git to an absolute executable path. Git 2.42 or later is
// required: the leading major.minor.patch triple is checked, and the complete
// version text after "git version " (including vendor suffixes) is pinned.
// Missing, old or malformed Git gives ErrGitUnavailable at inspection. Version
// mismatch gives inputs_stale, naming both pinned and running versions. A Git
// upgrade therefore requires the owner's input reapproval before captures can
// resume. The later service may still open when Git is unavailable.
//
// Commands use exec.CommandContext, a five-second WaitDelay and at most 64 KiB
// of stderr. Each call declares its expected exit codes; an unexpected exit is
// a GitError matching ErrGit. Git message text never determines an outcome.
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
// service must repeat this check at registration; scratch creation repeats it
// independently, immediately before creating a private branch.
//
// Proposal contains the absolute common directory, Git version, object format,
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
// # Removal boundary
//
// The service must prove a scratch has no unrepresented eligible edits before
// durably recording its closure. Its only represented tree in ticket 05 is its
// anchor; a capture refusal blocks closure. Ignored files are scratch-local,
// are discarded, and the service's closure result reports their count. A
// missing directory may close. These record/representation checks are not
// bypassed or simulated by this record-free mechanics package.
//
// After durable closure, or for an orphan never handed to a context, Core owns
// removal. A DirEntry/Lstat walk unlinks symlinks and ModeIrregular reparse
// points with os.Remove without descending, descends only real directories,
// and unlinks files. Only then does os.RemoveAll remove the remaining real
// directories. Core never asks git worktree remove --force to traverse an
// unverified directory. Removal failure gives ErrRemovalPending; registration
// is not pruned until removal completes. Then git worktree prune runs, the
// scratch branch is deleted by compare-and-swap only if still at its base, and
// the anchor is deleted. Moved branches retain their history. Closed scratches
// and Open's orphan sweep retry this same primitive; no success is assumed.
// Same-user check/use races remain C10's boundary.
//
// # Later slices
//
// CaptureProfile is the capture subset of CORE:862's CapacityProfile, to be
// extended by ticket 06 and C4, not a second capacity authority. Capture must
// receive an unexported reservation minted only by reserve; exclusive holders
// never wait and busy creates no snapshot. C2 has no validator: no_validator
// is unconditional, with no injectable validator seam, until C4 wires the
// real validator. Replay reads no clock. A maintenance policy for loose objects
// from observe/refused captures remains open for ticket 06 or later, not 05.
package workspace
