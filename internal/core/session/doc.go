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
// turn.cancel's accepted outcome means its stop was recorded, not that the
// turn has ended. too_late means a native ending already committed and writes
// nothing. TurnView exposes copied saved phase/wait, owner version, operation,
// generation, pending, deadline and effective counters; recovery_pending is
// current availability, not a rewritten saved phase. Current turn.inspect
// authority controls status disclosure.
// Same-ID message.send retrieval also requires current turn.inspect authority.
//
// A refused message.send can carry a copied EligibilityView in Data. Only
// ErrEligibilityHeld identifies the held refusal (outcome held); the presence
// of Data alone does not. ContextView and TurnView carry the same optional gate
// separately from the saved owner phase and wait. Display State, Reason,
// Version, OperationID and ThroughSequence as supplied, not an inferred stop
// or capacity wait. Context status can expose this gate without context.inspect;
// included turns still require turn.inspect. FirstAction is omitted unless
// its declared context is the queried context and context.inspect is current.
//
// FirstAction retains the initial/permitted bindings and first-admitted identity
// with their source sequences. Disposition is not_admitted, admission_refused,
// admitted_unbound, bound, terminal or unsupported. Only admission_refused uses
// the recorded turn_admission refusal as the current disposition; historical
// refusal metadata can survive a later admission. Post-bind waits and endings
// remain the turn's own state. The exact declared input may run under ordinary
// grants and budgets; completion does not release ordinary work. Eligibility
// changes belong to the trusted owner's Core Service seam, not Session commands.
// Inspection, attachment and reopening never start work; a refused input needs
// a new explicit submission after release.
//
// Since pages the committed records of the view's agent and session after a
// cursor, at most 128 events and 1 MiB per page. Wake closes at the next
// runtime change, so a reader waits without polling. Identity fields come from
// the recorded effect, never from the view. Event types are turn.started,
// tool.started, tool.finished, tool.output (a result-object reference, never
// result bytes), usage, and turn.finished, turn.cancelled or turn.failed;
// runtime.eligibility_changed carries a copied gate under the same current
// disclosure rules as status. Sequence is the source record's own sequence.
//
// LiveText returns a provisional snapshot and a subscription attached at the
// same delta high-water. Deltas are decoded answer text only, never reasoning,
// tool arguments or provider metadata. A subscription that falls behind closes
// with ErrReattachRequired instead of dropping text, a closed view's
// subscriptions end with ErrClosed and a recorded turn ending ends them with
// ErrFinished; an implementation error ends them when the turn's live limit is
// exceeded or its ending cannot be recorded. Once a turn has ended, LiveText
// returns its outcome and durable final-message reference, never the
// provisional text. FinalText returns the durable answer text of a finished
// turn of the view's own agent and session; every other turn, including an
// unended or unfinished one, is refused alike. Closing a Session detaches its
// views; it neither cancels turns nor stops Core.
//
// The seam has been exercised in process, by the terminal and tests against
// loopback providers, and by one owner-authorized live OpenAI drill (one tool
// turn; gpt-6-luna; two calls; one tool); no live Anthropic call has gone
// through it.
package session
