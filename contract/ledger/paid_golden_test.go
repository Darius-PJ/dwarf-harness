package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"
)

const paidDir = "testdata/f3/13/log"

var paidColony = paidID(1)

func paidID(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("019a1300-0000-7000-8000-%012x", n)) }

var paidTypes = func() Consumption {
	c := maps.Clone(scopedTypes)
	for _, typ := range []string{TypeRuntimePaidRouteConfigured, TypeRuntimeCredentialChanged, TypeRuntimeSpendConfigured, TypeRuntimePaidReserved, TypeRuntimePaidDispatchIntent, TypeRuntimePaidResult, TypeRuntimePaidOverrunAcknowledged, TypeRuntimePaidReceipt} {
		c[typ] = []int{1}
	}
	return c
}()
var paidLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := paidInputs()
	return buildLog(paidDir, paidColony, paidTypes, inputs)
})

func mustPaid(t testing.TB) *goldenLog { t.Helper(); return mustBuild(t, paidLog) }

// These independent framing helpers make real digest preimages; providers tests
// cross-check the resulting history against the production request/quote code.
func paidFrame(b *bytes.Buffer, value []byte) {
	_ = binary.Write(b, binary.BigEndian, uint64(len(value)))
	b.Write(value)
}
func paidRequestHash(r SealedDescriptor, body []byte) Digest {
	var b bytes.Buffer
	for _, s := range []string{"router-provider-request/v1", r.Provider, r.Authority, r.Model, r.Region, r.Method, r.Path, r.RequestedTier, r.Credential.ScopeID.String(), r.Credential.Version.String()} {
		paidFrame(&b, []byte(s))
	}
	_ = binary.Write(&b, binary.BigEndian, uint32(len(r.Headers)))
	for _, h := range r.Headers {
		paidFrame(&b, []byte(h.Name))
		paidFrame(&b, []byte(h.Value))
	}
	paidFrame(&b, body)
	return Digest(sha256.Sum256(b.Bytes()))
}
func paidQuoteHash(q CostBound) Digest {
	var b bytes.Buffer
	for _, v := range [][]byte{[]byte("router-cost-quote/v1"), q.RequestSHA256[:], []byte(q.CapabilityVersion), []byte(q.TariffVersion), []byte(q.TariffSHA256.String()), []byte(q.Currency), []byte(strconv.FormatInt(q.MaximumMicros, 10))} {
		paidFrame(&b, v)
	}
	_ = binary.Write(&b, binary.BigEndian, uint32(len(q.Components)))
	var components [][]byte
	for _, c := range q.Components {
		var part bytes.Buffer
		_ = binary.Write(&part, binary.BigEndian, uint32(len(c.Classes)))
		for _, s := range c.Classes {
			paidFrame(&part, []byte(s))
		}
		for _, s := range []string{strconv.FormatInt(c.MaxUnits, 10), c.RateNumerator, c.RateDenominator} {
			paidFrame(&part, []byte(s))
		}
		components = append(components, part.Bytes())
	}
	slices.SortFunc(components, bytes.Compare)
	for _, c := range components {
		paidFrame(&b, c)
	}
	return Digest(sha256.Sum256(b.Bytes()))
}

