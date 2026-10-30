package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"uuid"
)

const paidOpenAIDir = "testdata/f3/14/log"

// Chapter 14 is a second F3-13 producer sample, not a new payload family. The
// unchanged Anthropic prefix proves both producers share one replayed book.
func paidOpenAIInputs() ([]goldenCommit, map[string][]byte) {
	out, files := paidInputs()
	origin := scopedOriginFixture()
	route, grant, turn := paidID(0x400), paidID(0x410), paidID(0x420)
	scope, credential := paidID(0x1401), paidID(0x1402)
	var sequence int64
	for _, c := range out {
		sequence += int64(len(c.drafts))
	}
	now := time.Date(2026, 10, 25, 23, 59, 0, 0, time.UTC).UnixNano()
	draft := func(command, turn uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: origin.AgentID, ExecutionSessionID: origin.SessionID, ContextID: origin.ContextID, OperationID: command, TurnID: turn, Payload: mustEncode(p)}
	}
	add := func(s PaidSubmission, version int64, outcome string, attempt, turn uuid.UUID, typ string, effect payload) int64 {
		command := paidID(0x14000 + len(out))
		hash := Digest(sha256.Sum256(mustEncode(s)))
		actor, effectActor := Actor(s.Actor), Actor(s.Actor)
		if s.Operation == "paid.admit" {
			effectActor = ActorCore
		}
		r := RuntimePaidReceipt{CommandID: command, SubmissionSHA256: hash, Submission: s, Outcome: outcome, Version: version, AttemptID: attempt}
		now += int64(time.Second)
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: paidColony, CommitID: paidID(0x15000 + len(out)), EventTimeNS: now}, drafts: []Draft{draft(command, turn, TypeCommand, actor, Command{command, hash}), draft(command, turn, typ, effectActor, effect), draft(command, turn, TypeRuntimePaidReceipt, actor, r)}})
		sequence += 3
		return sequence - 1
	}
	owner := func(op string, args any) PaidSubmission {
		return PaidSubmission{Schema: 4, Actor: "owner", Origin: origin, Operation: op, Arguments: args}
	}
	object := func(text string) ObjectRef {
		h := scopedHash(text)
		files["objects/agents/Ada-a1/contexts/"+origin.ContextID.String()+"/core/objects/"+h.String()] = []byte(text)
		return ObjectRef{h, int64(len(text))}
	}
	bind := RuntimeCredentialChanged{ScopeID: scope, Provider: "openai", Version: 1, Action: "bind", CredentialVersion: credential, SourceKind: "environment", SourceLocator: "SYNTHETIC_F3_14_UNCONFIGURED_SOURCE"}
	add(owner("paid.credential.bind", PaidCredentialBindArguments{ScopeID: scope, Provider: bind.Provider, CredentialVersion: credential, SourceKind: bind.SourceKind, SourceLocator: bind.SourceLocator}), 1, "bound", uuid.UUID{}, uuid.UUID{}, TypeRuntimeCredentialChanged, bind)
	profile := paidProfileFixture()
	profile.CapabilityVersion, profile.TariffVersion = "synthetic-openai-cap-1", "synthetic-openai-tariff-1"
	profile.Provider, profile.Path, profile.APIVersion, profile.ModelID = "openai", "/v1/responses", "v1", "gpt-6-luna"
	profile.RequestedTier, profile.AllowedServedTiers = "default", []string{"default"}
	profile.ContextMax, profile.InputMax, profile.OutputMax = 1050000, 922000, 128000
	profile.UsageSchema = "openai-responses-text-30m-kguard/v1"
	profile.CacheClasses = []string{"input.cached.long", "input.cached.short", "input.write.long", "input.write.short"}
	profile.Tariff.Rates = []TariffRate{{"input.cached.long", "1", "50"}, {"input.cached.short", "1", "100"}, {"input.uncached.long", "1", "5"}, {"input.uncached.short", "1", "10"}, {"input.write.long", "1", "4"}, {"input.write.short", "1", "8"}, {"output.generated.long", "3", "4"}, {"output.generated.short", "1", "2"}}
	profile.VerifiedUTCNS, profile.ExpiresUTCNS = now-int64(time.Hour), now+int64(6*24*time.Hour)
	for i := range profile.Sources {
		profile.Sources[i].RetrievedUTCNS = profile.VerifiedUTCNS - 1
	}
	ph, err := profile.SHA256()
	if err != nil {
		panic(err)
	}
	th, err := profile.TariffSHA256()
	if err != nil {
		panic(err)
	}
	configured := RuntimePaidRouteConfigured{route, 3, 4, true, scope, profile, now}
	add(owner("paid.route.configure", PaidRouteArguments{route, 3, true, scope, ph}), 4, "configured", uuid.UUID{}, uuid.UUID{}, TypeRuntimePaidRouteConfigured, configured)
	terms := SpendTerms{Enabled: true, Mode: "finite", CeilingMicros: new(int64(1000000)), PeriodKind: "utc_day", WarningPPM: new(int64(800000))}
	spend := RuntimeSpendConfigured{Scope: PaidScope{"credential", scope}, Version: 1, Terms: terms, AuthorityVersion: 4}
	add(owner("paid.spend.configure", PaidSpendArguments{Scope: spend.Scope, Terms: terms}), 1, "configured", uuid.UUID{}, uuid.UUID{}, TypeRuntimeSpendConfigured, spend)
	body := `{"model":"gpt-6-luna","max_output_tokens":16,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Synthetic OpenAI request."}]}],"stream":true,"store":false,"service_tier":"default","truncation":"disabled","background":false,"include":["reasoning.encrypted_content"],"prompt_cache_options":{"mode":"implicit","prewarm":false,"ttl":"30m"},"text":{"format":{"type":"text"}}}`
	input := object(body)
	reserve := func(n int) RuntimePaidReserved {
		r := SealedDescriptor{profile.Provider, profile.Authority, profile.ModelID, profile.Region, "POST", profile.Path, profile.RequestedTier, CredentialRef{scope, credential}, []SemanticHeader{{"accept", "text/event-stream"}, {"content-type", "application/json"}}, input.SHA256, input.Bytes}
		q := CostBound{Currency: "USD", MaximumMicros: 230512, Components: []PricedDimension{{[]string{"input.cached.long", "input.cached.short", "input.uncached.long", "input.uncached.short", "input.write.long", "input.write.short"}, 922000, "1", "4"}, {[]string{"output.generated.long", "output.generated.short"}, 16, "3", "4"}}, RequestSHA256: paidRequestHash(r, []byte(body)), CapabilityVersion: profile.CapabilityVersion, TariffVersion: profile.TariffVersion, TariffSHA256: th}
		q.QuoteSHA256 = paidQuoteHash(q)
		p := RuntimePaidReserved{AttemptID: paidID(n), Version: 1, AuthorityVersion: 4, GrantID: grant, ParentTurnID: turn, RouteID: route, RouteVersion: 4, ProfileSHA256: ph, Request: r, Quote: q, AdmissionUTCNS: now, AdmissionPeriod: time.Unix(0, now).UTC().Format("2006-01-02"), Scopes: []PaidScope{{Kind: "colony"}, {"agent", origin.AgentID}, {"credential", scope}, {"grant", grant}}, InputObject: input}
		add(PaidSubmission{4, "agent", origin, "paid.admit", grant, paidAdmitArguments(p)}, 1, "reserved", p.AttemptID, turn, TypeRuntimePaidReserved, p)
		add(PaidSubmission{4, "core", origin, "paid.dispatch", grant, PaidAttemptArguments{p.AttemptID, 1}}, 2, "sent", p.AttemptID, turn, TypeRuntimePaidDispatchIntent, RuntimePaidDispatchIntent{p.AttemptID, 1, 2, 4, q.RequestSHA256, q.QuoteSHA256, r.Credential, now})
		return p
	}
	usage := func(input, cached, write, output int64, long bool) *PaidUsage {
		u := &PaidUsage{PricingTier: "default_short", ServedTier: "default"}
		for _, r := range profile.Tariff.Rates {
			u.Classes = append(u.Classes, PaidUnits{Class: r.Class})
		}
		i := 1
		if long {
			i = 0
			u.PricingTier = "default_long"
		}
		u.Classes[i].Units = cached
		u.Classes[i+2].Units = input - cached - write
		u.Classes[i+4].Units = write
		u.Classes[i+6].Units = output
		return u
	}
	result := func(p RuntimePaidReserved, r RuntimePaidResult, status string, raw json.RawMessage, objects []ObjectRef) (RuntimePaidResult, int64) {
		r.AttemptID = p.AttemptID
		r.ProviderRequestID = "synthetic-openai-request-" + p.AttemptID.String()
		r.ProviderMessageID = "synthetic-openai-response-" + p.AttemptID.String()
		r.MessageObjects = objects
		r.OutputStarted = new(len(objects) != 0)
		capture := map[string]any{
			"schema": 1, "kind": "paid_observation", "attempt_id": p.AttemptID,
			"origin": origin, "parent_turn_id": turn, "request_sha256": p.Quote.RequestSHA256,
			"provider_request_id": r.ProviderRequestID, "provider_message_id": r.ProviderMessageID,
			"model": profile.ModelID, "http_status": 200, "headers": []any{},
			"complete": status != "in_progress", "output_started": *r.OutputStarted,
			"stop_reason": status, "served_tier": "default", "raw_usage_final": raw,
			"message_objects": objects, "unknown_event_types": []string{},
			"unknown_event_type_overflow": 0, "content_blocks": len(objects),
			"content_error": false, "profile_violation": false,
		}
		if status == "incomplete" {
			capture["stop_details"] = json.RawMessage(`{"reason":"max_output_tokens"}`)
		}
		if r.ErrorClass != "" {
			capture["error_class"] = r.ErrorClass
		}
		if r.ErrorClass == "tariff_ambiguous" {
			capture["error"] = map[string]string{"type": "threshold_ambiguous", "message": ""}
		}
		evidence := object(string(mustJSON(capture)))
		r.EvidenceObject, r.EvidenceSHA256 = &evidence, evidence.SHA256
		s := PaidSubmission{Schema: 4, Actor: "core", Origin: origin, GrantID: grant, Operation: "paid.observe", Arguments: PaidObserveArguments{p.AttemptID, r.ExpectedVersion, r.Kind, r.EvidenceSHA256, evidence, objects}}
		outcome := r.Kind
		if r.Kind == "usage" {
			outcome = "settled"
		}
		if r.Kind == "correction" {
			s.Operation = "paid.correct"
			s.Arguments = PaidCorrectArguments{p.AttemptID, r.ExpectedVersion, r.PreviousResultSequence, r.PreviousEvidenceSHA256, r.EvidenceSHA256, evidence, objects}
			outcome = "corrected"
		}
		seq := add(s, r.Version, outcome, p.AttemptID, turn, TypeRuntimePaidResult, r)
		return r, seq
	}
	output := func(attempt int, block json.RawMessage) ObjectRef {
		return object(string(mustJSON(map[string]any{"schema": 1, "kind": "paid_output", "attempt_id": paidID(attempt), "origin": origin, "parent_turn_id": turn, "block_index": 0, "chunk_index": 0, "block": block})))
	}
	text := output(0x14500, json.RawMessage(`{"kind":"text","text":"Synthetic OpenAI answer."}`))
	opaque := output(0x14502, json.RawMessage(`{"kind":"opaque_provider","provider":"openai","model":"gpt-6-luna","artifact_kind":"responses_reasoning","payload":{"type":"reasoning","id":"synthetic-reasoning","summary":[],"encrypted_content":"synthetic-opaque-content","extension":{"retained":true}}}`))
	p := reserve(0x14500)
	result(p, RuntimePaidResult{ExpectedVersion: 2, Version: 3, Kind: "usage", Usage: usage(20, 4, 8, 16, false), CostMicros: new(int64(10))}, "completed", json.RawMessage(`{"input_tokens":20,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":8},"output_tokens":16,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":36}`), []ObjectRef{text})
	p = reserve(0x14501)
	result(p, RuntimePaidResult{ExpectedVersion: 2, Version: 3, Kind: "unknown", ErrorClass: "tariff_ambiguous"}, "completed", json.RawMessage(`{"input_tokens":272001,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":16}`), nil)
	p = reserve(0x14502)
	result(p, RuntimePaidResult{ExpectedVersion: 2, Version: 3, Kind: "usage", Usage: usage(20, 4, 8, 16, false), CostMicros: new(int64(10))}, "incomplete", json.RawMessage(`{"input_tokens":20,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":8},"output_tokens":16}`), []ObjectRef{opaque})
	p = reserve(0x14503)
	result(p, RuntimePaidResult{ExpectedVersion: 2, Version: 3, Kind: "unknown", ErrorClass: "protocol_error"}, "in_progress", json.RawMessage(`null`), nil)
	now += int64(2 * time.Minute)
	text = output(0x14503, json.RawMessage(`{"kind":"text","text":"Synthetic late OpenAI answer."}`))
	r, seq := result(p, RuntimePaidResult{ExpectedVersion: 3, Version: 4, Kind: "usage", Usage: usage(278529, 0, 0, 16, true), CostMicros: new(int64(55718))}, "completed", json.RawMessage(`{"input_tokens":278529,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":16}`), []ObjectRef{text})
	correction := r
	correction.Kind, correction.ExpectedVersion, correction.Version = "correction", 4, 5
	correction.PreviousResultSequence, correction.PreviousEvidenceSHA256 = seq, r.EvidenceSHA256
	correction.Usage = usage(278534, 0, 0, 16, true)
	correction.CostMicros = new(int64(55719))
	result(p, correction, "completed", json.RawMessage(`{"input_tokens":278534,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens":16}`), []ObjectRef{text})
	return out, files
}

func TestGoldenOpenAIStatic(t *testing.T) {
	t.Parallel()
	// Frozen hashes and real C/effect/R decoding catch drift in either producer
	// without importing Core or teaching the structural codec a tariff rule.
	inputs, files := paidOpenAIInputs()
	g, err := buildLog(paidOpenAIDir, paidColony, paidTypes, inputs)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(paidOpenAIDir)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	}) {
		want := files[name]
		path := filepath.Join(root, filepath.FromSlash(name))
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) || bytes.Contains(got, []byte{'\r'}) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(got))
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: paidTypes})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commits {
		if c.Records[len(c.Records)-1].Type != TypeRuntimePaidReceipt {
			continue
		}
		r, err := DecodeRuntimePaidReceipt(c.Records[len(c.Records)-1].Payload)
		if err != nil {
			t.Fatal(err)
		}
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil || command.CommandID != r.CommandID || command.SubmissionSHA256 != r.SubmissionSHA256 {
			t.Fatal("paid C/effect/R identity drift")
		}
		if _, err := decodeF3_13(c.Records[1].Type, c.Records[1].Payload); err != nil {
			t.Fatal(err)
		}
	}
}
