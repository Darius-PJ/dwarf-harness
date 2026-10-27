// Package credentials resolves configured sources once into owned, bounded
// memory. Sources are host configuration, never agent input. Environment selects
// a variable and Native selects a Windows Generic Credential target; neither
// reads until Bind. A Secret retains no source reference, so borrowing cannot
// silently reread a mutable source. Replay and credential authorization belong
// to Core's agents service, not this package.
//
// Environment values are nonempty visible ASCII without whitespace or controls,
// at most 4,096 bytes. Native generic blobs use UTF-16LE without a terminator,
// restricted to the same header-safe ASCII, at most 1,280 decoded bytes. Native
// uses CredReadW/CredFree on Windows/amd64 and refuses explicitly elsewhere;
// there is no fallback source or normalization of invalid bytes.
//
// Concurrent WithBytes callbacks each borrow an owned copy under a read lock.
// The copy is cleared before that borrow ends; callbacks must neither retain it
// nor call Release. Release waits for every borrower, overwrites the retained
// buffer and makes later borrows fail. Secret formatting redacts the value and
// JSON serialization refuses. Overwriting owned buffers is not a secure-erasure
// claim about Go strings, syscall allocations, HTTP, TLS or operating-system
// copies. No credential value, derived digest or length is a durable identity.
package credentials
