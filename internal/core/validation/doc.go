// Package validation implements C4's synchronous Windows Go engine. It consumes
// frozen F4-17 values, workspace-owned exact subjects and host-owned body bytes;
// it appends no records, grants no authority and cannot promote a workspace.
// New and Run refuse unsupported systems before filesystem or process effects.
//
// # Admission and lifetime
//
// A caller supplies the explicit nested workspace.ValidationCapacity and reserves
// the job's lane and aggregate resources before Run. The engine enforces its
// per-job materialization, output and process limits, checks CPU/memory requests
// against that profile, and never supplies a production capacity default. Memory
// and CPU are admission reservations, not native enforcement or measurements.
// Process limits and whole-tree closure belong exclusively to gitprocess.
//
// Attempt.Subject is an opaque value resolved by the same workspace Service.
// Its descriptor must equal the queued subject. Export verifies source currency,
// copies the exact tree under pinned C2 conversion inputs into a history-free
// private directory, re-normalizes its contents, then removes the private Git
// area. No workspace ref or hold survives export. Mutable scratch contents and
// its index never supply validation bytes.
//
// Prepared receives detached manifests and attempt_started inputs after export,
// overlay and environment preparation. S3 must durably publish those inputs and
// launch intent before returning nil; its callback may refuse launch. Started
// reports the actual native root PID, not the suite PID. Each attempt gets one
// gitprocess Owner and one Run; Close is mandatory even after a launch error.
// A Result is opaque and synchronous. Finish, Inputs and Bodies return copies;
// the result is evidence, never a reusable pass capability. S3 performs source,
// activation, deadline and attempt-binding guards before terminal bookkeeping.
// Release removes only the originally owned run-root identity, after the caller's
// durable terminal bookkeeping and proven native closure. Unconfirmed closure
// retains the run root/fences for recovery and Release refuses.
//
// # Environment and overlay
//
// The full toolchain and dependency manifests are verified against real regular
// files before each attempt; their path sets must match, not merely a sampled
// executable or lock file. The executable is absolute, inside the pinned
// toolchain, hashed and build-version checked. OS/arch must match the host;
// declared Windows loader values must equal the host values. Manifest visitors
// are provisional until LoadFileManifest verifies the bounded body's digest.
// Unsupported configuration is unavailable, never silently repaired/downloaded.
// Local or quoted replacement targets in go.mod, go.work in the working
// directory, undeclared go.sum, missing dependency inputs and unsupported host
// values refuse before launch. A valid unquoted module/version replacement can
// use only the already pinned dependency cache.
//
// Every attempt has fresh home, temporary, Go temporary, module cache, GOPATH and
// build-cache directories. Dependency inputs are copied and rehashed into that
// fresh module cache. The child environment is constructed from nothing, sorted
// by variable name, with only the frozen allowlist and fixed Go controls:
// CGO_ENABLED=0, GOENV=off, GOTOOLCHAIN=local, GOFLAGS empty, GOWORK=off,
// GOPROXY=off, GOSUMDB=off and GOVCS=*:off. Parent PATH, GOFLAGS, credentials and
// proxy settings are not inherited. cmd/go may derive its suite environment
// (including its own toolchain bin directory); that is not parent inheritance.
// These settings disable Go dependency acquisition, not arbitrary test network
// access: this engine is not C10 containment or protection against same-user
// filesystem races, hostile test code, nonce reading or forged suite output.
//
// Defining bodies overwrite subject copies and the generated canary is added
// only at its reserved path. Pins are read-only and hashed before launch (again
// after Prepared) and after positive closure. A changed/missing/unreadable pin
// cannot pass. The template body is reread after closure as well. Windows does
// not expose Git's executable bit; manifest modes remain the declared Git modes.
//
// # Go events and outcomes
//
// Invocation is the absolute pinned executable with argv:
// test -json -count=1 -p=1 -vet=off -timeout=<wall duration>
// -run=^(<sorted regexp-quoted top-level inventory names plus canary>)$ <package>.
// There is one explicit package and one expected import path. All inventoried
// subtest parents must be present. An emitted test/example/subtest outside the
// inventory is invalid output, including an unlisted child of a selected parent;
// unrelated top-level tests do not run. Fuzz targets and wider package selection
// are unsupported in v1.
//
// The streaming reducer rejects malformed/duplicate/unknown JSON members,
// truncated lines, unknown actions, duplicate or missing transitions, unexpected
// packages/nodes and inconsistent parent transitions. Test-event members are
// Time, Action, Package, Test, Elapsed, Output, OutputType, FailedBuild, Key,
// Value and Path. OutputType is allowed only on output, with the closed values
// empty, frame, error and error-continue; error text never substitutes for a fail
// terminal. attr (Key/Value) and artifacts (Path) are informational only while
// their inventoried test is running. Artifact paths are not opened or collected.
// Build events admit only ImportPath, Action and Output; build-fail plus any
// executed test is inconsistent. Unknown actions and other values fail closed.
// A bounded shared stdout/stderr budget reserves manifest/integrity metadata
// first. Overflow cancels the job, retains only the bounded prefix and cannot
// pass. Closure seals collection so even an unjoined writer cannot mutate an
// already returned result.
//
// The canary logs its exact binding and suite PID from TestDwarfCanaryV1, then
// deliberately calls testing.Errorf. Exactly one bound marker in that test's
// output, one run and one fail terminal are required. A missing, ambiguous,
// unbound or unexpectedly passed canary sets runner_suspect. This detects
// accidental bypass and protocol disagreement, not adversarial suite forgery.
//
// Precedence is cancellation/deadline; native closure; integrity; malformed or
// overflowing output; cached output; build failure before execution; no execution;
// canary; package/required terminals; native exit reconciliation; required skip;
// required failure; pass. Non-executing TestMain exit 0 is no_status. A complete
// pass requires every required node passed, the expected canary failure, package
// fail and native exit 1, live tree/root/I/O closure and verified pins. A failing
// required assertion is failed only after those same prerequisites; other
// problems are not relabeled as assertion failure. Summary assumes its caller
// has already grouped identical comparison identities: pass+fail is flaky,
// unknown/interrupted inputs otherwise inconclusive, and homogeneous passes or
// failures retain their respective result.
//
// # Exact digest preimages
//
// All JSON below is compact encoding/json output of typed structs in the stated
// field order, UTF-8, with no final LF. UUIDs use lowercase canonical text;
// digests and nonce use lowercase hexadecimal. Check, environment and inventory
// hashes use only CheckPreimage, EnvironmentPreimage and InventoryPreimage;
// capacity_profile_sha256 remains opaque to this engine.
//
// adapter_sha256 = SHA-256 of {"schema":1,"runner_sha256":R,
// "adapter":"go-test-json","adapter_version":1,"template_sha256":T}.
// R is the current host executable's raw SHA-256; T is SHA-256(CanaryTemplate()).
// The engine compares the queued adapter digest with AdapterDigest(R).
//
// nonce_sha256 = SHA-256 of {"protocol":"go-fail-v1","attempt_id":A,
// "run_id":U,"binding_sha256":B,"template_sha256":T,"nonce":N}.
// N is 32 cryptographically random bytes encoded as 64 hex digits, generated
// only after source export succeeds. CanaryTemplate's first substitution is
// the pinned package name; its second is strconv.Quote of this compact binding.
// canary_sha256 hashes the exact substituted Go source bytes, including its LF.
//
// marker_sha256 = SHA-256 of the UTF-8 bytes
// DWARF_CANARY_V1:<compact nonce-binding JSON>:<positive canonical decimal PID>.
// The testing log's indentation, dwarf_canary_v1_test.go:<line>: prefix and
// final LF are excluded. The marker must occur once in the canary's output;
// the binding must byte-equal this attempt's JSON, not merely decode similarly.
//
// concrete_environment_sha256 = SHA-256 of
// {"schema":1,"variables":[{"name":K,"value":V},...]}.
// Entries include every actual go-tool child environment variable, sorted by
// case-sensitive name; run paths are actual absolute paths, not placeholders.
// No redaction or normalization is applied to this digest's input bytes.
//
// # Manifest artifact and retained bytes
//
// The manifest artifact is compact typed JSON, no LF, in this field order:
// {"schema":1,"argv":["<absolute executable>","test",...],
// "working_directory":"<absolute directory>",
// "environment":[{"name":"<name>","value":"<literal or run path>"},...]}.
// argv contains the exact rendered arguments, environment is the same complete
// sorted list used above, and paths are actual values. Artifact hashes cover
// exact raw bytes. events is raw newline-delimited go-test JSON stdout; stderr
// is raw stderr, both bounded. integrity is the post-closure FileManifest of
// pinned files actually read, incomplete if missing/unavailable. The manifest
// is complete once rendered. Artifacts have unique kinds in lexical order.
// The environment records exactly what the engine passed to go, not go's suite
// derivations. Pinned cmd/go prepends its GOROOT/bin to PATH (the engine passes
// no PATH, so that is the suite's entire PATH), and adds PWD=<package directory>.
// These two derived entries are not inserted into this artifact or its digest.
// MaterializedManifest and OverlayManifest in Inputs are frozen FileManifest
// bodies, separate from artifacts; the latter includes every defining file and
// the substituted canary. Bodies contains artifact bytes for publication, not
// fabricated body-store pins. The caller owns publishing and durable retention.
package validation
