// Package host composes and owns one colony's detached Core. A candidate takes
// the existing ledger writer Claim before publishing an owner-authenticated
// endpoint. Composition orders store, agents, workspace, loop reconciliation,
// credentials and turn-loop enabling. Separately callable stages support direct
// owner commands without enabling the loop. Probe has no recovery/start effects.
//
// Connections expose booting, recovering, ready, quiescing and stopped; closing
// a connection never drains Core. Shutdown is an incarnation-bound RAM claim,
// not an F7 record. It drains agents, workspace and Store in dependency order;
// an unconfirmed close is never reported as stopped. Discovery is advisory.
// Session dispatch joins in the next producer slice; this slice refuses it.
package host
