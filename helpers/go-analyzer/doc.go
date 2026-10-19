// Command go-analyzer is D132's separate, pinned Go dependency resolver. Its
// only non-standard direct import is golang.org/x/tools/go/packages v0.50.0.
// It is built by the harness, never by an analyzed repository or a test:
//
//	GOTOOLCHAIN=local GOFLAGS='' CGO_ENABLED=0 go build -trimpath -o OUT/go-analyzer ./helpers/go-analyzer
//
// Windows uses go-analyzer.exe. The trusted host supplies the absolute helper
// and go paths, their actual SHA-256s and versions, GOROOT and the target pair.
// Both complete files must be verified under reserved graph work before launch.
// Host enablement requires the exact tuple's Windows Job descendant proof and
// smoke; another OS/target/toolchain needs its own qualification. This program
// does not establish an OS CPU/RSS limit or a hostile-same-user boundary.
//
// The sole entry is --graph-helper go-analyzer/v1. One strict graph-helper/v1
// request and one response use a big-endian uint32 length plus UTF-8 JSON, at
// most 8 MiB including the prefix. The fixed response envelope and Cost shape
// are unchanged. Exit 0 with a bound frame means joined; exit 2 is emitted only
// before the first go process, for a refused request/version/environment or
// missing telemetry control. Any other exit is unjoined. The helper's 20 s
// deadline precedes the parent's 30 s wall deadline. It waits for go version
// and packages.Load before emitting a response; a parent-forced termination
// retains the directory and fences all graph analysis until host restart.
//
// Load asks exactly NeedName|NeedFiles|NeedImports|NeedDeps|NeedModule for ./...,
// with Tests false, no build flags or overlay, and no syntax, types, export or
// compiled-file mode. Root go.mod is required; there is no GOPATH/synthetic
// fallback. go/parser ImportsOnly associates each immutable file's literal
// imports with its owning native Package.Imports; it never resolves a string.
// Target initializers, generators, tests and binaries are never executed.
//
// The public-field mapping, reconciled by P3, is:
// `Dir`→`Dir`; `Standard`→derived: `Module == nil` and resolved `Dir` under the
// pinned `GOROOT/src` with `PkgPath` equal to the root-relative directory and no
// `Errors`; `Module`/`Error`→`Module`/`Errors`; `DepsErrors` and `Incomplete`→derived
// by the closure walk over `Imports` under `NeedDeps`, where any reachable
// non-exempt package with `Errors`, a `Module.Error`, empty `GoFiles` or an
// identity conflict makes the consumer incomplete; `ImportMap`→already applied;
// `IgnoredGoFiles`→`IgnoredFiles` filtered to `.go`; test lists→inventory minus
// `GoFiles`, every unselected file unknown.
// Every component from GOROOT/src through a standard Dir must be a real
// directory without a reparse point, verified once per invocation. Each
// standard GoFile gets one charged visit and must be a regular, non-reparse
// direct child of that exact verified Dir; otherwise it is not exempt.
//
// Every case-sensitive corpus .go has a file state after successful loading.
// Native-selected usable files are complete only when their whole package
// closure and every literal import resolve. Unknown files publish no partial
// edges or module contributions. Excluded/test/hidden/nested unselected files
// stay unknown, never complete-empty. Native-confirmed in-tree membership
// supplies module versions and dependency-module→consumer-file edges. Standard
// packages under the pinned root are exempt and create no node or edge; an
// outside-tree directory replace and every consumer needing it remain unknown.
// An inside-tree replace is usable only through native selection and containment.
// Scope failures discard all causal output. Consumers use a file entry first,
// otherwise the Go workspace entry; absence of both never means complete.
//
// Both the helper and Config.Env use the complete fixed allowlist: PATH is only
// GOROOT/bin; GOTOOLCHAIN=local, GOENV/GOWORK/GOPROXY/GOPACKAGESDRIVER/GOSUMDB=off,
// GOFLAGS empty, CGO_ENABLED=0, GO111MODULE=on, and an explicit target. GOPATH,
// GOMODCACHE, GOCACHE, temporary/config/home paths are private, initially empty
// directories. Only OS runtime SystemRoot/WINDIR values are retained. The
// library's PWD addition and GO111MODULE=off version probe are part of the
// profile. No ambient driver, compiler, credentials, startup setting or cache
// is borrowed. GOTELEMETRY is not a control: before any native process the
// helper verifies exact bytes "off" in its private UserConfigDir/go/telemetry/mode.
//
// The parent materializes the complete bounded accepted regular-file corpus.
// An omitted .go, go.mod, go.sum, go.work or vendor/ entry makes the whole Go
// workspace unknown without launch, preserving its extraction reason. Other
// omitted files are left to native errors, including unmatched go:embed input.
// No missing blob gets an empty placeholder. Aliases, path escapes, symlinks
// and file/directory collisions are refused. Native metadata never substitutes
// for the immutable inventory or admits outside bytes as a graph fact.
//
// The 1 MiB/blob, 10,000-file/item, 8 MiB corpus/frame and 64 KiB diagnostic
// bounds are unchanged. First-party decoding, parent materialization, copies,
// package/import/member visits, result construction and sorting consume the
// invocation allowance. After Load, a budgeted owned-directory walk charges one
// unit per visited entry, including its root, and no bytes: it only stats.
// It still sums sizes and refuses the existing 64 MiB temporary cap with the
// measured total. Actual corpus/cache/scaffold byte counts are bounded
// diagnostics only; incomplete walks are explicitly labelled. There is no
// wire/API addition. The one-per-256-byte rate applies to framed input/output;
// the parent charges corpus materialization and its corpus-copy staging.
// Native library and tool activity is measured, not bounded by graph charges.
package main
