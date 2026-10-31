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
//
// Run serves the given Sessions. Plain text sends message.send to the
// selected context with fresh command and turn IDs; /context <agent>
// <context>, /status <turn>, /cancel <turn>, /send <command JSON> and /quit
// are the commands, and any other /command refuses locally. /send decodes one
// strict schema-1 command (DecodeCommand) whose JSON escapes carry multiline
// text and artifact references; nothing is shell-evaluated. Commands run one
// at a time in arrival order. Every displayed line carries the short
// [agent/context/turn] identity of the event, turn or command it reports,
// never the current selection, and escapes controls, ANSI and OSC sequences,
// carriage returns, format characters and invalid UTF-8 while keeping
// printable Unicode; each line of multiline text is labelled.
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
