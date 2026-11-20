package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"
)

const serviceDir = "testdata/f3/91/log"

var serviceColony = serviceID(0x100)
var serviceTypes = func() Consumption {
	c := maps.Clone(paidTypes)
	for _, typ := range []string{TypeRuntimeServiceObserved, TypeRuntimeServiceCauseChanged, TypeRuntimeServiceAgentChanged, TypeRuntimeServiceOutageNotice, TypeRuntimeServiceReceipt} {
		c[typ] = []int{1}
	}
	return c
}()
var serviceLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := serviceInputs()
	return buildLog(serviceDir, serviceColony, serviceTypes, inputs)
})

func servicePayloadType(p payload) string {
	switch p.(type) {
	case RuntimeServiceObserved:
		return TypeRuntimeServiceObserved
	case RuntimeServiceCauseChanged:
		return TypeRuntimeServiceCauseChanged
	case RuntimeServiceAgentChanged:
		return TypeRuntimeServiceAgentChanged
	case RuntimeServiceOutageNotice:
		return TypeRuntimeServiceOutageNotice
	default:
		panic("unexpected service effect")
	}
}

// The chapter reuses only the service-admissible paid setup, not its later
// accounting examples. New native calls keep their ordinary F3-11/F3-13 groups.
// Every new identity, object and sequence belongs solely to this chapter.
func serviceInputs() ([]goldenCommit, map[string][]byte) {
	prior, priorFiles := loopInputs()
	var out []goldenCommit
	files := map[string][]byte{}
	var sequence int64
	now := prior[0].header.EventTimeNS
	appendCommit := func(ds []Draft) {
		now += int64(time.Second)
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: serviceColony, CommitID: serviceID(0x2000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	for _, c := range prior {
		if slices.ContainsFunc(c.drafts, func(d Draft) bool { return d.Type == TypeRuntimeWorkAdmitted }) {
			break
		}
		appendCommit(c.drafts)
		now = c.header.EventTimeNS
		out[len(out)-1].header.EventTimeNS = now
		for _, d := range c.drafts {
			for path, data := range priorFiles {
				if bytes.Contains(d.Payload, []byte(filepath.Base(path))) {
					files[path] = data
				}
			}
		}
	}
	origins := []Origin{scopedOriginFixture(), {serviceID(0x301), serviceID(0x302), serviceID(0x303)}, {serviceID(0x311), serviceID(0x312), serviceID(0x313)}}
	directories := []string{"Ada-a1", "Bex-a2", "Cy-a3"}
	draft := func(o Origin, command, turn uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: o.AgentID, ExecutionSessionID: o.SessionID, ContextID: o.ContextID, TurnID: turn, OperationID: command, Payload: mustEncode(p)}
	}
	object := func(i int, data []byte) ObjectRef {
		h := Digest(sha256.Sum256(data))
		files["objects/agents/"+directories[i]+"/contexts/"+origins[i].ContextID.String()+"/core/objects/"+h.String()] = data
		return ObjectRef{h, int64(len(data))}
	}
	for i := 1; i < len(origins); i++ {
		o, command := origins[i], serviceID(0x1000+len(out))
		instructions := privateFixtureChange("instructions", "replace", scopedInstructions)
		empty := privateFixtureChange("memory", "replace", "")
		s := struct {
			Schema          int           `json:"schema"`
			Actor           string        `json:"actor"`
			AgentID         uuid.UUID     `json:"agent_id"`
			SessionID       uuid.UUID     `json:"session_id"`
			ContextID       uuid.UUID     `json:"context_id"`
			Operation       string        `json:"operation"`
			Target          PrivateTarget `json:"target"`
			ExpectedVersion int64         `json:"expected_version"`
			Arguments       any           `json:"arguments"`
		}{1, "owner", o.AgentID, o.SessionID, o.ContextID, "agent_register", PrivateTarget{"agent", []string{}}, 0, struct {
			Directory   string `json:"directory"`
			AgentSHA256 Digest `json:"agent_sha256"`
			AgentBytes  int64  `json:"agent_bytes"`
		}{directories[i], instructions.SHA256, instructions.Bytes}}
		h := Digest(sha256.Sum256(mustJSON(s)))
		appendCommit([]Draft{draft(o, command, uuid.UUID{}, TypeCommand, ActorOwner, Command{command, h}), draft(o, command, uuid.UUID{}, TypeRuntimeAgentRegistered, ActorOwner, RuntimeAgentRegistered{directories[i], o.ContextID}), draft(o, command, uuid.UUID{}, TypeRuntimeContextCreated, ActorOwner, RuntimeContextCreated{"general"}), draft(o, command, uuid.UUID{}, TypePrivateRevision, ActorCore, PrivateRevision{"agent", 0, 1, []PrivateChange{instructions, empty}}), draft(o, command, uuid.UUID{}, TypePrivateRevision, ActorCore, PrivateRevision{"context", 0, 1, []PrivateChange{privateFixtureChange("handoff", "replace", ""), empty}}), draft(o, command, uuid.UUID{}, TypeRuntimeCommandReceipt, ActorOwner, RuntimeCommandReceipt{CommandID: command, SubmissionSHA256: h, Operation: "agent_register", Target: s.Target, Outcome: "applied"})})
	}
	addScoped := func(s ScopedSubmission, version int64, outcome string, work, turn uuid.UUID, effect payload) {
		command := serviceID(0x1000 + len(out))
		h := Digest(sha256.Sum256(mustEncode(s)))
		typ := scopedPayloadType(effect)
		actor := Actor(s.Actor)
		if typ == TypeRuntimeWorkAdmitted || typ == TypeRuntimeWorkEnded {
			actor = ActorCore
		}
		r := RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: h, Submission: s, Outcome: outcome, Version: version, WorkID: work}
		appendCommit([]Draft{draft(s.Origin, command, turn, TypeCommand, Actor(s.Actor), Command{command, h}), draft(s.Origin, command, turn, typ, actor, effect), draft(s.Origin, command, turn, TypeRuntimeScopedReceipt, Actor(s.Actor), r)})
	}
	addPaid := func(s PaidSubmission, version int64, outcome string, attempt, turn uuid.UUID, typ string, effect payload) int64 {
		command := serviceID(0x1000 + len(out))
		h := Digest(sha256.Sum256(mustEncode(s)))
		actor := Actor(s.Actor)
		if s.Operation == "paid.admit" {
			actor = ActorCore
		}
		r := RuntimePaidReceipt{CommandID: command, SubmissionSHA256: h, Submission: s, Outcome: outcome, Version: version, AttemptID: attempt}
		appendCommit([]Draft{draft(s.Origin, command, turn, TypeCommand, Actor(s.Actor), Command{command, h}), draft(s.Origin, command, turn, typ, actor, effect), draft(s.Origin, command, turn, TypeRuntimePaidReceipt, Actor(s.Actor), r)})
		return sequence - 1
	}
	limits := Capacity{4, 8, 134217728, 1000, 4194304}
	target := func(o Origin) Resource {
		return Resource{Kind: "context", Binding: Binding{o.AgentID, o.ContextID}, Scope: "context", Slots: []string{}}
	}
	addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: origins[0], Operation: "capacity.configure", Target: target(origins[0]), Arguments: CapacityArguments{ScopeKey{Kind: "colony"}, limits}, Expected: ScopedExpected{Version: 1}}, 2, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeCapacityConfigured{Scope: ScopeKey{Kind: "colony"}, ExpectedVersion: 1, Version: 2, Limits: limits})
	turnGrants := []uuid.UUID{paidID(0x412), serviceID(0x320), serviceID(0x321)}
	paidGrants := []uuid.UUID{paidID(0x410), serviceID(0x330), serviceID(0x331)}
	turns := []uuid.UUID{serviceID(0x340), serviceID(0x341), serviceID(0x342)}
	authority := int64(4)
	for i := 1; i < len(origins); i++ {
		o := origins[i]
		for _, scope := range []ScopeKey{{"agent", o.AgentID}, {"context", o.ContextID}} {
			addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: o, Operation: "capacity.configure", Target: target(o), Arguments: CapacityArguments{scope, limits}}, 1, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeCapacityConfigured{Scope: scope, Version: 1, Limits: limits})
		}
		for _, paid := range []bool{false, true} {
			id, op, res := turnGrants[i], "turn.start", target(o)
			if paid {
				id, op, res = paidGrants[i], "model.call", Resource{Kind: "external", ResourceKey: "paid-route/" + paidID(0x400).String()}
			}
			g := RuntimeGrantIssued{GrantID: id, ExpectedAuthorityVersion: authority, AuthorityVersion: authority + 1, Grantee: Binding{o.AgentID, o.ContextID}, Operations: []string{op}, Resources: []Resource{res}, DataClass: "private", Recipients: []Binding{{o.AgentID, o.ContextID}}, Limits: limits, Lifetime: "until_revoked"}
			if paid {
				g.DataClass = "external"
			}
			addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: o, Operation: "grant.issue", Target: res, Expected: ScopedExpected{AuthorityVersion: new(authority)}, Arguments: scopedIssueArguments(g)}, authority+1, "applied", uuid.UUID{}, uuid.UUID{}, g)
			authority++
		}
		for _, scope := range []PaidScope{{"agent", o.AgentID}, {"grant", paidGrants[i]}} {
			terms := SpendTerms{Enabled: true, Mode: "finite", CeilingMicros: new(int64(1000000)), PeriodKind: "utc_day"}
			p := RuntimeSpendConfigured{Scope: scope, Version: 1, Terms: terms, AuthorityVersion: authority}
			addPaid(PaidSubmission{Schema: 4, Actor: "owner", Origin: o, Operation: "paid.spend.configure", Arguments: PaidSpendArguments{Scope: scope, Terms: terms}}, 1, "configured", uuid.UUID{}, uuid.UUID{}, TypeRuntimeSpendConfigured, p)
		}
	}
	for i, o := range origins {
		ss := []ScopeKey{{Kind: "colony"}, {"agent", o.AgentID}, {"context", o.ContextID}, {"grant", turnGrants[i]}}
		p := RuntimeWorkAdmitted{WorkID: turns[i], Kind: "turn", Version: 1, AuthorityVersion: authority, GrantID: turnGrants[i], Scopes: ss, Operation: "turn.start", Target: target(o)}
		addScoped(ScopedSubmission{Schema: 2, Actor: "agent", Origin: o, Operation: "turn.start", Target: target(o), GrantID: turnGrants[i], Arguments: TurnStartArguments{TurnID: turns[i]}}, 1, "admitted", turns[i], turns[i], p)
	}
	var template RuntimePaidReserved
	paidPrior, paidFiles := paidInputs()
	for _, c := range paidPrior {
		for _, d := range c.drafts {
			if d.Type == TypeRuntimePaidReserved {
				template, _ = DecodeRuntimePaidReserved(d.Payload)
				break
			}
		}
		if template.AttemptID != (uuid.UUID{}) {
			break
		}
	}
	var covered int64
	addService := func(s ServiceSubmission, effects ...payload) int64 {
		command := serviceID(0x4000 + len(out))
		if a, ok := s.Arguments.(ServiceRecordArguments); ok {
			command = a.ObservationID
		}
		h := Digest(sha256.Sum256(mustEncode(s)))
		ds := []Draft{draft(Origin{}, command, uuid.UUID{}, TypeCommand, ActorCore, Command{command, h})}
		refs := make([]ServiceEffect, len(effects))
		for i, p := range effects {
			if n, ok := p.(RuntimeServiceOutageNotice); ok {
				n.MemberSourceSequence = sequence + int64(len(ds))
				p = n
			}
			typ := servicePayloadType(p)
			refs[i] = ServiceEffect{typ, sequence + int64(len(ds)) + 1}
			ds = append(ds, draft(Origin{}, command, uuid.UUID{}, typ, ActorCore, p))
		}
		ds = append(ds, draft(Origin{}, command, uuid.UUID{}, TypeRuntimeServiceReceipt, ActorCore, RuntimeServiceReceipt{command, h, s, "recorded", refs, covered}))
		appendCommit(ds)
		return sequence - int64(len(ds)) + 2
	}
	record := func(e RuntimeServiceObserved) int64 {
		return addService(ServiceSubmission{Schema: 9, Actor: "core", Operation: "service.record", CauseID: e.CauseID, AgentID: e.AgentID, Arguments: ServiceRecordArguments{e.ObservationID, e.CauseKey, e.Cause, e.Source, e.CompletionPermitsObject}}, e)
	}
	state := func(op string, causeID, agent uuid.UUID, cv, mv, gen int64, a ServiceStateArguments, effects ...payload) {
		s := ServiceSubmission{Schema: 9, Actor: "core", Operation: op, CauseID: causeID, AgentID: agent, ExpectedCauseVersion: new(cv), Arguments: a}
		if op != "service.clear" {
			s.ExpectedMemberVersion, s.ExpectedGeneration = new(mv), new(gen)
		}
		addService(s, effects...)
	}
	// Each paid source is captured before its unchanged native result group.
	paidCall := func(i int, success bool, permits []ServiceCompletionPermit) (ServiceSource, func(), *ObjectRef) {
		o := origins[i]
		p := template
		p.AttemptID, p.AuthorityVersion, p.GrantID, p.ParentTurnID, p.TaskBinding = serviceID(0x5000+len(out)), authority, paidGrants[i], turns[i], nil
		p.Scopes = []PaidScope{{Kind: "colony"}, {"agent", o.AgentID}, {"credential", p.Request.Credential.ScopeID}, {"grant", p.GrantID}}
		p.AdmissionUTCNS, p.AdmissionPeriod = now, time.Unix(0, now).UTC().Format("2006-01-02")
		for path, data := range paidFiles {
			if strings.HasSuffix(path, p.InputObject.SHA256.String()) {
				object(i, data)
				break
			}
		}
		addPaid(PaidSubmission{4, "agent", o, "paid.admit", p.GrantID, paidAdmitArguments(p)}, 1, "reserved", p.AttemptID, turns[i], TypeRuntimePaidReserved, p)
		dispatch := RuntimePaidDispatchIntent{p.AttemptID, 1, 2, authority, p.Quote.RequestSHA256, p.Quote.QuoteSHA256, p.Request.Credential, now}
		dispatchSequence := addPaid(PaidSubmission{4, "core", o, "paid.dispatch", p.GrantID, PaidAttemptArguments{p.AttemptID, 1}}, 2, "sent", p.AttemptID, turns[i], TypeRuntimePaidDispatchIntent, dispatch)
		r := paidResultFixture("unknown")
		reason, terminal := "overloaded", ""
		if success {
			r = paidResultFixture("usage")
			reason, terminal = "provider_completed", "end_turn"
			r.MessageObjects = nil
		}
		r.AttemptID, r.ProviderRequestID = p.AttemptID, "synthetic-request-"+p.AttemptID.String()
		if success {
			r.ProviderMessageID = "synthetic-message-" + p.AttemptID.String()
		}
		r.ErrorClass = "overloaded"
		if success {
			r.ErrorClass = ""
		}
		obs := object(i, mustJSON(struct {
			Kind       string    `json:"kind"`
			Synthetic  bool      `json:"synthetic"`
			AttemptID  uuid.UUID `json:"attempt_id"`
			Complete   bool      `json:"complete"`
			StopReason string    `json:"stop_reason"`
			ErrorClass string    `json:"error_class,omitempty"`
		}{"paid_observation", true, p.AttemptID, success, terminal, r.ErrorClass}))
		r.EvidenceObject, r.EvidenceSHA256 = &obs, obs.SHA256
		source := ServiceSource{Kind: "paid_call", ObservationID: serviceID(0x6000 + len(out)), ObservedThrough: sequence, SourceSequence: new(dispatchSequence), Origin: &o, TurnID: turns[i], AttemptID: p.AttemptID, Object: &obs, ObservedUTCNS: now, Reason: reason, Provenance: "provider_response"}
		var permitsObject *ObjectRef
		if success {
			source.Kind = "successful_call"
			source.Completion = &ServiceCompletion{Provider: "anthropic", Terminal: "end_turn"}
			v := object(i, mustJSON(struct {
				Schema          int                       `json:"schema"`
				Kind            string                    `json:"kind"`
				ObservationID   uuid.UUID                 `json:"observation_id"`
				ThroughSequence int64                     `json:"through_sequence"`
				Permits         []ServiceCompletionPermit `json:"permits"`
			}{1, "service_completion", source.ObservationID, sequence, permits}))
			permitsObject = &v
		}
		// The caller journals E now, then emits the native result using this
		// saved closure's data below; no private financial schema is extended.
		finish := func() {
			outcome := "unknown"
			if success {
				outcome = "settled"
			}
			addPaid(PaidSubmission{4, "core", o, "paid.observe", p.GrantID, PaidObserveArguments{p.AttemptID, 2, r.Kind, r.EvidenceSHA256, obs, r.MessageObjects}}, 3, outcome, p.AttemptID, turns[i], TypeRuntimePaidResult, r)
		}
		return source, finish, permitsObject
	}
	grace := serviceCauseFixtures()[4]
	grace.AuthoritySHA256 = scopedHash(template.Request.Authority)
	grace.Model, grace.Region = template.Request.Model, new(template.Request.Region)
	graceID, immediateID := serviceID(0x7001), serviceID(0x7002)
	graceKey, _ := grace.Key()
	immediate := ServiceCauseSpec{Family: "execution", Code: "process_unconfirmed", ScopeKind: "colony"}
	immediateKey, _ := immediate.Key()
	source, finish, _ := paidCall(0, false, nil)
	record(RuntimeServiceObserved{ObservationID: source.ObservationID, AgentID: origins[0].AgentID, CauseID: graceID, CauseKey: graceKey, Cause: &grace, Source: source})
	covered = sequence
	finish()
	due := source.ObservedUTCNS + ServiceGraceNS
	graceMember := RuntimeServiceAgentChanged{AgentID: origins[0].AgentID, CauseID: graceID, Version: 1, Generation: 1, Phase: "pending", FirstSource: source, DueUTCNS: new(due), Source: source}
	state("service.observe", graceID, origins[0].AgentID, 0, 0, 0, ServiceStateArguments{CauseKey: graceKey, Cause: grace, Source: source, FirstSource: &source, DueUTCNS: new(due)}, RuntimeServiceCauseChanged{graceID, graceKey, 0, 1, grace, "active", source}, graceMember)
	outage := serviceID(0x7100)
	members := make([]RuntimeServiceAgentChanged, len(origins))
	for i, o := range origins {
		s := ServiceSource{Kind: "process_refusal", ObservationID: serviceID(0x7200 + i), ObservedThrough: sequence, CommandID: serviceID(0x7300 + i), Origin: &o, TurnID: turns[i], ObservedUTCNS: now, Reason: "process_unconfirmed", ExecutionRunID: serviceID(0x7400)}
		rejected := ScopedSubmission{Schema: 2, Actor: "agent", Origin: o, Operation: "turn.start", Target: target(o), GrantID: turnGrants[i], Arguments: TurnStartArguments{TurnID: serviceID(0x7500 + i)}}
		s.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(rejected)))
		record(RuntimeServiceObserved{ObservationID: s.ObservationID, AgentID: o.AgentID, CauseID: immediateID, CauseKey: immediateKey, Cause: &immediate, Source: s})
		covered = sequence
		m := RuntimeServiceAgentChanged{AgentID: o.AgentID, CauseID: immediateID, Version: 1, Generation: 1, Phase: "lost", FirstSource: s, OutageID: outage, Source: s}
		a := ServiceStateArguments{CauseKey: immediateKey, Cause: immediate, Source: s, FirstSource: &s, OutageID: outage}
		effects := []payload{m}
		cv := int64(1)
		if i == 0 {
			cv = 0
			a.NoticeID = serviceID(0x7600)
			effects = []payload{RuntimeServiceCauseChanged{immediateID, immediateKey, 0, 1, immediate, "active", s}, m, RuntimeServiceOutageNotice{outage, immediateID, "began", a.NoticeID, 1}}
		}
		state("service.observe", immediateID, o.AgentID, cv, 0, 0, a, effects...)
		members[i] = m
	}
	// The third demanded agent joins an already open outage; no second began.
	clear := ServiceSource{Kind: "cause_clear", ObservationID: serviceID(0x7700), ObservedThrough: sequence, ObservedUTCNS: now, Reason: "process_closed", ExecutionRunID: serviceID(0x7400), ExecutionProof: "retained_job"}
	record(RuntimeServiceObserved{ObservationID: clear.ObservationID, AgentID: origins[0].AgentID, CauseID: immediateID, Source: clear})
	covered = sequence
	clearSequence := sequence + 2
	state("service.clear", immediateID, uuid.UUID{}, 1, 0, 0, ServiceStateArguments{CauseKey: immediateKey, Cause: immediate, Source: clear}, RuntimeServiceCauseChanged{immediateID, immediateKey, 1, 2, immediate, "cleared", clear})
	now = due
	expiry := ServiceSource{Kind: "grace_expiry", ObservationID: serviceID(0x7800), ObservedThrough: sequence, ObservedUTCNS: due, Reason: "grace_due"}
	graceMember.ExpectedVersion, graceMember.Version, graceMember.Phase, graceMember.OutageID, graceMember.Source = 1, 2, "lost", serviceID(0x7101), expiry
	state("service.expire", graceID, origins[0].AgentID, 1, 1, 1, ServiceStateArguments{CauseKey: graceKey, Cause: grace, Source: expiry, OutageID: graceMember.OutageID, NoticeID: serviceID(0x7601)}, graceMember, RuntimeServiceOutageNotice{graceMember.OutageID, graceID, "began", serviceID(0x7601), 1})
	for i, o := range origins {
		permits := []ServiceCompletionPermit{{CauseID: immediateID, CauseVersion: 2, MemberGeneration: 1, Kind: "execution_clear", ProofSequence: new(clearSequence)}}
		if i == 0 {
			permits = append([]ServiceCompletionPermit{{CauseID: graceID, CauseVersion: 1, MemberGeneration: 1, Kind: "same_scope"}}, permits...)
		}
		success, finish, permitObject := paidCall(i, true, permits)
		record(RuntimeServiceObserved{ObservationID: success.ObservationID, AgentID: o.AgentID, Source: success, CompletionPermitsObject: permitObject})
		lastObservation := sequence
		finish()
		if i == 0 {
			selected := success
			selected.Completion = new(*success.Completion)
			selected.Completion.Permit = &permits[0]
			graceMember.ExpectedVersion, graceMember.Version, graceMember.Phase, graceMember.Source = 2, 3, "restored", selected
			state("service.complete", graceID, o.AgentID, 1, 2, 1, ServiceStateArguments{CauseKey: graceKey, Cause: grace, Source: selected, OutageID: graceMember.OutageID, NoticeID: serviceID(0x7900)}, RuntimeServiceCauseChanged{graceID, graceKey, 1, 2, grace, "cleared", selected}, graceMember, RuntimeServiceOutageNotice{graceMember.OutageID, graceID, "ended", serviceID(0x7900), 1})
		}
		selected := success
		selected.Completion = new(*success.Completion)
		selected.Completion.Permit = &permits[len(permits)-1]
		m := members[i]
		m.ExpectedVersion, m.Version, m.Phase, m.Source = 1, 2, "restored", selected
		a := ServiceStateArguments{CauseKey: immediateKey, Cause: immediate, Source: selected, OutageID: outage}
		effects := []payload{m}
		if i == len(origins)-1 {
			a.NoticeID = serviceID(0x7901)
			effects = append(effects, RuntimeServiceOutageNotice{outage, immediateID, "ended", a.NoticeID, 1})
		}
		covered = lastObservation
		state("service.complete", immediateID, o.AgentID, 2, 1, 1, a, effects...)
	}
	return out, files
}