func paidInputs() ([]goldenCommit, map[string][]byte) {
	general, work := scopedOriginFixture(), scopedOriginFixture()
	work.ContextID = fid("d2")
	binding := func(o Origin) Binding { return Binding{o.AgentID, o.ContextID} }
	contextTarget := func(o Origin) Resource {
		return Resource{Kind: "context", Binding: binding(o), Scope: "context", Slots: []string{}}
	}
	out := scopedInputs()[:2] // Registration and one work context; no later F3-11 effects.
	var sequence int64
	for i := range out {
		out[i].header.ColonyID = paidColony
		sequence += int64(len(out[i].drafts))
	}
	files := map[string][]byte{}
	for _, text := range []string{scopedInstructions, ""} {
		files["objects/"+scopedHash(text).String()] = []byte(text)
	}
	object := func(o Origin, text string) ObjectRef {
		h := scopedHash(text)
		files["objects/agents/Ada-a1/contexts/"+o.ContextID.String()+"/core/objects/"+h.String()] = []byte(text)
		return ObjectRef{h, int64(len(text))}
	}
	now := time.Date(2026, 10, 24, 23, 59, 0, 0, time.UTC).UnixNano()
	appendCommit := func(ds []Draft) {
		now += int64(time.Second)
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: paidColony, CommitID: paidID(0x2000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	draft := func(o Origin, command, turn uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: o.AgentID, ExecutionSessionID: o.SessionID, ContextID: o.ContextID, OperationID: command, TurnID: turn, Payload: mustEncode(p)}
	}
	addScoped := func(s ScopedSubmission, version int64, outcome string, workID, turn uuid.UUID, effects ...payload) {
		command := paidID(0x1000 + len(out))
		hash := Digest(sha256.Sum256(mustEncode(s)))
		ds := []Draft{draft(s.Origin, command, turn, TypeCommand, Actor(s.Actor), Command{command, hash})}
		for _, p := range effects {
			typ, actor := scopedPayloadType(p), Actor(s.Actor)
			if typ == TypeRuntimeWorkAdmitted || typ == TypeRuntimeWorkEnded || typ == TypeRuntimeLocalUsed || typ == TypeBodyPin {
				actor = ActorCore
			}
			d := draft(s.Origin, command, turn, typ, actor, p)
			for _, effect := range effects {
				if task, ok := effect.(RuntimeTaskChanged); ok && (typ == TypeRuntimeTaskChanged || typ == TypeBodyPin) {
					d.BodyRefs = []Digest{task.Description.SHA256}
				}
			}
			ds = append(ds, d)
		}
		ds = append(ds, draft(s.Origin, command, turn, TypeRuntimeScopedReceipt, Actor(s.Actor), RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: hash, Submission: s, Outcome: outcome, Version: version, WorkID: workID}))
		appendCommit(ds)
	}
	addPaid := func(s PaidSubmission, version int64, outcome string, attempt, turn uuid.UUID, typ string, effect payload, usage *PaidLocalUsage) int64 {
		command := paidID(0x1000 + len(out))
		hash := Digest(sha256.Sum256(mustEncode(s)))
		actor := Actor(s.Actor)
		effectActor := actor
		if s.Operation == "paid.admit" {
			effectActor = ActorCore
		}
		r := RuntimePaidReceipt{CommandID: command, SubmissionSHA256: hash, Submission: s, Outcome: outcome, Version: version, AttemptID: attempt, Usage: usage}
		appendCommit([]Draft{draft(s.Origin, command, turn, TypeCommand, actor, Command{command, hash}), draft(s.Origin, command, turn, typ, effectActor, effect), draft(s.Origin, command, turn, TypeRuntimePaidReceipt, actor, r)})
		return sequence - 1
	}
	limits := Capacity{2, 8, 134217728, 1000, 4194304}
	for _, scope := range []ScopeKey{{Kind: "colony"}, {"agent", general.AgentID}, {"context", general.ContextID}, {"context", work.ContextID}} {
		o := general
		if scope.ID == work.ContextID {
			o = work
		}
		addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: o, Operation: "capacity.configure", Target: contextTarget(o), Arguments: CapacityArguments{scope, limits}}, 1, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeCapacityConfigured{Scope: scope, Version: 1, Limits: limits})
	}
	taskID, assignment := paidID(0x300), paidID(0x301)
	description := "Synthetic paid accounting task.\n"
	desc := ObjectRef{scopedHash(description), int64(len(description))}
	files["bodies/"+desc.SHA256.String()] = []byte(description)
	task := RuntimeTaskChanged{TaskID: taskID, Action: "create", Version: 1, CreatorActor: "owner", Creator: general, Description: desc, AcceptanceSHA256: scopedHash("Synthetic settlement evidence."), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{general.AgentID}, Assignment: &Assignment{assignment, 1, binding(work), "owner", general}, State: "waiting"}
	addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: general, Operation: "task.create", Target: Resource{Kind: "task", TaskID: taskID}, Arguments: TaskCreateArguments{taskID, desc, task.AcceptanceSHA256, task.Dependencies, task.Audience, &AssignmentInput{assignment, binding(work)}}}, 1, "applied", uuid.UUID{}, uuid.UUID{}, BodyPin{PinID: taskID}, task)
	taskBinding := &TaskBinding{taskID, assignment, 1}
	routeID, credentialScope, credentialVersion := paidID(0x400), paidID(0x401), paidID(0x402)
	rootID, childID, generalTurnGrant, workTurnGrant := paidID(0x410), paidID(0x411), paidID(0x412), paidID(0x413)
	var authority int64
	localScopes := func(o Origin, grant uuid.UUID, parents ...uuid.UUID) []ScopeKey {
		ss := []ScopeKey{{Kind: "colony"}, {"agent", o.AgentID}, {"context", o.ContextID}}
		ids := append([]uuid.UUID{grant}, parents...)
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		for _, id := range ids {
			ss = append(ss, ScopeKey{"grant", id})
		}
		return ss
	}
	issue := func(g RuntimeGrantIssued, actor string, o Origin, parent uuid.UUID) {
		g.ExpectedAuthorityVersion, g.AuthorityVersion = authority, authority+1
		s := ScopedSubmission{Schema: 2, Actor: actor, Origin: o, Operation: "grant.issue", Target: g.Resources[0], GrantID: parent, Expected: ScopedExpected{AuthorityVersion: new(authority)}, Arguments: scopedIssueArguments(g)}
		effects := []payload{g}
		if actor == "agent" {
			effects = append(effects, RuntimeLocalUsed{AuthorityVersion: authority, GrantID: parent, Scopes: localScopes(o, parent), Units: 1})
		}
		authority++
		addScoped(s, authority, "applied", uuid.UUID{}, uuid.UUID{}, effects...)
	}
	root := RuntimeGrantIssued{GrantID: rootID, Grantee: binding(general), Operations: []string{"grant.issue", "model.call"}, Resources: []Resource{{Kind: "external", ResourceKey: "paid-route/" + routeID.String()}}, DataClass: "external", Recipients: []Binding{binding(general), binding(work)}, Limits: limits, DelegationDepth: 1, Lifetime: "until_revoked"}
	issue(root, "owner", general, uuid.UUID{})
	child := root
	child.GrantID, child.ParentGrantID, child.Grantee, child.Operations, child.DelegationDepth, child.TaskBinding = childID, rootID, binding(work), []string{"model.call"}, 0, taskBinding
	issue(child, "agent", general, rootID)
	for _, row := range []struct {
		o       Origin
		id      uuid.UUID
		binding *TaskBinding
	}{{general, generalTurnGrant, nil}, {work, workTurnGrant, taskBinding}} {
		issue(RuntimeGrantIssued{GrantID: row.id, Grantee: binding(row.o), Operations: []string{"turn.start"}, Resources: []Resource{contextTarget(row.o)}, DataClass: "private", Recipients: []Binding{binding(row.o)}, Limits: limits, Lifetime: "until_revoked", TaskBinding: row.binding}, "owner", general, uuid.UUID{})
	}
	task.Action, task.ExpectedVersion, task.Version, task.State = "start", 1, 2, "active"
	addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: general, Operation: "task.start", Target: Resource{Kind: "task", TaskID: taskID}, Expected: ScopedExpected{Version: 1, AssignmentID: assignment, Generation: new(int64(1))}, Arguments: TaskMutationArguments{TaskID: taskID, Action: "start"}}, 2, "applied", uuid.UUID{}, uuid.UUID{}, task)
	owner := func(op string, args any) PaidSubmission {
		return PaidSubmission{Schema: 4, Actor: "owner", Origin: general, Operation: op, Arguments: args}
	}
	bind := RuntimeCredentialChanged{ScopeID: credentialScope, Provider: "anthropic", Version: 1, Action: "bind", CredentialVersion: credentialVersion, SourceKind: "environment", SourceLocator: "SYNTHETIC_F3_13_UNCONFIGURED_SOURCE"}
	addPaid(owner("paid.credential.bind", PaidCredentialBindArguments{bind.ScopeID, 0, bind.Provider, bind.CredentialVersion, bind.SourceKind, bind.SourceLocator, uuid.UUID{}}), 1, "bound", uuid.UUID{}, uuid.UUID{}, TypeRuntimeCredentialChanged, bind, nil)
	profile := paidProfileFixture()
	profile.VerifiedUTCNS, profile.ExpiresUTCNS = now-int64(time.Hour), now+int64(6*24*time.Hour)
	for i := range profile.Sources {
		profile.Sources[i].RetrievedUTCNS = profile.VerifiedUTCNS - 1
	}
	routeVersion := int64(0)
	configureRoute := func() {
		hash, err := profile.SHA256()
		if err != nil {
			panic(err)
		}
		p := RuntimePaidRouteConfigured{routeID, routeVersion, routeVersion + 1, true, credentialScope, profile, now}
		routeVersion++
		addPaid(owner("paid.route.configure", PaidRouteArguments{routeID, p.ExpectedVersion, true, credentialScope, hash}), routeVersion, "configured", uuid.UUID{}, uuid.UUID{}, TypeRuntimePaidRouteConfigured, p, nil)
	}
	configureRoute()
	terms := SpendTerms{Enabled: true, Mode: "finite", CeilingMicros: new(int64(1000000)), PeriodKind: "utc_day", WarningPPM: new(int64(800000))}
	spend := func(scope PaidScope, expected int64, terms SpendTerms, parent uuid.UUID, delegate bool) {
		s := owner("paid.spend.configure", PaidSpendArguments{scope, expected, terms, parent})
		var u *PaidLocalUsage
		outcome := "configured"
		if delegate {
			s.Actor, s.GrantID, s.Operation, outcome = "agent", rootID, "paid.spend.delegate", "delegated"
			u = &PaidLocalUsage{authority, rootID, localScopes(general, rootID), 1, 0}
		}
		p := RuntimeSpendConfigured{scope, expected, expected + 1, terms, parent, authority}
		addPaid(s, p.Version, outcome, uuid.UUID{}, uuid.UUID{}, TypeRuntimeSpendConfigured, p, u)
	}
	for _, scope := range []PaidScope{{Kind: "colony"}, {"agent", general.AgentID}, {"credential", credentialScope}, {"task", taskID}, {"grant", rootID}} {
		spend(scope, 0, terms, uuid.UUID{}, false)
	}
	childTerms := terms
	childTerms.CeilingMicros = new(int64(500000))
	spend(PaidScope{"grant", childID}, 0, childTerms, rootID, true)
	generalTurn, workTurn := paidID(0x420), paidID(0x421)
	for _, row := range []struct {
		o           Origin
		grant, turn uuid.UUID
		task        *TaskBinding
	}{{general, generalTurnGrant, generalTurn, nil}, {work, workTurnGrant, workTurn, taskBinding}} {
		s := ScopedSubmission{Schema: 2, Actor: "agent", Origin: row.o, Operation: "turn.start", Target: contextTarget(row.o), GrantID: row.grant, Arguments: TurnStartArguments{row.turn, row.task}}
		p := RuntimeWorkAdmitted{WorkID: row.turn, Kind: "turn", Version: 1, AuthorityVersion: authority, GrantID: row.grant, Scopes: localScopes(row.o, row.grant), Operation: "turn.start", Target: contextTarget(row.o), TaskBinding: row.task}
		addScoped(s, 1, "admitted", row.turn, row.turn, p)
	}
	reserve := func(n int, taskAttempt bool) RuntimePaidReserved {
		o, grant, turn, boundTask := general, rootID, generalTurn, (*TaskBinding)(nil)
		ss := []PaidScope{{Kind: "colony"}, {"agent", general.AgentID}, {"credential", credentialScope}}
		if taskAttempt {
			o, grant, turn, boundTask = work, childID, workTurn, taskBinding
			ss = append(ss, PaidScope{"task", taskID})
		}
		ss = append(ss, PaidScope{"grant", rootID})
		if taskAttempt {
			ss = append(ss, PaidScope{"grant", childID})
		}
		body := fmt.Sprintf(`{"model":%q,"max_tokens":20,"stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"Synthetic local input.","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"Synthetic continuation.","cache_control":{"type":"ephemeral","ttl":"5m"}}]}],"service_tier":%q}`, profile.ModelID, profile.RequestedTier)
		input := object(o, body)
		ph, err := profile.SHA256()
		if err != nil {
			panic(err)
		}
		th, err := profile.TariffSHA256()
		if err != nil {
			panic(err)
		}
		r := SealedDescriptor{profile.Provider, profile.Authority, profile.ModelID, profile.Region, "POST", profile.Path, profile.RequestedTier, CredentialRef{credentialScope, credentialVersion}, []SemanticHeader{{"accept", "text/event-stream"}, {"anthropic-version", profile.APIVersion}, {"content-type", "application/json"}}, input.SHA256, input.Bytes}
		q := CostBound{Currency: "USD", MaximumMicros: 120, Components: []PricedDimension{{[]string{"input.cache_read", "input.cache_write_1h", "input.cache_write_5m", "input.uncached"}, 100, "1", "1"}, {[]string{"output.generated"}, 20, "1", "1"}}, RequestSHA256: paidRequestHash(r, []byte(body)), CapabilityVersion: profile.CapabilityVersion, TariffVersion: profile.TariffVersion, TariffSHA256: th}
		q.QuoteSHA256 = paidQuoteHash(q)
		p := RuntimePaidReserved{paidID(n), 1, authority, grant, turn, boundTask, routeID, routeVersion, ph, r, q, now, time.Unix(0, now).UTC().Format("2006-01-02"), ss, input}
		s := PaidSubmission{4, "agent", o, "paid.admit", grant, paidAdmitArguments(p)}
		addPaid(s, 1, "reserved", p.AttemptID, turn, TypeRuntimePaidReserved, p, nil)
		return p
	}
	origin := func(p RuntimePaidReserved) Origin {
		if p.TaskBinding != nil {
			return work
		}
		return general
	}
	dispatch := func(p RuntimePaidReserved) {
		s := PaidSubmission{4, "core", origin(p), "paid.dispatch", p.GrantID, PaidAttemptArguments{p.AttemptID, 1}}
		addPaid(s, 2, "sent", p.AttemptID, p.ParentTurnID, TypeRuntimePaidDispatchIntent, RuntimePaidDispatchIntent{p.AttemptID, 1, 2, authority, p.Quote.RequestSHA256, p.Quote.QuoteSHA256, p.Request.Credential, now}, nil)
	}
	result := func(p RuntimePaidReserved, r RuntimePaidResult, label string) (RuntimePaidResult, int64) {
		r.AttemptID = p.AttemptID
		s := PaidSubmission{Schema: 4, Actor: "core", Origin: origin(p), GrantID: p.GrantID}
		outcome := r.Kind
		if r.Kind == "released_unsent" {
			s.Operation, s.Arguments = "paid.release", PaidAttemptArguments{p.AttemptID, r.ExpectedVersion}
		} else {
			r.ProviderRequestID = "synthetic-request-" + p.AttemptID.String()
			if r.ProviderMessageID != "" {
				r.ProviderMessageID = "synthetic-message-" + p.AttemptID.String()
			}
			if r.Usage != nil && r.MessageObjects != nil {
				output := object(origin(p), fmt.Sprintf("{\"kind\":\"paid_output\",\"text\":%q}", "Synthetic output: "+label))
				r.MessageObjects = []ObjectRef{output}
				*r.OutputStarted = true
			}
			stopReason, category := "end_turn", ""
			if r.Kind == "unknown" {
				stopReason = ""
			}
			if r.NoCharge != nil || r.ErrorClass == "refused" {
				stopReason = "refusal"
			}
			if r.ErrorClass == "refused" {
				category = "bio"
			}
			observation := object(origin(p), string(mustJSON(struct {
				Kind              string            `json:"kind"`
				Synthetic         bool              `json:"synthetic"`
				Label             string            `json:"label"`
				ProviderMessageID string            `json:"provider_message_id,omitempty"`
				Complete          bool              `json:"complete"`
				OutputStarted     bool              `json:"output_started"`
				StopReason        string            `json:"stop_reason"`
				Category          string            `json:"category,omitempty"`
				Usage             *PaidUsage        `json:"usage,omitempty"`
				NoCharge          *NoChargeEvidence `json:"no_charge,omitempty"`
				ErrorClass        string            `json:"error_class,omitempty"`
				MessageObjects    []ObjectRef       `json:"message_objects,omitempty"`
			}{"paid_observation", true, label, r.ProviderMessageID, r.Kind != "unknown", *r.OutputStarted, stopReason, category, r.Usage, r.NoCharge, r.ErrorClass, r.MessageObjects})))
			r.EvidenceObject, r.EvidenceSHA256 = &observation, observation.SHA256
			if r.Kind == "correction" {
				s.Operation, s.Arguments, outcome = "paid.correct", PaidCorrectArguments{p.AttemptID, r.ExpectedVersion, r.PreviousResultSequence, r.PreviousEvidenceSHA256, r.EvidenceSHA256, observation, r.MessageObjects}, "corrected"
			} else {
				s.Operation, s.Arguments = "paid.observe", PaidObserveArguments{p.AttemptID, r.ExpectedVersion, r.Kind, r.EvidenceSHA256, observation, r.MessageObjects}
			}
			if r.Kind == "usage" {
				outcome = "settled"
			}
		}
		seq := addPaid(s, r.Version, outcome, p.AttemptID, p.ParentTurnID, TypeRuntimePaidResult, r, nil)
		return r, seq
	}
	p := reserve(0x500, true)
	result(p, paidResultFixture("released_unsent"), "unsent")
	p = reserve(0x501, true)
	dispatch(p)
	result(p, paidResultFixture("unknown"), "incomplete before rollover")
	now += int64(2 * time.Minute)
	usage := paidResultFixture("usage")
	usage.ExpectedVersion, usage.Version = 3, 4
	*usage.OutputStarted = true
	usage.Usage.Classes = []PaidUnits{{"input.cache_read", 2}, {"input.cache_write_1h", 3}, {"input.cache_write_5m", 4}, {"input.uncached", 5}, {"output.generated", 6}}
	*usage.CostMicros = 20
	settled, settledSeq := result(p, usage, "late all-class usage in original period")
	// Retrying this exact evidence/command is represented by the existing receipt,
	// not an extra group. S3 replay consumes that receipt for the duplicate path.
	correction := settled
	correction.Kind, correction.ExpectedVersion, correction.Version = "correction", settled.Version, settled.Version+1
	correction.PreviousResultSequence, correction.PreviousEvidenceSHA256 = settledSeq, settled.EvidenceSHA256
	correction.Usage = &PaidUsage{Classes: slices.Clone(settled.Usage.Classes), PricingTier: "standard"}
	correction.Usage.Classes[4].Units = 7
	correction.CostMicros = new(int64(21))
	result(p, correction, "corrected final usage")
	profile.ModelID, profile.RequestedTier, profile.CapabilityVersion = "claude-sonnet-5-5", "auto", "local-cap-2"
	profile.NoChargeRules = []string{"anthropic_refusal_before_output_2026_09_24"}
	configureRoute()
	p = reserve(0x502, true)
	dispatch(p)
	result(p, paidResultFixture("no_charge"), "documented null-category refusal")
	p = reserve(0x503, true)
	dispatch(p)
	billed := paidResultFixture("usage")
	billed.ErrorClass = "refused"
	billed.Usage.Classes[4].Units = 0
	billed.CostMicros = new(int64(1))
	billed.MessageObjects = nil
	result(p, billed, "billed pre-output refusal bio")
	p = reserve(0x504, false)
	dispatch(p)
	overrun := paidResultFixture("usage")
	overrun.Usage.Classes[3].Units = 121
	overrun.CostMicros = new(int64(122))
	overrun.BoundViolation = true
	*overrun.OutputStarted = true
	overrun, overrunSeq := result(p, overrun, "unclamped overrun")
	ack := RuntimePaidOverrunAcknowledged{p.AttemptID, overrun.Version, overrun.Version + 1, overrunSeq, overrun.EvidenceSHA256, p.ProfileSHA256}
	addPaid(owner("paid.overrun.acknowledge", PaidOverrunArguments{ack.AttemptID, ack.ExpectedVersion, ack.ResultSequence, ack.EvidenceSHA256, ack.RetiredProfileSHA256}), ack.Version, "acknowledged", p.AttemptID, uuid.UUID{}, TypeRuntimePaidOverrunAcknowledged, ack, nil)
	profile.CapabilityVersion = "local-cap-3"
	profile.Sources[0].Quote += " Revised synthetic facts."
	configureRoute()
	oldCredential := credentialVersion
	credentialVersion = paidID(0x403)
	bind.ExpectedVersion, bind.Version, bind.CredentialVersion, bind.RevokePreviousVersion = 1, 2, credentialVersion, oldCredential
	addPaid(owner("paid.credential.bind", PaidCredentialBindArguments{bind.ScopeID, 1, bind.Provider, bind.CredentialVersion, bind.SourceKind, bind.SourceLocator, oldCredential}), 2, "bound", uuid.UUID{}, uuid.UUID{}, TypeRuntimeCredentialChanged, bind, nil)
	terms.CeilingMicros = new(int64(900000))
	spend(PaidScope{"credential", credentialScope}, 1, terms, uuid.UUID{}, false)
	unlimited := SpendTerms{Enabled: true, Mode: "unlimited", PeriodKind: "utc_day"}
	spend(PaidScope{Kind: "colony"}, 1, unlimited, uuid.UUID{}, false)
	p = reserve(0x505, false)
	dispatch(p)
	result(p, paidResultFixture("usage"), "unlimited scope still finite quote")
	revoke := RuntimeCredentialChanged{ScopeID: credentialScope, Provider: "anthropic", ExpectedVersion: 2, Version: 3, Action: "revoke", CredentialVersion: credentialVersion}
	addPaid(owner("paid.credential.revoke", PaidCredentialRevokeArguments{credentialScope, 2, "anthropic", credentialVersion}), 3, "revoked", uuid.UUID{}, uuid.UUID{}, TypeRuntimeCredentialChanged, revoke, nil)
	return out, files
}

