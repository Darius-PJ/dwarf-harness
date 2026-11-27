// Package launcher defines the DRAFT owner-local colony-host wire protocol.
// This package is not frozen. DraftVersion numbers incompatible wire changes;
// additive operations, codes and optional fields do not by themselves bump it.
// The separate F7-30 ledger contract, including its meaning version, is not
// defined by or dependent on this draft. There are no draft golden files.
//
// # Framing and strict decoding
//
// A frame is uint32 big-endian payload length followed by exactly that many
// UTF-8 JSON bytes. ReadFrame refuses zero and lengths above MaxFrameBytes
// (1,048,576) before allocation. Only EOF before any length byte is a clean
// detach; partial headers and missing/partial bodies fail. WriteFrame finishes
// short writes and fails zero progress. There is no delimiter, padding or BOM.
//
// Decode accepts one object, rejects duplicate decoded keys recursively, null
// anywhere, trailing data, invalid UTF-8, unpaired surrogate escapes, depth
// above 32, unknown object members and missing required members. All numbers
// are exact nonnegative integers through MaxInt64; decimal/exponent spellings
// are refused without float coercion. UUIDs are canonical lowercase RFC 9562
// version 7; SHA-256 digests are lowercase 64-digit hex. Optional means absent,
// never null, a zero UUID or an empty identity stand-in. Explicit structs and
// their JSON tags define the field lists and writer order; no Go type name is
// sent. The tagged frozen payload is the sole opaque object exception: its
// consumer must validate it with the corresponding frozen ledger decoder.
//
// Encode and Decode are stateless codecs. A network reader must use Decoder,
// not Decode alone: Decoder checks the handshake, direction, connection ID,
// contiguous positive per-direction sequence, distinct live RPC IDs, response
// correlation and contiguous part ordinals. Protocol failures close its state.
// Unknown operations instead return RequestRefusal with the original RPCID and
// invalid_command; they consume that frame sequence, not the connection. The
// caller sends the refusal and retires it with SentResponse. No codec queues
// work, authenticates an OS peer, retransmits a request or cancels Core work.
//
// # Handshake and lifecycle
//
// Hello is {kind:"hello",versions:[DraftVersion],profile:"base",colony_id,
// connection_id}. The connection UUID is correlation, not authority. A server
// authenticates its native peer, validates the claimed colony against the
// lock-held colony, and only then sends Welcome and calls AcceptWelcome.
// Welcome is {kind:"welcome",version,profile,connection_id,host_id,colony_id,
// principal,state,lifecycle_revision,max_frame_bytes}. Principal is exactly
// {kind:"windows_logon",user_sid,authentication_id} or {kind:"unix_uid",uid}.
// Authentication ID is lowercase hex16; UID is uint32. These are the server's
// observations, never client claims. F7-30 stable retry identity ignores the
// Windows authentication ID; the transport's native checks remain separate.
// Refused is {kind:"refused",code}, with unauthorized, incompatible,
// wrong_colony, busy, protocol_error or unavailable, and ends the handshake.
//
// States/revisions are booting/1, recovering/2, ready/3, quiescing/4, stopped/5.
// host_id identifies one process incarnation. Its revision is not a durable
// colony revision. Host status includes startup from ready onwards, containing
// only integrity_issues, pending_paid, unresumable_turns, conditions and
// credentials [{scope_id,provider,version,other_versions}]. The condition tokens
// are process_unconfirmed, private_integrity, service_recovery_pending and
// previous_host_unclean, at most eight. No setting, path, PID or secret is sent.
//
// # Post-welcome frames
//
// Every frame starts {version,connection_id,seq,kind,...}. Each direction starts
// seq at one, contiguous, without wrap. Request continues with rpc_id,
// operation,binding?,expected?,command_id?,arguments. RPC IDs are positive
// connection-local integers, distinct from durable UUID command IDs. Binding is
// {colony_id,agent_id,session_id,context_id}; expected is {kind,id,revision}.
// Presence and expected kinds are per-operation, not one global mutation gate.
// Response continues with rpc_id,code,part,last,result?; part starts at zero.
// host_state continues with host_id,state,lifecycle_revision,code?. text_delta
// continues with subscription_id,turn_id,index,text. text_end continues with
// subscription_id,code. Unknown frame kinds are protocol errors; unknown
// required codes/tags are incompatible, never arbitrary Error() text.
//
// # Operation table
//
// LookupOperation exposes scope, command/expected requirements, accepted
// expected kinds and idempotency class. The source table also selects the
// concrete argument and result structs. It is extendable in later drafts:
// colony-owner operations may use a native receipt without an F7-30 bind.
// This ticket fills only host and session rows. Here "F7" means F7-30 bind;
// "RAM" means host-incarnation-only idempotency; "none" means a passive call.
//
//	operation          scope    command expected       idempotency
//	host.status        host     no      absent         none
//	host.watch         host     no      absent         none
//	host.shutdown      host     yes     host           RAM
//	session.list       host     no      absent         none
//	message.send       session  yes     context        F7
//	turn.cancel        session  yes     continuation   F7
//	promotion.request  session  yes     context        F7
//	turn.status        session  no      absent         none
//	context.status     session  no      absent         none
//	session.events     session  no      absent         none
//	session.wait       session  no      absent         none
//	text.attach        session  no      absent         none
//	text.close         session  no      absent         none
//	text.final         session  no      absent         none
//	service.status     session  no      absent         none
//	service.outages    session  no      absent         none
//
// Host-scoped calls require the authenticated principal from hello/welcome, not
// a session binding. Every session row requires its binding. session.list is
// computed from the host's current enabled set on each call, returning only
// configured rows the principal is authorized to see. It creates no context.
//
// The following argument/result table uses the explicit structs in types.go.
// With code=ok the listed result is required. Other refusal codes may omit the
// result; if present it has the same listed shape, except code=held on the
// three F7 mutations requires HeldResult {eligibility:EligibilityView}. Held
// is refused before binding and writes no F7 record; it has no binding_sequence.
// Bound mutation results always have a positive, actual binding_sequence.
//
//	operation          arguments          result
//	host.status        HostStatus         HostStatusResult
//	host.watch         HostWatch          HostStatusResult
//	host.shutdown      HostShutdown       HostStatusResult
//	session.list       SessionList        SessionListResult
//	message.send       MessageSend        MutationResult or held:HeldResult
//	turn.cancel        TurnCancel         MutationResult or held:HeldResult
//	promotion.request  PromotionRequest   MutationResult or held:HeldResult
//	turn.status        TurnStatus         TurnView
//	context.status     ContextStatus      ContextView
//	session.events     SessionEvents      EventPage
//	session.wait       SessionWait        EmptyResult
//	text.attach        TextAttach         TextSnapshotPart, then TextPart
//	text.close         TextClose          EmptyResult
//	text.final         TextFinal          TextPart
//	service.status     ServiceStatus      ServiceView
//	service.outages    ServiceOutages     ServiceOutagePage
//
// host.watch waits only when after_revision is current. host.shutdown requires
// the current host_id and ready revision, rejecting a prior incarnation. Its
// RAM claim acknowledges intent, not completed closure. It writes no F7 record:
// ticket 16's owner.stop(shutdown) records and the closed Store are its durable
// evidence. S2 implements those effects; this contract only states the class.
//
// SessionList has paired optional after_agent_id/after_context_id, limit 1..128
// and through_seq on subsequent pages. Rows are ordered by agent/context IDs;
// a changed through-sequence refuses stale_page rather than mixing snapshots.
// SessionEvents has after_seq and limit 1..128 and preserves whole commit page
// boundaries. Its token names one connection-local, one-use Wake; session.wait
// consumes it, and detach retires it. ServiceOutages retains the landed passive
// cursor/through_sequence binding, optional filter and 128-row cap. Authorization,
// page assembly, current-enabled-set lookup and token ownership are host duties.
//
// MessageSend preserves turn_id, exact UTF-8 text and artifact order; the frozen
// meaning encoder normalizes only omitted artifacts to []. Artifact source
// sequences and byte counts are positive. TurnCancel uses its turn ID and the
// submitted continuation owner version, not a freshly substituted expectation.
// PromotionRequest retains workspace_id, head and optional restart_from; the
// producer invokes the actual unavailable validation boundary, never a fake
// successful promotion. F7 MutationResult states are bound, in_progress,
// admitted_unbound, recovery_pending and resolved. Only resolved has outcome:
// applied, accepted, too_late or interrupted. Native sequence members name
// actual committed witnesses; omission does not prove that no effect occurred.
//
// # Public projections and event table
//
// TurnView, ContextView, EligibilityView and the service types mirror the landed
// public Session fields explicitly. TurnView has no invented service field;
// ContextView includes the landed service view. Only native ledger payloads
// use RecordPayload {record_type,record_version:1,payload}. Tool output is an
// ObjectRef, not a tagged ledger record. Eligibility and service data are
// explicit structs of the already-authorized projections, never a raw envelope,
// credential, locator, private body or a record handed to a client to redact.
// Producers and consumers validate native payloads with their frozen codecs
// and originating IDs; producers additionally apply current disclosure authority.
//
// Event is {sequence,type,agent_id?,session_id?,context_id?,turn_id?,
// operation_id?,data}. Origin presence is kind-specific, not zero-filled:
//
//	event                         data                          origin
//	turn.started                  runtime.turn_bound v1         full
//	tool.started                  runtime.tool_started v1       full
//	tool.finished                 runtime.tool_result v1        full
//	tool.output                   ObjectRef                     full
//	usage                         runtime.paid_result v1        full
//	turn.finished/cancelled/failed runtime.turn_result v1        full
//	eligibility.changed           EligibilityView               full
//	service.changed               ServiceChanged                agent only
//	outage.joined                 ServiceChanged                agent only
//	outage.began/ended             OutageNotice                  none
//
// Full requires agent, session and context. Agent-only requires agent and
// forbids session/context; none forbids all three. Service/outage kinds also
// forbid turn_id. Native rows use RecordPayload except tool.output. The other
// rows use the explicit projected data type directly. ServiceChanged mirrors
// the landed service-agent change; OutageNotice mirrors the landed public
// incident notice, not a ServiceView substituted for either event. A member
// forbidden for a kind is refused even when the sender knows a valid UUID.
//
// ServiceCauseView omits outage_id when Core has no outage (including pending
// members) and first_source when Core has no member/source (an unsupported
// cause). A conversion maps that absent Core value to omission, not an empty
// source. Every real source retains the landed fields; present zero identities
// or an empty source are refused. No Core projection is changed by this draft.
//
// # Text, bounds and ownership
//
// TextSnapshotPart is part zero of an atomic text.attach result, with text,
// delta_high_water,state,final_message_object? and subscription_id iff the
// landed state is provisional (live). Later parts contain only text; Response
// supplies the ordinal. text.final uses TextPart throughout. ChunkText splits
// UTF-8 text at byte boundaries of at most 65,536, includes metadata once,
// checks full encoded frame overhead and completes with last=true. Queue all
// snapshot parts before forwarding later deltas. Gap or bounded-output failure
// ends/re-attaches that subscription, never silently drops text or stops Core.
// Nontext records are indivisible: host page assembly uses whole records/rows
// and returns too_large when one indivisible result cannot fit.
//
// The host enforces 32 views and 128 combined RPC/wait/subscription/output
// resources per view. Decoder bounds its RPC map but cannot count host-owned
// handles; the host must enforce the combined limit. Transport deadlines,
// peer authentication, enabled-set replacement, five-second handshake and
// incomplete-frame/write guards, draining and native closure belong to the
// host. There is no idle-session shutdown, auto-retry or durable text cursor.
// Detaching a view releases its resources, not the host's work or colony Claim.
package launcher
