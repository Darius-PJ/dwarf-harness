package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"uuid"
)

const scopedDir = "testdata/f3/11/log"
const scopedInstructions = "# Ada\n"
const scopedMemory = "work memory\n"
const scopedDescriptionA = "Implement scoped admission.\n"
const scopedDescriptionB = "Verify scoped accounting.\n"

var scopedColony = uuid.MustParse("019a1100-0000-7000-8000-000000000001")

func scopedID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1100-0000-7000-8000-%012x", n))
}

var scopedTypes = Consumption{
	TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
	TypeRuntimeAgentRegistered: {1}, TypeRuntimeContextCreated: {1}, TypePrivateRevision: {1}, TypeRuntimeCommandReceipt: {1},
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1}, TypeScratchCreated: {1},
	TypeRuntimeGrantIssued: {1}, TypeRuntimeGrantRevoked: {1}, TypeRuntimeTaskChanged: {1}, TypeRuntimeCapacityConfigured: {1}, TypeRuntimeWorkAdmitted: {1}, TypeRuntimeWorkDispatched: {1}, TypeRuntimeWorkEnded: {1}, TypeRuntimeLocalUsed: {1}, TypePrivateScopedRevision: {1}, TypeRuntimeScopedReceipt: {1},
}
var scopedLog = sync.OnceValues(func() (*goldenLog, error) { return buildLog(scopedDir, scopedColony, scopedTypes, scopedInputs()) })

func mustScoped(t testing.TB) *goldenLog { t.Helper(); return mustBuild(t, scopedLog) }

func scopedFixtureFiles() map[string][]byte {
	files := map[string][]byte{}
	for _, text := range []string{scopedInstructions, "", scopedMemory} {
		files["objects/"+scopedHash(text).String()] = []byte(text)
	}
	for _, body := range [][]byte{[]byte(scopedDescriptionA), []byte(scopedDescriptionB), scopedCaptureFixture()} {
		files["bodies/"+Digest(sha256.Sum256(body)).String()] = body
	}
	return files
}