func TestGoldenF3_13(t *testing.T) {
	t.Parallel()
	g := mustPaid(t)
	_, files := paidInputs()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(paidDir)
	if *update {
		for name, data := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("CR byte in %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: paidTypes})
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]bool{}
	profiles := map[Digest]Profile{}
	results := map[int64]RuntimePaidResult{}
	for _, c := range commits {
		for _, r := range c.Records {
			if !Defined().Contains(TypeVersion{r.Type, r.TypeVersion}) {
				t.Fatalf("unregistered type %s", r.Type)
			}
		}
		last := c.Records[len(c.Records)-1]
		if last.Type != TypeRuntimePaidReceipt {
			continue
		}
		if len(c.Records) != 3 || c.Records[0].Type != TypeCommand {
			t.Fatal("paid group is not C,effect,R")
		}
		receipt, err := DecodeRuntimePaidReceipt(last.Payload)
		if err != nil {
			t.Fatal(err)
		}
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil || command.CommandID != receipt.CommandID || command.SubmissionSHA256 != receipt.SubmissionSHA256 {
			t.Fatal("command/receipt mismatch")
		}
		effect := c.Records[1]
		decoded, err := decodeF3_13(effect.Type, effect.Payload)
		if err != nil {
			t.Fatal(err)
		}
		shapes[receipt.Submission.Operation+"/"+receipt.Outcome] = true
		for i, r := range c.Records {
			actor := Actor(receipt.Submission.Actor)
			if i == 1 && receipt.Submission.Operation == "paid.admit" {
				actor = ActorCore
			}
			o := receipt.Submission.Origin
			if r.Actor != actor || r.AgentID != o.AgentID || r.ExecutionSessionID != o.SessionID || r.ContextID != o.ContextID || r.OperationID != receipt.CommandID || r.TurnID != effect.TurnID || r.BodyRefs != nil || r.MeasurementPath != "" {
				t.Fatal("paid envelope mismatch")
			}
		}
		checkObject := func(o ObjectRef) []byte {
			t.Helper()
			path := "objects/agents/Ada-a1/contexts/" + effect.ContextID.String() + "/core/objects/" + o.SHA256.String()
			data, ok := files[path]
			if !ok || int64(len(data)) != o.Bytes || Digest(sha256.Sum256(data)) != o.SHA256 {
				t.Fatalf("object mismatch at %s", path)
			}
			return data
		}
		switch p := decoded.(type) {
		case RuntimePaidRouteConfigured:
			hash, err := p.Profile.SHA256()
			if err != nil {
				t.Fatal(err)
			}
			if hash != receipt.Submission.Arguments.(PaidRouteArguments).ProfileSHA256 {
				t.Fatal("profile digest mismatch")
			}
			profiles[hash] = p.Profile
		case RuntimePaidReserved:
			profile, ok := profiles[p.ProfileSHA256]
			if !ok {
				t.Fatal("missing admission profile")
			}
			tariff, _ := profile.TariffSHA256()
			if paidRequestHash(p.Request, checkObject(p.InputObject)) != p.Quote.RequestSHA256 || paidQuoteHash(p.Quote) != p.Quote.QuoteSHA256 || tariff != p.Quote.TariffSHA256 || p.ParentTurnID != effect.TurnID {
				t.Fatal("reservation hash or turn mismatch")
			}
		case RuntimePaidResult:
			if p.EvidenceObject != nil {
				checkObject(*p.EvidenceObject)
			}
			for _, o := range p.MessageObjects {
				checkObject(o)
			}
			if p.Kind == "correction" {
				previous, ok := results[p.PreviousResultSequence]
				if !ok || previous.AttemptID != p.AttemptID || previous.EvidenceSHA256 != p.PreviousEvidenceSHA256 {
					t.Fatal("correction lost previous result")
				}
			}
			results[int64(effect.Seq)] = p
		case RuntimePaidOverrunAcknowledged:
			p0, ok := results[p.ResultSequence]
			if !ok || !p0.BoundViolation || p0.EvidenceSHA256 != p.EvidenceSHA256 {
				t.Fatal("acknowledgement lost violation")
			}
		}
	}
	for _, shape := range []string{"paid.route.configure/configured", "paid.credential.bind/bound", "paid.credential.revoke/revoked", "paid.spend.configure/configured", "paid.spend.delegate/delegated", "paid.admit/reserved", "paid.dispatch/sent", "paid.release/released_unsent", "paid.observe/unknown", "paid.observe/settled", "paid.observe/no_charge", "paid.correct/corrected", "paid.overrun.acknowledge/acknowledged"} {
		if !shapes[shape] {
			t.Fatalf("missing %s", shape)
		}
	}
	t.Logf("commits=%d records=%d shapes=%s", len(commits), cursor, strings.Join(slices.Sorted(maps.Keys(shapes)), ","))
}
