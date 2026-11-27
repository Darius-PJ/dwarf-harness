// Package localipc owns owner-local native endpoints, peer checks, advisory
// discovery and detached spawning. Only a colony Claim holder may Listen,
// PrepareRun, Publish or remove stale endpoint material. Discovery is not
// authority. Unix peers are checked before reading; Windows clients are
// impersonated after the bounded hello, before any welcome or Core effect.
// Restricted tokens and different sign-in sessions are refused. Same-owner
// unrestricted programs are not a sandbox boundary. No remote or TCP fallback
// exists. Unsupported native platforms refuse explicitly.
package localipc