// scopedInputs covers every ordered F3-11 group shape with live prerequisites.
// This is fixed contract data, not an executor or a substitute service reducer.
func scopedInputs() []goldenCommit {
	agent, session, general := fid("a1"), fid("b1"), fid("d1")
	w1, w2, w3 := fid("d2"), fid("d3"), fid("d4")
	origin := func(context uuid.UUID) Origin { return Origin{agent, session, context} }
	binding := func(context uuid.UUID) Binding { return Binding{agent, context} }
	contextTarget := func(context uuid.UUID, slots ...string) Resource {
		if slots == nil {
			slots = []string{}
		}
		return Resource{Kind: "context", Binding: binding(context), Scope: "context", Slots: slots}
	}
	var out []goldenCommit
	var sequence int64
	appendCommit := func(drafts []Draft) {
		i := len(out) + 1
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: scopedColony, CommitID: scopedID(0x2000 + i), EventTimeNS: 1791000700000000000 + int64(i)*1000000000}, drafts: drafts})
		sequence += int64(len(drafts))
	}
	identity := func(context uuid.UUID, op string, args any, effects ...payload) {
		command := scopedID(0x1000 + len(out) + 1)
		scope := "context"
		if op == "agent_register" {
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
		}{1, "owner", agent, session, context, op, target, 0, args}
		js, err := json.Marshal(submission)
		if err != nil {
			panic(err)
		}
		hash := Digest(sha256.Sum256(js))
		draft := func(typ string, actor Actor, p payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: agent, ExecutionSessionID: session, ContextID: context, OperationID: command, Payload: mustEncode(p)}
		}
		drafts := []Draft{draft(TypeCommand, ActorOwner, Command{command, hash})}
		for _, p := range effects {
			typ, actor := TypePrivateRevision, ActorCore
			switch p.(type) {
			case RuntimeAgentRegistered:
				typ, actor = TypeRuntimeAgentRegistered, ActorOwner
			case RuntimeContextCreated:
				typ, actor = TypeRuntimeContextCreated, ActorOwner
			}
			drafts = append(drafts, draft(typ, actor, p))
		}
		drafts = append(drafts, draft(TypeRuntimeCommandReceipt, ActorOwner, RuntimeCommandReceipt{CommandID: command, SubmissionSHA256: hash, Operation: op, Target: target, Outcome: "applied"}))
		appendCommit(drafts)
	}
	emptyMemory := privateFixtureChange("memory", "replace", "")
	emptyHandoff := privateFixtureChange("handoff", "replace", "")
	instructions := privateFixtureChange("instructions", "replace", scopedInstructions)
	identity(general, "agent_register", struct {
		Directory   string `json:"directory"`
		AgentSHA256 Digest `json:"agent_sha256"`
		AgentBytes  int64  `json:"agent_bytes"`
	}{"Ada-a1", instructions.SHA256, instructions.Bytes}, RuntimeAgentRegistered{"Ada-a1", general}, RuntimeContextCreated{"general"}, PrivateRevision{"agent", 0, 1, []PrivateChange{instructions, emptyMemory}}, PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
	for _, work := range []uuid.UUID{w1, w2, w3} {
		identity(work, "context_create", RuntimeContextCreated{"work"}, RuntimeContextCreated{"work"}, PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
	}
	workspace, scratch, pin := scopedID(0x400), scopedID(0x401), scopedID(0x402)
	capture := Digest(sha256.Sum256(scopedCaptureFixture()))
	appendCommit([]Draft{
		{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: workspace, CommonDir: "C:/fixture/scoped-repo/.git", ObjectFormat: "sha1"})},
		{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{capture}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: workspace, Revision: 1, CaptureInputsSHA256: capture, PinID: pin})},
		{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{capture}, Payload: mustEncode(BodyPin{PinID: pin})},
	})
	appendCommit([]Draft{{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: w1, Payload: mustEncode(ScratchCreated{ScratchID: scratch, WorkspaceID: workspace, ContextID: w1, Dir: "0a1b2c3d", Branch: "refs/heads/dwarf-harness/0a1b2c3d", BaseCommit: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", AnchorTree: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"})}})

	add := func(s ScopedSubmission, version int64, result string, work, turn uuid.UUID, effects ...payload) {
		command := scopedID(0x1000 + len(out) + 1)
		hash := Digest(sha256.Sum256(mustEncode(s)))
		draft := func(typ string, actor Actor, p payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: s.Origin.AgentID, ExecutionSessionID: s.Origin.SessionID, ContextID: s.Origin.ContextID, OperationID: command, TurnID: turn, Payload: mustEncode(p)}
		}
		drafts := []Draft{draft(TypeCommand, Actor(s.Actor), Command{command, hash})}
		for _, p := range effects {
			typ := scopedPayloadType(p)
			actor := ActorCore
			switch p.(type) {
			case RuntimeGrantIssued, RuntimeGrantRevoked, RuntimeCapacityConfigured, RuntimeTaskChanged:
				actor = Actor(s.Actor)
			}
			d := draft(typ, actor, p)
			switch q := p.(type) {
			case RuntimeTaskChanged:
				if q.State != "completed" && q.State != "failed" && q.State != "cancelled" {
					d.BodyRefs = []Digest{q.Description.SHA256}
				}
			case BodyPin:
				for _, effect := range effects {
					if task, ok := effect.(RuntimeTaskChanged); ok {
						d.BodyRefs = []Digest{task.Description.SHA256}
					}
				}
			}
			drafts = append(drafts, d)
		}
		drafts = append(drafts, draft(TypeRuntimeScopedReceipt, Actor(s.Actor), RuntimeScopedReceipt{command, hash, s, result, version, work}))
		appendCommit(drafts)
	}
	sub := func(actor string, context uuid.UUID, op string, target Resource, grant uuid.UUID, e ScopedExpected, args any) ScopedSubmission {
		return ScopedSubmission{2, actor, origin(context), op, target, grant, e, args}
	}
	limits := Capacity{3, 4, 256 << 20, 100, 4 << 20}
	for _, scope := range []ScopeKey{{Kind: "colony"}, {"agent", agent}, {"context", general}, {"context", w1}} {
		cap := limits
		if scope.Kind == "context" {
			cap.TurnSlots = 1
		}
		context := general
		if scope.Kind == "context" {
			context = scope.ID
		}
		add(sub("owner", context, "capacity.configure", contextTarget(context), uuid.UUID{}, ScopedExpected{}, CapacityArguments{scope, cap}), 1, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeCapacityConfigured{scope, 0, 1, cap})
	}
	var authority int64
	usage := func(context, grant uuid.UUID, amount int64, ancestors ...uuid.UUID) RuntimeLocalUsed {
		scopes := []ScopeKey{{Kind: "colony"}, {"agent", agent}, {"context", context}, {"grant", grant}}
		for _, id := range ancestors {
			scopes = append(scopes, ScopeKey{"grant", id})
		}
		slices.SortFunc(scopes, func(a, b ScopeKey) int {
			if scopeRank(a.Kind) != scopeRank(b.Kind) {
				return scopeRank(a.Kind) - scopeRank(b.Kind)
			}
			return bytes.Compare(a.ID[:], b.ID[:])
		})
		return RuntimeLocalUsed{AuthorityVersion: authority, GrantID: grant, Scopes: scopes, Units: 1, Bytes: amount}
	}
	issue := func(g RuntimeGrantIssued, actor string, context, grant uuid.UUID) {
		g.ExpectedAuthorityVersion = authority
		g.AuthorityVersion = authority + 1
		s := sub(actor, context, "grant.issue", g.Resources[0], grant, ScopedExpected{AuthorityVersion: new(authority)}, scopedIssueArguments(g))
		effects := []payload{g}
		if actor == "agent" {
			effects = append(effects, usage(context, grant, 0))
		}
		authority++
		add(s, authority, "applied", uuid.UUID{}, uuid.UUID{}, effects...)
	}
	rootID, privateID, childID, artifactID := scopedID(0x300), scopedID(0x301), scopedID(0x302), scopedID(0x303)
	workspaceTarget := Resource{Kind: "workspace", WorkspaceID: workspace, Binding: binding(w1), PathPrefix: "src"}
	root := RuntimeGrantIssued{GrantID: rootID, Grantee: binding(general), Operations: []string{"file.read", "grant.issue"}, Resources: []Resource{workspaceTarget}, DataClass: "workspace", Recipients: []Binding{binding(w1)}, Limits: limits, DelegationDepth: 1, Lifetime: "until_revoked"}
	issue(root, "owner", general, uuid.UUID{})
	createTask := func(id uuid.UUID, text, actor string, grant uuid.UUID) RuntimeTaskChanged {
		task := RuntimeTaskChanged{TaskID: id, Action: "create", Version: 1, CreatorActor: actor, Creator: origin(general), Description: ObjectRef{scopedHash(text), int64(len(text))}, AcceptanceSHA256: scopedHash("Task acceptance: exact stated goal.\n"), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{agent}, State: "waiting"}
		s := sub(actor, general, "task.create", Resource{Kind: "task", TaskID: id}, grant, ScopedExpected{}, TaskCreateArguments{id, task.Description, task.AcceptanceSHA256, task.Dependencies, task.Audience, nil})
		effects := []payload{BodyPin{PinID: id}, task}
		if actor == "agent" {
			effects = append(effects, usage(general, grant, task.Description.Bytes))
		}
		add(s, 1, "applied", uuid.UUID{}, uuid.UUID{}, effects...)
		return task
	}
	taskA := createTask(scopedID(0x200), scopedDescriptionA, "owner", uuid.UUID{})
	artifact := ArtifactRef{sequence - 1, taskA.Description.SHA256, taskA.Description.Bytes}
	mutate := func(task RuntimeTaskChanged, actor string, context, grant uuid.UUID, args TaskMutationArguments) RuntimeTaskChanged {
		e := ScopedExpected{Version: task.Version}
		if task.Assignment != nil {
			e.AssignmentID = task.Assignment.AssignmentID
			e.Generation = new(task.Assignment.Generation)
		}
		task.Action = args.Action
		task.ExpectedVersion = task.Version
		task.Version++
		task.WaitReason = args.WaitReason
		task.WaitArtifact = args.WaitArtifact
		task.Submission = args.Submission
		task.FailureCause = args.FailureCause
		task.FailureEvidence = args.FailureEvidence
		task.State = "waiting"
		switch args.Action {
		case "start":
			task.State = "active"
		case "submit":
			task.State = "submitted"
		case "complete":
			task.State = "completed"
		case "fail":
			task.State = "failed"
		case "cancel":
			task.State = "cancelled"
		}
		if args.NewAssignment != nil {
			generation := int64(1)
			if task.Assignment != nil {
				generation = task.Assignment.Generation + 1
			}
			task.Assignment = &Assignment{args.NewAssignment.AssignmentID, generation, args.NewAssignment.Assignee, actor, origin(context)}
		}
		s := sub(actor, context, "task."+args.Action, Resource{Kind: "task", TaskID: task.TaskID}, grant, e, args)
		effects := []payload{task}
		if task.State == "completed" || task.State == "failed" || task.State == "cancelled" {
			effects = append(effects, BodyUnpin{PinID: task.TaskID})
		}
		if actor == "agent" {
			effects = append(effects, usage(context, grant, 0))
		}
		add(s, task.Version, "applied", uuid.UUID{}, uuid.UUID{}, effects...)
		return task
	}
	taskA = mutate(taskA, "owner", general, uuid.UUID{}, TaskMutationArguments{TaskID: taskA.TaskID, Action: "assign", NewAssignment: &AssignmentInput{scopedID(0x210), binding(w1)}})
	boundA := &TaskBinding{taskA.TaskID, taskA.Assignment.AssignmentID, 1}
	private := RuntimeGrantIssued{GrantID: privateID, Grantee: binding(w1), Operations: []string{"private.read", "private.write", "turn.start"}, Resources: []Resource{contextTarget(w1, "memory")}, DataClass: "private", Recipients: []Binding{binding(w1)}, Limits: limits, Lifetime: "until_revoked", TaskBinding: boundA}
	issue(private, "owner", general, uuid.UUID{})
	child := root
	child.GrantID = childID
	child.Grantee = binding(w1)
	child.ParentGrantID = rootID
	child.Operations = []string{"file.read"}
	child.DelegationDepth = 0
	child.TaskBinding = boundA
	issue(child, "agent", general, rootID)
	artifactGrant := RuntimeGrantIssued{GrantID: artifactID, Grantee: binding(w1), Operations: []string{"artifact.read"}, Resources: []Resource{{Kind: "artifact", Artifact: artifact}}, DataClass: "artifact", Recipients: []Binding{binding(w1)}, Limits: limits, Lifetime: "until_revoked", TaskBinding: boundA}
	issue(artifactGrant, "owner", general, uuid.UUID{})
	change := privateFixtureChange("memory", "replace", scopedMemory)
	add(sub("agent", w1, "private.write", contextTarget(w1, "memory"), privateID, ScopedExpected{Version: 1}, PrivateWriteArguments{[]PrivateChange{change}}), 2, "applied", uuid.UUID{}, uuid.UUID{}, PrivateScopedRevision{"context", 1, 2, []PrivateChange{change}}, usage(w1, privateID, change.Bytes))
	memorySequence := sequence - 2
	read := PrivateReadRef{"context", "memory", 2, 0, 1024}
	u := usage(w1, privateID, change.Bytes)
	u.Read = &read
	u.Selected = []SelectedObject{{change.SHA256, change.Bytes, 2, memorySequence}}
	add(sub("agent", w1, "private.read", contextTarget(w1, "memory"), privateID, ScopedExpected{}, PrivateReadArguments{read}), 0, "applied", uuid.UUID{}, uuid.UUID{}, u)
	u = usage(w1, artifactID, artifact.Bytes)
	u.Artifact = &artifact
	add(sub("agent", w1, "artifact.read", Resource{Kind: "artifact", Artifact: artifact}, artifactID, ScopedExpected{}, ArtifactReadArguments{artifact, 1024}), 0, "applied", uuid.UUID{}, uuid.UUID{}, u)
	taskA = mutate(taskA, "owner", general, uuid.UUID{}, TaskMutationArguments{TaskID: taskA.TaskID, Action: "start"})
	turn, local := scopedID(0x500), scopedID(0x501)
	add(sub("agent", w1, "turn.start", contextTarget(w1), privateID, ScopedExpected{}, TurnStartArguments{turn, boundA}), 1, "admitted", turn, turn, RuntimeWorkAdmitted{WorkID: turn, Kind: "turn", Version: 1, AuthorityVersion: authority, GrantID: privateID, Scopes: usage(w1, privateID, 0).Scopes, Operation: "turn.start", Target: contextTarget(w1), TaskBinding: boundA})
	localTarget := workspaceTarget
	localTarget.PathPrefix = "src/main.go"
	arguments := scopedHash(`{"path":"src/main.go"}`)
	add(sub("agent", w1, "local.admit", localTarget, childID, ScopedExpected{}, LocalAdmissionArguments{local, turn, "file.read", arguments, 1, 1024, boundA}), 1, "admitted", local, turn, RuntimeWorkAdmitted{WorkID: local, Kind: "local", Version: 1, AuthorityVersion: authority, GrantID: childID, Scopes: usage(w1, childID, 0, rootID).Scopes, Operation: "file.read", Target: localTarget, TaskBinding: boundA, ParentTurnID: turn, ArgumentsSHA256: arguments, ToolSchema: new(int64(1)), MaxBytes: new(int64(1024))})
	add(sub("core", w1, "local.begin", localTarget, childID, ScopedExpected{Version: 1}, WorkDispatchArguments{local, arguments}), 2, "dispatched", local, turn, RuntimeWorkDispatched{local, 1, 2, authority, childID, arguments})
	for i, outcome := range []string{"unknown", "failed"} {
		expected := int64(2 + i)
		add(sub("core", w1, "local.end", localTarget, childID, ScopedExpected{Version: expected}, WorkEndArguments{local, outcome}), expected+1, "recorded", local, turn, RuntimeWorkEnded{local, expected, expected + 1, outcome})
	}
	add(sub("core", w1, "turn.end", contextTarget(w1), privateID, ScopedExpected{Version: 1}, WorkEndArguments{turn, "interrupted"}), 2, "recorded", turn, turn, RuntimeWorkEnded{turn, 1, 2, "interrupted"})
	for _, args := range []TaskMutationArguments{{TaskID: taskA.TaskID, Action: "wait", WaitReason: "input"}, {TaskID: taskA.TaskID, Action: "resume"}, {TaskID: taskA.TaskID, Action: "start"}, {TaskID: taskA.TaskID, Action: "submit", Submission: &SubjectRef{Kind: "artifact", Artifact: artifact}}, {TaskID: taskA.TaskID, Action: "complete", Submission: &SubjectRef{Kind: "artifact", Artifact: artifact}}} {
		taskA = mutate(taskA, "owner", general, uuid.UUID{}, args)
	}
	add(sub("owner", general, "grant.revoke", workspaceTarget, uuid.UUID{}, ScopedExpected{Version: 1, AuthorityVersion: new(authority)}, GrantRevokeArguments{childID}), authority+1, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeGrantRevoked{childID, authority, authority + 1})
	authority++
	// Additional roots make the three agent task shapes authorized, not merely
	// owner records relabelled agent. The named-task root follows creation.
	createID, mutateID := scopedID(0x304), scopedID(0x305)
	createGrant := RuntimeGrantIssued{GrantID: createID, Grantee: binding(general), Operations: []string{"task.create"}, Resources: []Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{binding(general), binding(w2), binding(w3)}, Limits: limits, Lifetime: "until_revoked"}
	issue(createGrant, "owner", general, uuid.UUID{})
	taskB := createTask(scopedID(0x201), scopedDescriptionB, "agent", createID)
	mutateGrant := createGrant
	mutateGrant.GrantID = mutateID
	mutateGrant.Operations = []string{"task.assign", "task.cancel", "task.reassign"}
	mutateGrant.Resources = []Resource{{Kind: "task", TaskID: taskB.TaskID}}
	issue(mutateGrant, "owner", general, uuid.UUID{})
	taskB = mutate(taskB, "owner", general, uuid.UUID{}, TaskMutationArguments{TaskID: taskB.TaskID, Action: "assign", NewAssignment: &AssignmentInput{scopedID(0x211), binding(w2)}})
	taskB = mutate(taskB, "agent", general, mutateID, TaskMutationArguments{TaskID: taskB.TaskID, Action: "reassign", NewAssignment: &AssignmentInput{scopedID(0x212), binding(w3)}})
	mutate(taskB, "agent", general, mutateID, TaskMutationArguments{TaskID: taskB.TaskID, Action: "cancel"})
	limits.LocalUnits = 2
	add(sub("owner", general, "capacity.configure", contextTarget(general), uuid.UUID{}, ScopedExpected{Version: 1}, CapacityArguments{ScopeKey{Kind: "colony"}, limits}), 2, "applied", uuid.UUID{}, uuid.UUID{}, RuntimeCapacityConfigured{ScopeKey{Kind: "colony"}, 1, 2, limits})
	return out
}

