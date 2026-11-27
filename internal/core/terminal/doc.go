// Package terminal is the plain standard-library line terminal on Core's
// session seam. It imports only internal/core/session, contract packages and
// the standard library; the executable composes it with Core. It uses
// buffered line input and scrollback output: no raw mode, terminal library,
// alternate screen or color.
//
// DecodeSettings reads the host's external, nonsecret settings: one strict
// UTF-8 JSON object of at most 1 MiB with schema 1, contexts and
// credential_bindings. Unknown members, null, duplicate members, string or
// fractional numbers and out-of-range values refuse with the member path and
// allowed form, never the value. A missing turn_policy member takes
// DefaultPolicy's testing value (16 model calls, 64 tool calls, 600,000 ms,
// 64 KiB arguments and results, 8 discovery entries, 4,096 search entries,
// 100 matches, 8 MiB live text); these are safety settings, not money,
// capability or measured fit. A context without task_binding must be titled
// "General work". Source locators stay with the host: WriteStartup and
// CredentialBinding's formatting print only scope, provider and versions.
// The host selects either these legacy settings or managed Core setup, never
// both. ConfiguredContext and Configuration convert only the view; no settings
// file is generated or imported, and managed starts need no --settings.
//
// Run serves the given Sessions. Plain text sends message.send to the
// selected context with fresh command and turn IDs; /context <agent>
// <context>, /status <turn>, /cancel <turn>, /service [agent], /outages,
// /send <command JSON> and /quit are the commands, and any other /command refuses
// locally. /send decodes one strict schema-1 command (DecodeCommand) whose JSON
// escapes carry multiline text and artifact references; nothing is shell-evaluated.
// Commands run one at a time in arrival order. Every displayed line carries the
// short [agent/context/turn] identity of the event, turn or command it reports,
// never the current selection, and escapes controls, ANSI and OSC sequences,
// carriage returns, format characters and invalid UTF-8 while keeping
// printable Unicode; each line of multiline text is labelled.
//
// Held sends show session.ErrEligibilityHeld's fixed class and the copied gate's
// state, reason, version, operation and through-sequence. The same gate appears
// at attachment, on /status <turn>, on context.status sent through /send, and on
// eligibility events. A redacted view shows only the fields Core supplied;
// terminal never reconstructs a missing declaration. An available first action
// shows its initial/permitted input and admission identities. When Disposition
// is supplied, it also shows available first-admitted identity and source sequences;
// Disposition, not retained Refusal metadata, controls admission-refusal display.
// Events have no derived disposition or sequences and print neither; status
// remains the source of those facts.
// Eligibility is separate from the saved phase/wait and recovery availability.
// Only the trusted owner's Core Service transitions set or release the gate;
// there is no terminal eligibility command or automatic resend on release.
//
// /service, /outages and their passive /send equivalents use the same bounded
// identifier formatting as WriteServiceStatus at startup. Lost service prints
// as service_loss, separately from eligibility and saved turn state. No service
// command sets state or sends work. Retained-source and recovery errors remain
// visible; full source provenance, object digests and cursors are not printed.
// Colony notices from every Session are deduplicated by NoticeID; joined members
// by (OutageID, AgentID, generation). Each refresh lists the full affected set at
// one head, passing Core's Next cursor back unchanged. A stale page restarts the
// listing at most twice; if the third attempt changes, its obtained rows print
// with a partial-list warning and an explicit /outages retry.
// The host's normal startup and typed refusals print the Service snapshot before
// Run or the existing refusal line. A missing snapshot is never shown as healthy.
//
// Run takes one live subscription per active turn of every agent, not only the
// selection. Provisional text prints as "live (provisional):" and is never the
// answer; after the recorded ending and the end of provisional text, a
// finished turn's durable answer prints as "final (durable):" from
// Session.FinalText. A subscription that falls behind reattaches and prints
// only the suffix beyond what it displayed. Turns recovered from an earlier
// run show as unresumable; no resume is offered.
//
// An interrupt while the selected context has an active turn requests its
// cancellation; at an idle prompt it detaches. /quit, end of input or an idle
// interrupt first finishes the commands already read and the durable answers
// already being fetched (another interrupt skips that), then detaches every
// view and runs the host's Drain steps in order; a refused Config still runs
// Drain. On interactive input an end-of-input key read while the selected
// turn is active pauses reading until that turn ends, because a console
// interrupt can also end a pending read.
package terminal
