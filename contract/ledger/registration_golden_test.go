package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"uuid"
)

const registrationDir = "testdata/f3/139/log"
const registrationDescription = "Registered resource declaration fixture.\n"

var registrationColony = registrationID(1)
var registrationTypes = Consumption{
	TypeCommand: {1}, TypeBodyPin: {1},
	TypeRuntimeAgentRegistered: {1}, TypeRuntimeContextCreated: {1}, TypePrivateRevision: {1}, TypeRuntimeCommandReceipt: {1},
	TypeRuntimeTaskChanged: {1}, TypeRuntimeGrantIssued: {1}, TypeRuntimeScopedReceipt: {1},
	TypeRuntimeResourceRegistered: {1}, TypeRuntimeWorkDomainRegistered: {1}, TypeRuntimeDeclarationChanged: {1}, TypeRuntimeRegistrationReceipt: {1},
}
var registrationLog = sync.OnceValues(func() (*goldenLog, error) {
	return buildLog(registrationDir, registrationColony, registrationTypes, registrationInputs())
})

func registrationID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1390-0000-7000-8000-%012x", n))
}

func registrationAddress() Resource {
	return Resource{Kind: "external", ResourceKey: "paid-route/" + registrationID(0x450).String()}
}

func registrationFixtureFiles() map[string][]byte {
	return map[string][]byte{
		"objects/" + scopedHash(scopedInstructions).String():     []byte(scopedInstructions),
		"objects/" + scopedHash("").String():                     {},
		"bodies/" + scopedHash(registrationDescription).String(): []byte(registrationDescription),
	}
}

