// Package setup implements the owner's reviewed configuration operations and
// the F3-150 configuration fold. It grants no authority, admits no turn, creates
// no implicit capacity or spend terms, and never calls a provider.
//
// Review accepts the strict JSON form of the corresponding Input type, reads
// native facts, allocates immutable command/object identities and expands the
// finite context policy. A context selector on route set, credential bind or
// task create resolves the configured forward references into the review.
// Every apply request is then built from that review alone. Artifact bytes are
// embedded, with their exact size and SHA-256, so retrying needs no source file.
// No input contains a secret. Credential binding delegates source resolution
// to agents; its review explicitly discloses the later-start reread.
//
// WriteReview creates new files with 0o600 and never replaces a file. On POSIX
// the mode is owner-only; Windows ignores the modes, and the ACL the directory
// inherits applies, exactly as for colony files. The review contains no secret.
// Its digest covers its format/schema, colony identity and directory, operation,
// ordered typed steps, and ordered artifact identities. JSON indentation and
// object-member order are not significant. Apply requires the complete digest,
// verifies every embedded byte before effects, and refuses a changed directory.
// Each native and setup step has its own command ID. Partial application is
// resumed with the original review, not a newly reviewed command.
//
// Services are borrowed from a serialized owner host. Apart from colony create,
// setup neither opens nor closes them. Native workspaces retain native-minted
// IDs. Only an exact revision/digest match completes a workspace's interrupted
// native effect. A duplicate setup command returns its original receipt before
// current native revision, repository or body availability checks.
//
// LoadConfiguration runs immediately after ledgerstore.Open, before agents or
// workspace open. Only a first-commit colony.create group selects managed mode.
// Managed contexts come exclusively from context.set, not native registration.
// Historic turn-policy bounds remain readable; current bounds apply only to
// new configuration. Host-settings bytes remain opaque here for the host's
// versioned decoder. Legacy settings are selected, never imported or merged.
//
// Failure exposes stable safe causes and remediation commands while preserving
// native errors.Is/errors.As chains. Errors and receipts contain no instructions,
// profile/source contents, credential locators or secret-derived material.
package setup
