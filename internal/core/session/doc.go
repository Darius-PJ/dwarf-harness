// Package session is the in-process value seam between Core and an owning
// host's views: agents implements Session, the terminal consumes it and the
// executable composes both. It is an internal Go interface, not a launcher
// protocol or frozen wire format, and it imports only the standard library and
// contract packages.
//
// The trusted host binds a Session to one registered agent and execution
// session; principal and origin are the host's, and a view's focus is never
// authority. A Command uses schema 1, a UUIDv7 command ID and a context of that
// agent and session, and carries exactly one value from the closed set:
// message.send with MessageSend, turn.cancel with TurnCancel, turn.status with
// TurnStatus, context.status with ContextStatus or promotion.request with
// PromotionRequest. Any other pairing is refused before an effect.
//
// CommandResult carries only actual durable sequences: ReceiptSequence and
// EffectSequence name committed records, and queries carry none. Retrying a
// command ID with the same meaning returns its original sequences; a changed
// meaning is refused.
//
// Since pages the committed records of the view's agent and session after a
// cursor, at most 128 events and 1 MiB per page. Wake closes at the next
// runtime change, so a reader waits without polling. Identity fields come from
// the recorded effect, never from the view. Event types are turn.started,
// tool.started, tool.finished, tool.output (a result-object reference, never
// result bytes), usage, and turn.finished, turn.cancelled or turn.failed;
// Sequence is the source record's own sequence.
//
// LiveText returns a provisional snapshot and a subscription attached at the
// same delta high-water. Deltas are decoded answer text only, never reasoning,
// tool arguments or provider metadata. A subscription that falls behind closes
// with ErrReattachRequired instead of dropping text, a closed view's
// subscriptions end with ErrClosed and a recorded turn ending ends them with
// ErrFinished; an implementation error ends them when the turn's live limit is
// exceeded or its ending cannot be recorded. Once a turn has ended, LiveText
// returns its outcome and durable final-message reference, never the
// provisional text. Closing a Session detaches its views; it neither cancels
// turns nor stops Core.
package session