// registrationInputs uses the frozen earlier-family encodings for five real
// prerequisite commits, followed by fourteen owner evidence transitions.
func registrationInputs() []goldenCommit {
	a := Origin{registrationID(0x100), registrationID(0x101), registrationID(0x102)}
	b := Origin{registrationID(0x200), registrationID(0x201), registrationID(0x202)}
	work := registrationID(0x103)
	ab := []uuid.UUID{a.AgentID, b.AgentID}
	var out []goldenCommit
	appendCommit := func(drafts []Draft) {
		i := len(out) + 1
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: registrationColony, CommitID: registrationID(0x2000 + i), EventTimeNS: 1791000700000000000 + int64(i)*1000000000}, drafts: drafts})
	}
	draft := func(o Origin, typ string, actor Actor, command uuid.UUID, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: o.AgentID, ExecutionSessionID: o.SessionID, ContextID: o.ContextID, OperationID: command, Payload: mustEncode(p)}
	}
	identity := func(o Origin, operation string, args any, effects ...payload) {
		command := registrationID(0x1000 + len(out) + 1)
		scope := "context"
		if operation == "agent_register" {
			scope = "agent"
		}
		target := PrivateTarget{scope, []string{}}
		submission := struct {
			Schema          int           `json:"schema"`
			Actor           string        `json:"actor"`
			AgentID         uuid.UUID     `json:"agent_id"`
			SessionID       uuid.UUID     `json:"session_id"`
			ContextID       uuid.UUID     `json:"context_id"`
			Operation       string        `json:"operation"`
			Target          PrivateTarget `json:"target"`
			ExpectedVersion int64         `json:"expected_version"`
			Arguments       any           `json:"arguments"`
		}{1, "owner", o.AgentID, o.SessionID, o.ContextID, operation, target, 0, args}
		js, err := json.Marshal(submission)
		if err != nil {
			panic(err)
		}
		digest := Digest(sha256.Sum256(js))
		drafts := []Draft{draft(o, TypeCommand, ActorOwner, command, Command{command, digest})}
		for _, effect := range effects {
			typ, actor := TypePrivateRevision, ActorCore
			switch effect.(type) {
			case RuntimeAgentRegistered:
				typ, actor = TypeRuntimeAgentRegistered, ActorOwner
			case RuntimeContextCreated:
				typ, actor = TypeRuntimeContextCreated, ActorOwner
			}
			drafts = append(drafts, draft(o, typ, actor, command, effect))
		}
		drafts = append(drafts, draft(o, TypeRuntimeCommandReceipt, ActorOwner, command, RuntimeCommandReceipt{CommandID: command, SubmissionSHA256: digest, Operation: operation, Target: target, Outcome: "applied"}))
		appendCommit(drafts)
	}
	instructions := privateFixtureChange("instructions", "replace", scopedInstructions)
	memory := privateFixtureChange("memory", "replace", "")
	handoff := privateFixtureChange("handoff", "replace", "")
	for i, origin := range []Origin{a, b} {
		directory := fmt.Sprintf("Ada-a%d", i+1)
		identity(origin, "agent_register", struct {
			Directory   string `json:"directory"`
			AgentSHA256 Digest `json:"agent_sha256"`
			AgentBytes  int64  `json:"agent_bytes"`
		}{directory, instructions.SHA256, instructions.Bytes}, RuntimeAgentRegistered{directory, origin.ContextID}, RuntimeContextCreated{"general"}, PrivateRevision{"agent", 0, 1, []PrivateChange{instructions, memory}}, PrivateRevision{"context", 0, 1, []PrivateChange{handoff, memory}})
	}
	identity(Origin{a.AgentID, a.SessionID, work}, "context_create", RuntimeContextCreated{"work"}, RuntimeContextCreated{"work"}, PrivateRevision{"context", 0, 1, []PrivateChange{handoff, memory}})
	addScoped := func(s ScopedSubmission, version int64, effects ...payload) {
		command := registrationID(0x1000 + len(out) + 1)
		digest := Digest(sha256.Sum256(mustEncode(s)))
		drafts := []Draft{draft(a, TypeCommand, ActorOwner, command, Command{command, digest})}
		for _, effect := range effects {
			actor := ActorOwner
			if _, ok := effect.(BodyPin); ok {
				actor = ActorCore
			}
			d := draft(a, scopedPayloadType(effect), actor, command, effect)
			switch effect.(type) {
			case BodyPin, RuntimeTaskChanged:
				d.BodyRefs = []Digest{scopedHash(registrationDescription)}
			}
			drafts = append(drafts, d)
		}
		drafts = append(drafts, draft(a, TypeRuntimeScopedReceipt, ActorOwner, command, RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: digest, Submission: s, Outcome: "applied", Version: version}))
		appendCommit(drafts)
	}
	task := RuntimeTaskChanged{TaskID: registrationID(0x300), Action: "create", Version: 1, CreatorActor: "owner", Creator: a, Description: ObjectRef{scopedHash(registrationDescription), int64(len(registrationDescription))}, AcceptanceSHA256: scopedHash("registration fixture acceptance\n"), Dependencies: []uuid.UUID{}, Audience: ab, State: "waiting"}
	addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: a, Operation: "task.create", Target: Resource{Kind: "task", TaskID: task.TaskID}, Arguments: TaskCreateArguments{task.TaskID, task.Description, task.AcceptanceSHA256, task.Dependencies, task.Audience, nil}}, 1, BodyPin{task.TaskID}, task)
	grant := RuntimeGrantIssued{GrantID: registrationID(0x600), AuthorityVersion: 1, Grantee: Binding{a.AgentID, a.ContextID}, Operations: []string{"model.call"}, Resources: []Resource{registrationAddress()}, DataClass: "external", Recipients: []Binding{{a.AgentID, a.ContextID}}, Lifetime: "until_revoked"}
	addScoped(ScopedSubmission{Schema: 2, Actor: "owner", Origin: a, Operation: "grant.issue", Target: registrationAddress(), Expected: ScopedExpected{AuthorityVersion: new(int64(0))}, Arguments: scopedIssueArguments(grant)}, 1, grant)

	add := func(effect payload) {
		command := registrationID(0x1000 + len(out) + 1)
		s := RegistrationSubmission{Schema: 5, Actor: "owner", Origin: a}
		var typ string
		var version int64
		switch p := effect.(type) {
		case RuntimeResourceRegistered:
			typ, version, s.Operation = TypeRuntimeResourceRegistered, p.Version, "resource.set"
			s.Arguments = ResourceRegistrationArguments{p.ResourceID, p.ExpectedVersion, p.Resource, p.Audience}
		case RuntimeWorkDomainRegistered:
			typ, version, s.Operation = TypeRuntimeWorkDomainRegistered, p.Version, "work_domain.set"
			s.Arguments = WorkDomainRegistrationArguments{p.WorkDomainID, p.ExpectedVersion, p.Audience}
		case RuntimeDeclarationChanged:
			typ, version, s.Operation = TypeRuntimeDeclarationChanged, p.Version, "declaration.set"
			s.Arguments = DeclarationArguments{p.DeclarationID, p.ExpectedVersion, p.Subject, p.Audience, p.Consumers}
		default:
			panic(fmt.Sprintf("unexpected registration effect %T", effect))
		}
		digest := Digest(sha256.Sum256(mustEncode(s)))
		appendCommit([]Draft{
			draft(a, TypeCommand, ActorOwner, command, Command{command, digest}),
			draft(a, typ, ActorOwner, command, effect),
			draft(a, TypeRuntimeRegistrationReceipt, ActorOwner, command, RuntimeRegistrationReceipt{command, digest, s, "applied", version}),
		})
	}
	r := RuntimeResourceRegistered{registrationID(0x400), 0, 1, registrationAddress(), ab}
	d := RuntimeWorkDomainRegistered{registrationID(0x401), 0, 1, ab}
	taskConsumer := DeclarationConsumer{Kind: "task", TaskID: task.TaskID}
	workConsumer := DeclarationConsumer{Kind: "context", Binding: Binding{a.AgentID, work}}
	generalConsumer := DeclarationConsumer{Kind: "context", Binding: Binding{b.AgentID, b.ContextID}}
	rt := RuntimeDeclarationChanged{registrationID(0x500), 0, 1, RegistrationRef{"resource", r.ResourceID, 1}, ab, []DeclarationConsumer{taskConsumer}}
	rc := RuntimeDeclarationChanged{registrationID(0x501), 0, 1, rt.Subject, []uuid.UUID{a.AgentID}, []DeclarationConsumer{workConsumer}}
	dt := RuntimeDeclarationChanged{registrationID(0x502), 0, 1, RegistrationRef{"work_domain", d.WorkDomainID, 1}, ab, []DeclarationConsumer{taskConsumer}}
	dc := RuntimeDeclarationChanged{registrationID(0x503), 0, 1, dt.Subject, []uuid.UUID{b.AgentID}, []DeclarationConsumer{generalConsumer}}
	for _, effect := range []payload{r, d, rt, rc, dt, dc} {
		add(effect)
	}
	r.ExpectedVersion, r.Version, r.Audience = 1, 2, []uuid.UUID{a.AgentID}
	d.ExpectedVersion, d.Version, d.Audience = 1, 2, []uuid.UUID{b.AgentID}
	add(r)
	add(d)
	rt.ExpectedVersion, rt.Version, rt.Subject.Version, rt.Audience, rt.Consumers = 1, 2, 2, r.Audience, []DeclarationConsumer{workConsumer, taskConsumer}
	rc.ExpectedVersion, rc.Version, rc.Subject.Version, rc.Consumers = 1, 2, 2, []DeclarationConsumer{}
	dt.ExpectedVersion, dt.Version, dt.Subject.Version, dt.Audience, dt.Consumers = 1, 2, 2, d.Audience, []DeclarationConsumer{}
	dc.ExpectedVersion, dc.Version, dc.Subject.Version = 1, 2, 2
	for _, effect := range []payload{rt, rc, dt, dc} {
		add(effect)
	}
	r.ExpectedVersion, r.Version, r.Audience = 2, 3, ab
	add(r)
	rt.ExpectedVersion, rt.Version, rt.Subject.Version, rt.Audience, rt.Consumers = 2, 3, 3, ab, []DeclarationConsumer{taskConsumer}
	add(rt)
	return out
}