func scopedPayloadType(p payload) string {
	switch p.(type) {
	case BodyPin:
		return TypeBodyPin
	case BodyUnpin:
		return TypeBodyUnpin
	case RuntimeGrantIssued:
		return TypeRuntimeGrantIssued
	case RuntimeGrantRevoked:
		return TypeRuntimeGrantRevoked
	case RuntimeTaskChanged:
		return TypeRuntimeTaskChanged
	case RuntimeCapacityConfigured:
		return TypeRuntimeCapacityConfigured
	case RuntimeWorkAdmitted:
		return TypeRuntimeWorkAdmitted
	case RuntimeWorkDispatched:
		return TypeRuntimeWorkDispatched
	case RuntimeWorkEnded:
		return TypeRuntimeWorkEnded
	case RuntimeLocalUsed:
		return TypeRuntimeLocalUsed
	case PrivateScopedRevision:
		return TypePrivateScopedRevision
	case RuntimeScopedReceipt:
		return TypeRuntimeScopedReceipt
	}
	panic(fmt.Sprintf("unexpected scoped effect %T", p))
}

func TestGoldenF3_11(t *testing.T) {
	t.Parallel()
	g := mustScoped(t)
	files := scopedFixtureFiles()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(scopedDir)
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
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("CR byte in %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	stored := snapshot(t, g.dir)
	if !slices.Equal(slices.Sorted(maps.Keys(stored)), g.names) {
		t.Fatal("unexpected golden segments")
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: scopedTypes})
	if err != nil || len(commits) != 39 || cursor != 130 {
		t.Fatalf("commits=%d cursor=%d: %v; want 39/130", len(commits), cursor, err)
	}
	shapes := map[string]bool{}
	for i, c := range commits {
		for _, r := range c.Records {
			if !Defined().Contains(TypeVersion{r.Type, r.TypeVersion}) {
				t.Fatalf("seq %d type not in Defined", r.Seq)
			}
		}
		if i < 6 {
			continue
		}
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := DecodeRuntimeScopedReceipt(c.Records[len(c.Records)-1].Payload)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 {
			t.Fatal("command/receipt mismatch")
		}
		var types []string
		for j, r := range c.Records {
			types = append(types, r.Type)
			actor := Actor(receipt.Submission.Actor)
			switch r.Type {
			case TypeRuntimeWorkAdmitted, TypeRuntimeWorkDispatched, TypeRuntimeWorkEnded, TypeRuntimeLocalUsed, TypePrivateScopedRevision, TypeBodyPin, TypeBodyUnpin:
				actor = ActorCore
			}
			o := receipt.Submission.Origin
			if r.Actor != actor || r.AgentID != o.AgentID || r.ExecutionSessionID != o.SessionID || r.ContextID != o.ContextID || r.OperationID != command.CommandID || r.MeasurementPath != "" {
				t.Fatalf("seq %d invalid group identity", r.Seq)
			}
			if scopedWorkOperation(receipt.Submission.Operation) != (r.TurnID != uuid.UUID{}) {
				t.Fatalf("seq %d turn mismatch", r.Seq)
			}
			if j > 0 && j < len(c.Records)-1 && r.Type != TypeBodyPin && r.Type != TypeBodyUnpin {
				p, err := decodeF3_11(r.Type, r.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if encoded, err := p.Encode(); err != nil || !bytes.Equal(encoded, r.Payload) {
					t.Fatalf("seq %d codec roundtrip: %v", r.Seq, err)
				}
			}
			var refs []Digest
			if r.Type == TypeRuntimeTaskChanged {
				task, err := DecodeRuntimeTaskChanged(r.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if task.State != "completed" && task.State != "failed" && task.State != "cancelled" {
					refs = []Digest{task.Description.SHA256}
				}
			}
			if r.Type == TypeBodyPin {
				next, err := DecodeRuntimeTaskChanged(c.Records[j+1].Payload)
				if err != nil {
					t.Fatal(err)
				}
				refs = []Digest{next.Description.SHA256}
			}
			if !slices.Equal(r.BodyRefs, refs) {
				t.Fatalf("seq %d retention mismatch", r.Seq)
			}
		}
		shapes[fmt.Sprint(types)] = true
	}
	for _, middle := range [][]string{{TypeRuntimeGrantIssued}, {TypeRuntimeGrantRevoked}, {TypeRuntimeCapacityConfigured}, {TypeRuntimeGrantIssued, TypeRuntimeLocalUsed}, {TypeBodyPin, TypeRuntimeTaskChanged}, {TypeBodyPin, TypeRuntimeTaskChanged, TypeRuntimeLocalUsed}, {TypeRuntimeTaskChanged}, {TypeRuntimeTaskChanged, TypeRuntimeLocalUsed}, {TypeRuntimeTaskChanged, TypeBodyUnpin}, {TypeRuntimeTaskChanged, TypeBodyUnpin, TypeRuntimeLocalUsed}, {TypeRuntimeWorkAdmitted}, {TypeRuntimeWorkDispatched}, {TypeRuntimeWorkEnded}, {TypePrivateScopedRevision, TypeRuntimeLocalUsed}, {TypeRuntimeLocalUsed}} {
		shape := append([]string{TypeCommand}, middle...)
		shape = append(shape, TypeRuntimeScopedReceipt)
		if !shapes[fmt.Sprint(shape)] {
			t.Fatalf("missing group shape %v", shape)
		}
	}
	result, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || result != want {
		t.Fatalf("Check=%+v %v; want %+v", result, err, want)
	}
	t.Log("commits=39 records=130")
}

func TestF3_11Compatibility(t *testing.T) {
	t.Parallel()
	g := mustScoped(t)
	older := Consumption{TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1}, TypeBodiesCollected: {1}}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: older})
	var wantSeqs, wantLasts []uint64
	var seq uint64
	for _, input := range g.inputs {
		first := len(wantSeqs)
		for _, d := range input.drafts {
			seq++
			if d.Type == TypeCommand || d.Type == TypeBodyPin || d.Type == TypeBodyUnpin {
				wantSeqs = append(wantSeqs, seq)
			}
		}
		if len(wantSeqs) > first {
			wantLasts = append(wantLasts, seq)
		}
	}
	if err != nil || cursor != 130 || !slices.Equal(seqs(commits), wantSeqs) || !slices.Equal(lasts(commits), wantLasts) {
		t.Fatalf("older consumer seqs=%v lasts=%v cursor=%d: %v", seqs(commits), lasts(commits), cursor, err)
	}
	for _, c := range commits {
		got, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, After: c.Last, Through: ToEnd, Consume: older})
		var remaining []uint64
		for _, seq := range wantSeqs {
			if seq > c.Last {
				remaining = append(remaining, seq)
			}
		}
		if err != nil || cursor != 130 || !slices.Equal(seqs(got), remaining) {
			t.Fatalf("resume after %d: %v cursor=%d %v", c.Last, seqs(got), cursor, err)
		}
	}
}

// The unchanged historical capture-inputs/1 vector from workspace's
// TestCaptureInputsKeepHistoricalGitFloor. No optional host files are named,
// so the document itself is the complete registration body_refs set.
func scopedCaptureFixture() []byte {
	return []byte(`{"format":"dwarf-harness/capture-inputs","version":1,"rules":"capture-rules/1","git_version":"2.42.0","object_format":"sha1","core.autocrlf":"false","core.eol":"native","core.filemode":"true","core.ignorecase":"false","core.longpaths":"false","core.precomposeunicode":"false","core.symlinks":"true"}`)
}