func TestGoldenF3_91(t *testing.T) {
	g := mustBuild(t, serviceLog)
	_, files := serviceInputs()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(serviceDir)
	if *update {
		for name, data := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("fixture differs %s: %v", name, err)
		}
		if bytes.ContainsRune(got, '\r') {
			t.Fatalf("fixture CR: %s", name)
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			return fmt.Errorf("unexpected fixture %s", rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	commits, _, err := readAll(serviceDir, ReadOptions{Colony: serviceColony, Through: ToEnd, Consume: serviceTypes})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkServiceGroups(commits, files); err != nil {
		t.Fatal(err)
	}
	var began, restored int
	members := map[uuid.UUID]map[uuid.UUID]string{}
	lateJoin := false
	for _, c := range commits {
		for _, r := range c.Records {
			switch r.Type {
			case TypeRuntimeServiceAgentChanged:
				m, _ := DecodeRuntimeServiceAgentChanged(r.Payload)
				if members[m.CauseID] == nil {
					members[m.CauseID] = map[uuid.UUID]string{}
				}
				members[m.CauseID][m.AgentID] = m.Phase
				if m.CauseID == serviceID(0x7002) && len(members[m.CauseID]) == 3 && m.Phase == "lost" {
					lateJoin = true
					for _, phase := range members[m.CauseID] {
						if phase != "lost" {
							t.Fatal("late join lost membership listing")
						}
					}
				}
			case TypeRuntimeServiceOutageNotice:
				n, _ := DecodeRuntimeServiceOutageNotice(r.Payload)
				if n.Edge == "began" {
					began++
				} else if n.Edge == "ended" {
					restored++
				}
			}
		}
	}
	if began != 2 || restored != 2 || !lateJoin {
		t.Fatalf("incidents: began=%d restored=%d lateJoin=%v", began, restored, lateJoin)
	}
	for _, agents := range members {
		for _, phase := range agents {
			if phase != "restored" {
				t.Fatal("member never completed")
			}
		}
	}
	t.Logf("F3-91 %d commits, sequence %d, %d fixture files", len(commits), commits[len(commits)-1].Last, len(files))
}

// This is the contract fixture's group/binding validator, not the Core planner.
// It deliberately has no clocks, provider calls, eligibility or durable writes.
func checkServiceGroups(commits []Commit, files map[string][]byte) error {
	invalid := func(reason string) error { return fmt.Errorf("%w: F3-91 %s", ErrInvalid, reason) }
	records := map[int64]Record{}
	observations := map[uuid.UUID]RuntimeServiceObserved{}
	causes := map[uuid.UUID]RuntimeServiceCauseChanged{}
	keys := map[Digest]uuid.UUID{}
	members := map[[2]uuid.UUID]RuntimeServiceAgentChanged{}
	notices := map[uuid.UUID]map[string]bool{}
	commands := map[uuid.UUID]bool{}
	var covered int64
	objectBytes := func(ref ObjectRef) ([]byte, error) {
		for name, data := range files {
			if filepath.Base(name) == ref.SHA256.String() && int64(len(data)) == ref.Bytes && Digest(sha256.Sum256(data)) == ref.SHA256 {
				return data, nil
			}
		}
		return nil, invalid("missing exact private object")
	}
	for _, c := range commits {
		last := c.Records[len(c.Records)-1]
		if last.Type != TypeRuntimeServiceReceipt {
			for _, r := range c.Records {
				records[int64(r.Seq)] = r
			}
			continue
		}
		r, err := DecodeRuntimeServiceReceipt(last.Payload)
		if err != nil {
			return err
		}
		if len(c.Records) != len(r.Effects)+2 {
			return invalid("group length")
		}
		if commands[r.CommandID] {
			return invalid("semantic duplicate command")
		}
		commands[r.CommandID] = true
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil || c.Records[0].Type != TypeCommand || command.CommandID != r.CommandID || command.SubmissionSHA256 != r.SubmissionSHA256 {
			return invalid("command binding")
		}
		if r.SourceThrough < covered || r.SourceThrough >= int64(c.First) {
			return invalid("coverage prefix")
		}
		covered = r.SourceThrough
		var member *RuntimeServiceAgentChanged
		var notice *RuntimeServiceOutageNotice
		for i, envelope := range c.Records {
			if envelope.Actor != ActorCore || envelope.OperationID != r.CommandID || envelope.AgentID != (uuid.UUID{}) || envelope.ExecutionSessionID != (uuid.UUID{}) || envelope.ContextID != (uuid.UUID{}) || envelope.TurnID != (uuid.UUID{}) || len(envelope.BodyRefs) != 0 || envelope.MeasurementPath != "" {
				return invalid("envelope binding")
			}
			if i == 0 || i == len(c.Records)-1 {
				continue
			}
			if r.Effects[i-1] != (ServiceEffect{envelope.Type, int64(envelope.Seq)}) {
				return invalid("effect sequence/order")
			}
			p, err := decodeF3_91(envelope.Type, envelope.Payload)
			if err != nil {
				return err
			}
			s := r.Submission
			switch p := p.(type) {
			case RuntimeServiceObserved:
				a, ok := s.Arguments.(ServiceRecordArguments)
				if !ok || p.ObservationID != r.CommandID || p.AgentID != s.AgentID || p.CauseID != s.CauseID || !reflect.DeepEqual(a, ServiceRecordArguments{p.ObservationID, p.CauseKey, p.Cause, p.Source, p.CompletionPermitsObject}) {
					return invalid("observation/submission")
				}
				if _, ok := observations[p.ObservationID]; ok {
					return invalid("duplicate observation")
				}
				if p.Cause != nil {
					if id, ok := keys[p.CauseKey]; ok && id != p.CauseID {
						return invalid("same key different cause")
					}
					if old, ok := causes[p.CauseID]; ok && old.CauseKey != p.CauseKey {
						return invalid("cause identity changed")
					}
					keys[p.CauseKey] = p.CauseID
				}
				if p.Source.SourceSequence != nil && (p.Source.Kind == "paid_call" || p.Source.Kind == "successful_call") {
					native, ok := records[*p.Source.SourceSequence]
					if !ok || native.Type != TypeRuntimePaidDispatchIntent || native.AgentID != p.AgentID || native.ExecutionSessionID != p.Source.Origin.SessionID || native.ContextID != p.Source.Origin.ContextID || native.TurnID != p.Source.TurnID {
						return invalid("wrong native source")
					}
					dispatch, err := DecodeRuntimePaidDispatchIntent(native.Payload)
					if err != nil || dispatch.AttemptID != p.Source.AttemptID {
						return invalid("wrong attempt")
					}
					if _, err := objectBytes(*p.Source.Object); err != nil {
						return err
					}
				}
				observations[p.ObservationID] = p
			case RuntimeServiceCauseChanged:
				a, ok := s.Arguments.(ServiceStateArguments)
				old := causes[p.CauseID]
				if !ok || p.CauseID != s.CauseID || p.CauseKey != a.CauseKey || p.ExpectedVersion != *s.ExpectedCauseVersion || p.ExpectedVersion != old.Version || !reflect.DeepEqual(p.Source, a.Source) {
					return invalid("cause CAS/submission")
				}
				if old.Version > 0 && (old.CauseKey != p.CauseKey || old.Condition == p.Condition) {
					return invalid("duplicate cause condition")
				}
				if id, ok := keys[p.CauseKey]; ok && id != p.CauseID {
					return invalid("cause association")
				}
				keys[p.CauseKey], causes[p.CauseID] = p.CauseID, p
			case RuntimeServiceAgentChanged:
				a, ok := s.Arguments.(ServiceStateArguments)
				key := [2]uuid.UUID{p.CauseID, p.AgentID}
				old := members[key]
				if !ok || p.CauseID != s.CauseID || p.AgentID != s.AgentID || p.ExpectedVersion != *s.ExpectedMemberVersion || p.ExpectedVersion != old.Version || !reflect.DeepEqual(p.Source, a.Source) {
					return invalid("member CAS/submission")
				}
				if old.Version > 0 {
					if p.Generation != old.Generation || !reflect.DeepEqual(p.FirstSource, old.FirstSource) || !reflect.DeepEqual(p.DueUTCNS, old.DueUTCNS) {
						return invalid("immutable episode witness")
					}
					if p.Phase == old.Phase {
						return invalid("duplicate member transition")
					}
				} else if p.Generation != 1 {
					return invalid("initial generation")
				}
				if p.Phase == "restored" && (old.Phase != "lost" || p.Source.ObservedUTCNS <= old.FirstSource.ObservedUTCNS) {
					return invalid("completion before onset")
				}
				if p.Source.Kind == "grace_expiry" && (old.Phase != "pending" || old.DueUTCNS == nil || p.Source.ObservedUTCNS < *old.DueUTCNS) {
					return invalid("early expiry")
				}
				members[key] = p
				member = &p
			case RuntimeServiceOutageNotice:
				if member == nil || p.MemberSourceSequence != int64(envelope.Seq)-1 || p.CauseID != member.CauseID || p.OutageID != member.OutageID || p.Edge == "began" && member.Phase != "lost" || p.Edge == "ended" && member.Phase != "restored" {
					return invalid("notice member binding")
				}
				if notices[p.OutageID] == nil {
					notices[p.OutageID] = map[string]bool{}
				}
				if notices[p.OutageID][p.Edge] || p.Edge == "ended" && !notices[p.OutageID]["began"] {
					return invalid("duplicate/unopened notice")
				}
				if p.Edge == "ended" {
					for _, m := range members {
						if m.OutageID == p.OutageID && m.Phase == "lost" {
							return invalid("restoration with lost member")
						}
					}
				}
				notices[p.OutageID][p.Edge] = true
				notice = &p
			}
		}
		if a, ok := r.Submission.Arguments.(ServiceStateArguments); ok {
			if a.NoticeID != (uuid.UUID{}) && (notice == nil || notice.NoticeID != a.NoticeID) || a.NoticeID == (uuid.UUID{}) && notice != nil {
				return invalid("notice submission")
			}
			source := a.Source
			if source.Kind != "grace_expiry" {
				e, ok := observations[source.ObservationID]
				if !ok {
					return invalid("missing journal evidence")
				}
				if source.Completion != nil {
					if e.CompletionPermitsObject == nil {
						return invalid("missing completion permits")
					}
					raw, err := objectBytes(*e.CompletionPermitsObject)
					if err != nil {
						return err
					}
					var permits struct {
						Schema          int                       `json:"schema"`
						Kind            string                    `json:"kind"`
						ObservationID   uuid.UUID                 `json:"observation_id"`
						ThroughSequence int64                     `json:"through_sequence"`
						Permits         []ServiceCompletionPermit `json:"permits"`
					}
					if err := json.Unmarshal(raw, &permits); err != nil {
						return err
					}
					if permits.Schema != 1 || permits.Kind != "service_completion" || permits.ObservationID != source.ObservationID || permits.ThroughSequence != source.ObservedThrough {
						return invalid("permit capture binding")
					}
					found := false
					for i, p := range permits.Permits {
						if i > 0 && permits.Permits[i-1].CauseID.String() >= p.CauseID.String() {
							return invalid("permit order/duplicate")
						}
						if reflect.DeepEqual(p, *source.Completion.Permit) {
							found = true
						}
					}
					if !found || source.Completion.Permit.Kind == "blocked" {
						return invalid("wrong/blocked permit")
					}
					source.Completion = new(*source.Completion)
					source.Completion.Permit = nil
				}
				if !reflect.DeepEqual(source, e.Source) {
					return invalid("wrong journal source")
				}
			}
		}
		for _, record := range c.Records {
			records[int64(record.Seq)] = record
		}
	}
	return nil
}

func TestServiceGroupRejections(t *testing.T) {
	t.Parallel()
	_, files := serviceInputs()
	commits, _, err := readAll(serviceDir, ReadOptions{Colony: serviceColony, Through: ToEnd, Consume: serviceTypes})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name   string
		mutate func([]Commit)
	}{
		{"actor", func(cs []Commit) {
			for i := range cs {
				if cs[i].Records[len(cs[i].Records)-1].Type == TypeRuntimeServiceReceipt {
					cs[i].Records[0].Actor = ActorOwner
					return
				}
			}
		}},
		{"origin", func(cs []Commit) {
			for i := range cs {
				if cs[i].Records[len(cs[i].Records)-1].Type == TypeRuntimeServiceReceipt {
					cs[i].Records[1].AgentID = serviceID(99)
					return
				}
			}
		}},
		{"order", func(cs []Commit) {
			for i := range cs {
				if len(cs[i].Records) > 3 && cs[i].Records[len(cs[i].Records)-1].Type == TypeRuntimeServiceReceipt {
					cs[i].Records[1], cs[i].Records[2] = cs[i].Records[2], cs[i].Records[1]
					return
				}
			}
		}},
		{"wrong_source", func(cs []Commit) {
			for i := range cs {
				for j := range cs[i].Records {
					if cs[i].Records[j].Type == TypeRuntimePaidDispatchIntent {
						p, _ := DecodeRuntimePaidDispatchIntent(cs[i].Records[j].Payload)
						p.AttemptID = serviceID(99)
						cs[i].Records[j].Payload = mustEncode(p)
						return
					}
				}
			}
		}},
		{"duplicate_command", func(cs []Commit) {
			for i := range cs {
				if cs[i].Records[len(cs[i].Records)-1].Type == TypeRuntimeServiceReceipt {
					cs[len(cs)-1] = cs[i]
					return
				}
			}
		}},
		{"semantic_duplicate_cause", func(cs []Commit) {
			for i := range cs {
				if cs[i].Records[1].Type != TypeRuntimeServiceObserved {
					continue
				}
				e, _ := DecodeRuntimeServiceObserved(cs[i].Records[1].Payload)
				if e.CauseID != serviceID(0x7002) || e.AgentID == scopedOriginFixture().AgentID {
					continue
				}
				e.CauseID = serviceID(99)
				r, _ := DecodeRuntimeServiceReceipt(cs[i].Records[2].Payload)
				r.Submission.CauseID = e.CauseID
				r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
				cs[i].Records[0].Payload = mustEncode(Command{r.CommandID, r.SubmissionSHA256})
				cs[i].Records[1].Payload = mustEncode(e)
				cs[i].Records[2].Payload = mustEncode(r)
				return
			}
		}},
		{"notice_self_sequence", func(cs []Commit) {
			for i := range cs {
				for j := range cs[i].Records {
					if cs[i].Records[j].Type == TypeRuntimeServiceOutageNotice {
						p, _ := DecodeRuntimeServiceOutageNotice(cs[i].Records[j].Payload)
						p.MemberSourceSequence = int64(cs[i].Records[j].Seq)
						cs[i].Records[j].Payload = mustEncode(p)
						return
					}
				}
			}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			copied := slices.Clone(commits)
			for i := range copied {
				copied[i].Records = slices.Clone(copied[i].Records)
			}
			row.mutate(copied)
			if err := checkServiceGroups(copied, files); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid group accepted: %v", err)
			}
		})
	}
}