func TestGoldenF3_139(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, registrationLog)
	files := registrationFixtureFiles()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(registrationDir)
	if *update {
		for name, data := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(data, want) || bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("fixture %s differs or contains CR: %v", name, err)
		}
		if filepath.Dir(name) != "log" && filepath.Base(name) != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("incorrect content address %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(name)]; !ok {
			return fmt.Errorf("unexpected fixture file %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: registrationTypes})
	if err != nil || len(commits) != 19 || cursor != 65 {
		t.Fatalf("commits=%d cursor=%d: %v; want 19/65", len(commits), cursor, err)
	}
	// Inspect consumer-visible versions and pins independently of generation.
	wantVersions := []int64{1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 3, 3}
	wantConsumers := map[uint64][]string{31: {"task"}, 34: {"context"}, 37: {"task"}, 40: {"context"}, 49: {"context", "task"}, 52: {}, 55: {}, 58: {"context"}, 64: {"task"}}
	resources := map[uuid.UUID]RuntimeResourceRegistered{}
	domains := map[uuid.UUID]RuntimeWorkDomainRegistered{}
	declarations := map[uuid.UUID]RuntimeDeclarationChanged{}
	for i, commit := range commits[5:] {
		if len(commit.Records) != 3 || commit.First != uint64(24+i*3) || commit.Last != uint64(26+i*3) {
			t.Fatalf("new group %d has wrong boundaries: %+v", i, commit)
		}
		command, err := DecodeCommand(commit.Records[0].Payload)
		if err != nil || commit.Records[0].Type != TypeCommand {
			t.Fatalf("command: %+v %v", command, err)
		}
		receipt, err := DecodeRuntimeRegistrationReceipt(commit.Records[2].Payload)
		if err != nil || commit.Records[2].Type != TypeRuntimeRegistrationReceipt || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.Version != wantVersions[i] || receipt.Outcome != "applied" {
			t.Fatalf("receipt: %+v %v", receipt, err)
		}
		for _, record := range commit.Records {
			o := receipt.Submission.Origin
			if record.Actor != ActorOwner || record.AgentID != o.AgentID || record.ExecutionSessionID != o.SessionID || record.ContextID != o.ContextID || record.OperationID != command.CommandID || record.TurnID != (uuid.UUID{}) || record.MeasurementPath != "" || len(record.BodyRefs) != 0 {
				t.Fatalf("unexpected envelope at %d", record.Seq)
			}
		}
		record := commit.Records[1]
		effect, err := decodeF3_139(record.Type, record.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var args any
		var operation string
		switch p := effect.(type) {
		case RuntimeResourceRegistered:
			if p.Version != resources[p.ResourceID].Version+1 || p.Resource.ResourceKey != registrationAddress().ResourceKey {
				t.Fatalf("resource snapshot at %d: %+v", record.Seq, p)
			}
			resources[p.ResourceID] = p
			args, operation = ResourceRegistrationArguments{p.ResourceID, p.ExpectedVersion, p.Resource, p.Audience}, "resource.set"
		case RuntimeWorkDomainRegistered:
			if p.Version != domains[p.WorkDomainID].Version+1 {
				t.Fatalf("domain snapshot at %d: %+v", record.Seq, p)
			}
			domains[p.WorkDomainID] = p
			args, operation = WorkDomainRegistrationArguments{p.WorkDomainID, p.ExpectedVersion, p.Audience}, "work_domain.set"
		case RuntimeDeclarationChanged:
			current := resources[p.Subject.ID].Version
			if p.Subject.Kind == "work_domain" {
				current = domains[p.Subject.ID].Version
			}
			var kinds []string
			for _, consumer := range p.Consumers {
				kinds = append(kinds, consumer.Kind)
			}
			if p.Subject.Version != current || p.Version != declarations[p.DeclarationID].Version+1 || !slices.Equal(kinds, wantConsumers[record.Seq]) {
				t.Fatalf("declaration snapshot at %d: %+v", record.Seq, p)
			}
			declarations[p.DeclarationID] = p
			args, operation = DeclarationArguments{p.DeclarationID, p.ExpectedVersion, p.Subject, p.Audience, p.Consumers}, "declaration.set"
		default:
			t.Fatalf("unexpected effect %T", effect)
		}
		if receipt.Submission.Operation != operation || !reflect.DeepEqual(receipt.Submission.Arguments, args) {
			t.Fatalf("effect differs from receipt at %d", record.Seq)
		}
		if got, err := effect.Encode(); err != nil || !bytes.Equal(got, record.Payload) {
			t.Fatalf("noncanonical effect at %d: %v", record.Seq, err)
		}
		// A source revision must not rewrite earlier pinned declarations.
		if record.Seq == 43 && declarations[registrationID(0x500)].Subject.Version != 1 || record.Seq == 61 && declarations[registrationID(0x500)].Subject.Version != 2 {
			t.Fatal("source revision rewrote a declaration")
		}
	}
	result, err := Check(g.dir, g.colony)
	if want := (CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}); err != nil || result != want {
		t.Fatalf("Check=%+v %v; want %+v", result, err, want)
	}
	t.Log("commits=19 records=65 first_effect=25 first_receipt=26 final_effect=64 final_receipt=65")
}
