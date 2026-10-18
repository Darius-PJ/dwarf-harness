// Package graphfoundation is the explicit Core-to-graph wiring boundary. Its
// artifact reader delegates immutable Git-object access to workspace.Service;
// its appender is the ledger store itself, not a second D50 implementation.
// There is no sequencer callback, observer or background worker. Callers and
// tests drive graph work only through explicit Step calls.
//
// S3 selects incremental work only. Rebuild selection, cadence and the
// rebuild/incremental comparison belong to ticket 61 S4. This package is not
// accepted-promotion product integration (ticket 63), U5 readiness or ticket 61's
// close.
package graphfoundation
