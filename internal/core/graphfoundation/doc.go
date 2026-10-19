// Package graphfoundation is the explicit Core-to-graph wiring boundary. Its
// artifact reader delegates immutable Git-object access to workspace.Service;
// its appender is the ledger store itself, not a second D50 implementation.
// There is no sequencer callback, observer or background worker. Callers and
// tests drive graph work only through explicit Step calls.
//
// Real Git and C1 tests drive frozen C2 fixture records through incremental
// analysis, all-workspace rebuilds, load confirmation, cadence, analyzer changes
// and supersession. An independent accepted-ref/analyzer oracle checks rebuilt
// topology, coverage and generations without reading graph records or images.
// Paused work leaves Core appends and graph reads available. These are foundation
// proofs, not live accepted-promotion integration (ticket 63), A2 hard containment,
// C4 throughput, U5 readiness or ticket 61's close.
package graphfoundation
