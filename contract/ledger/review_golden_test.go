package ledger

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"
)

const reviewDir = "testdata/f3/12/log"
const reviewWorkerInstructions = "# Review worker\n"
const reviewReviewerInstructions = "# Review judge\n"

var reviewDescriptions = []string{"Implement the reviewed task.\n", "Assess the private memory failure.\n", "Assess the artifact failure.\n"}
var reviewColony = reviewID(1)
var reviewTypes = Consumption{
	TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
	TypeRuntimeAgentRegistered: {1}, TypeRuntimeContextCreated: {1}, TypePrivateRevision: {1}, TypeRuntimeCommandReceipt: {1},
	TypeRuntimeCapacityConfigured: {1}, TypeRuntimeTaskChanged: {1}, TypeRuntimeGrantIssued: {1}, TypeRuntimeScopedReceipt: {1},
	TypeRuntimeReviewRequested: {1}, TypeRuntimeReviewResult: {1}, TypeRuntimeReviewReceipt: {1},
}
var reviewLog = sync.OnceValues(func() (*goldenLog, error) { return buildLog(reviewDir, reviewColony, reviewTypes, reviewInputs()) })

func mustReview(t testing.TB) *goldenLog { t.Helper(); return mustBuild(t, reviewLog) }
func reviewID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1200-0000-7000-8000-%012x", n))
}
func reviewFixtureFiles() map[string][]byte {
	files := map[string][]byte{}
	for _, text := range []string{reviewWorkerInstructions, reviewReviewerInstructions, ""} {
		files["objects/"+scopedHash(text).String()] = []byte(text)
	}
	for _, text := range reviewDescriptions {
		files["bodies/"+scopedHash(text).String()] = []byte(text)
	}
	return files
}

// reviewInputs supplies real historical prerequisites without a service import.
// Sequences are read back from each completed fixture group; hashes are encoded.
func reviewInputs() []goldenCommit {
	worker := Origin{reviewID(0xa1), reviewID(0xb1), reviewID(0xd1)}
	reviewer := Origin{reviewID(0xa2), reviewID(0xb2), reviewID(0xd2)}
	w1, w2, w3 := worker, worker, worker
	w1.ContextID, w2.ContextID, w3.ContextID = reviewID(0xd3), reviewID(0xd4), reviewID(0xd5)
	binding := func(o Origin) Binding { return Binding{o.AgentID, o.ContextID} }
	contextTarget := func(o Origin) Resource {
		return Resource{Kind: "context", Binding: binding(o), Scope: "context", Slots: []string{}}
	}
	var out []goldenCommit
	var sequence int64
	const base int64 = 1791000800000000000
	appendCommit := func(drafts []Draft) {
		i := len(out) + 1
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: reviewColony, CommitID: reviewID(0x2000 + i), EventTimeNS: base + int64(i)*1000000000}, drafts: drafts})
		sequence += int64(len(drafts))
	}
	draft := func(o Origin, command uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: o.AgentID, ExecutionSessionID: o.SessionID, ContextID: o.ContextID, OperationID: command, Payload: mustEncode(p)}
	}
	identity := func(o Origin, op string, args any, effects ...payload) {
		command := reviewID(0x1000 + len(out) + 1)
		scope := "context"
		if op == "agent_register" {
			scope = "agent"
		}
		target := PrivateTarget{scope, []string{}}
		sub := struct {
			Schema          int           `json:"schema"`
			Actor           string        `json:"actor"`
			AgentID         uuid.UUID     `json:"agent_id"`
			SessionID       uuid.UUID     `json:"session_id"`
			ContextID       uuid.UUID     `json:"context_id"`
			Operation       string        `json:"operation"`
			Target          PrivateTarget `json:"target"`
			ExpectedVersion int64         `json:"expected_version"`
			Arguments       any           `json:"arguments"`
		}{1, "owner", o.AgentID, o.SessionID, o.ContextID, op, target, 0, args}
		hash := Digest(sha256.Sum256(mustJSON(sub)))
		drafts := []Draft{draft(o, command, TypeCommand, ActorOwner, Command{command, hash})}
		for _, effect := range effects {
			typ, actor := TypePrivateRevision, ActorCore
			switch effect.(type) {
			case RuntimeAgentRegistered:
				typ, actor = TypeRuntimeAgentRegistered, ActorOwner
			case RuntimeContextCreated:
				typ, actor = TypeRuntimeContextCreated, ActorOwner
			}
			drafts = append(drafts, draft(o, command, typ, actor, effect))
		}
		drafts = append(drafts, draft(o, command, TypeRuntimeCommandReceipt, ActorOwner, RuntimeCommandReceipt{CommandID: command, SubmissionSHA256: hash, Operation: op, Target: target, Outcome: "applied"}))
		appendCommit(drafts)
	}
	emptyMemory, emptyHandoff := privateFixtureChange("memory", "replace", ""), privateFixtureChange("handoff", "replace", "")
	for _, row := range []struct {
		o               Origin
		directory, text string
	}{{worker, "Worker-a1", reviewWorkerInstructions}, {reviewer, "Reviewer-a2", reviewReviewerInstructions}} {
		instructions := privateFixtureChange("instructions", "replace", row.text)
		args := struct {
			Directory   string `json:"directory"`
			AgentSHA256 Digest `json:"agent_sha256"`
			AgentBytes  int64  `json:"agent_bytes"`
		}{row.directory, instructions.SHA256, instructions.Bytes}
		identity(row.o, "agent_register", args, RuntimeAgentRegistered{row.directory, row.o.ContextID}, RuntimeContextCreated{"general"}, PrivateRevision{"agent", 0, 1, []PrivateChange{instructions, emptyMemory}}, PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
	}
	var memorySource int64
	for _, o := range []Origin{w1, w2, w3} {
		identity(o, "context_create", RuntimeContextCreated{"work"}, RuntimeContextCreated{"work"}, PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
		if o == w2 {
			memorySource = sequence - 1
		}
	}
	addScoped := func(s ScopedSubmission, version int64, effects ...payload) {
		command := reviewID(0x1000 + len(out) + 1)
		hash := Digest(sha256.Sum256(mustEncode(s)))
		drafts := []Draft{draft(s.Origin, command, TypeCommand, Actor(s.Actor), Command{command, hash})}
		for _, effect := range effects {
			typ := scopedPayloadType(effect)
			actor := Actor(s.Actor)
			if typ == TypeBodyPin {
				actor = ActorCore
			}
			d := draft(s.Origin, command, typ, actor, effect)
			if task, ok := effect.(RuntimeTaskChanged); ok {
				d.BodyRefs = []Digest{task.Description.SHA256}
			}
			if _, ok := effect.(BodyPin); ok {
				for _, p := range effects {
					if task, ok := p.(RuntimeTaskChanged); ok {
						d.BodyRefs = []Digest{task.Description.SHA256}
					}
				}
			}
			drafts = append(drafts, d)
		}
		drafts = append(drafts, draft(s.Origin, command, TypeRuntimeScopedReceipt, Actor(s.Actor), RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: hash, Submission: s, Outcome: "applied", Version: version}))
		appendCommit(drafts)
	}
	limits := Capacity{3, 8, 134217728, 100, 4194304}
	for _, scope := range []ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: worker.AgentID}, {Kind: "agent", ID: reviewer.AgentID}} {
		o := worker
		if scope.ID == reviewer.AgentID {
			o = reviewer
		}
		s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: o, Operation: "capacity.configure", Target: contextTarget(o), Arguments: CapacityArguments{scope, limits}}
		addScoped(s, 1, RuntimeCapacityConfigured{scope, 0, 1, limits})
	}
	createTask := func(n int, o Origin, text string) (RuntimeTaskChanged, ArtifactRef) {
		id := reviewID(n)
		a := &Assignment{reviewID(n + 0x10), 1, binding(o), "owner", worker}
		task := RuntimeTaskChanged{TaskID: id, Action: "create", Version: 1, CreatorActor: "owner", Creator: worker, Description: ObjectRef{scopedHash(text), int64(len(text))}, AcceptanceSHA256: scopedHash("Acceptance: exact task and evidence.\n"), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{worker.AgentID, reviewer.AgentID}, Assignment: a, State: "waiting"}
		s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: worker, Operation: "task.create", Target: Resource{Kind: "task", TaskID: id}, Arguments: TaskCreateArguments{id, task.Description, task.AcceptanceSHA256, task.Dependencies, task.Audience, &AssignmentInput{a.AssignmentID, a.Assignee}}}
		addScoped(s, 1, BodyPin{id}, task)
		return task, ArtifactRef{sequence - 1, task.Description.SHA256, task.Description.Bytes}
	}
	taskA, artifactA := createTask(0x200, w1, reviewDescriptions[0])
	taskB, _ := createTask(0x201, w2, reviewDescriptions[1])
	taskD, artifactD := createTask(0x202, w3, reviewDescriptions[2])
	var authority int64
	issue := func(id uuid.UUID, o Origin, operations []string, resources []Resource, dataClass string, recipients []Binding) {
		slices.SortFunc(resources, func(a, b Resource) int { return bytes.Compare(mustJSON(a), mustJSON(b)) })
		slices.SortFunc(recipients, func(a, b Binding) int {
			if n := bytes.Compare(a.AgentID[:], b.AgentID[:]); n != 0 {
				return n
			}
			return bytes.Compare(a.ContextID[:], b.ContextID[:])
		})
		g := RuntimeGrantIssued{GrantID: id, ExpectedAuthorityVersion: authority, AuthorityVersion: authority + 1, Grantee: binding(o), Operations: operations, Resources: resources, DataClass: dataClass, Recipients: recipients, Limits: limits, Lifetime: "until_revoked"}
		s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: worker, Operation: "grant.issue", Target: resources[0], Expected: ScopedExpected{AuthorityVersion: new(authority)}, Arguments: scopedIssueArguments(g)}
		authority++
		addScoped(s, authority, g)
	}
	requestGrant, failureGrant, reviewerGrant, turnGrant := reviewID(0x300), reviewID(0x301), reviewID(0x302), reviewID(0x303)
	issue(requestGrant, w1, []string{"task.complete", "task.inspect"}, []Resource{{Kind: "task", TaskID: taskA.TaskID}}, "task", []Binding{binding(w1), binding(reviewer)})
	issue(failureGrant, w3, []string{"task.fail", "task.inspect"}, []Resource{{Kind: "task", TaskID: taskD.TaskID}}, "task", []Binding{binding(w3), binding(reviewer)})
	issue(reviewerGrant, reviewer, []string{"task.complete", "task.fail", "task.inspect"}, []Resource{{Kind: "task", TaskID: taskA.TaskID}, {Kind: "task", TaskID: taskB.TaskID}, {Kind: "task", TaskID: taskD.TaskID}}, "task", []Binding{binding(reviewer)})
	issue(turnGrant, w1, []string{"turn.start"}, []Resource{contextTarget(w1)}, "private", []Binding{binding(w1)})
	for _, action := range []string{"start", "submit"} {
		e := ScopedExpected{Version: taskA.Version, AssignmentID: taskA.Assignment.AssignmentID, Generation: new(int64(1))}
		a := TaskMutationArguments{TaskID: taskA.TaskID, Action: action}
		taskA.Action, taskA.ExpectedVersion, taskA.Version = action, taskA.Version, taskA.Version+1
		taskA.State = "active"
		if action == "submit" {
			taskA.State = "submitted"
			a.Submission = &SubjectRef{Kind: "artifact", Artifact: artifactA}
			taskA.Submission = a.Submission
		}
		s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: worker, Operation: "task." + action, Target: Resource{Kind: "task", TaskID: taskA.TaskID}, Expected: e, Arguments: a}
		addScoped(s, taskA.Version, taskA)
	}
	makeBinding := func(task RuntimeTaskChanged, subject SubjectRef, principal ReviewPrincipal) ReviewBinding {
		return ReviewBinding{TaskID: task.TaskID, TaskVersion: task.Version, AssignmentID: task.Assignment.AssignmentID, Generation: task.Assignment.Generation, Assignee: task.Assignment.Assignee, Reviewer: principal, Subject: subject, AcceptanceSHA256: task.AcceptanceSHA256, CheckSHA256: scopedHash("Review checklist: inspect exact subject and evidence.\n"), ConfigSHA256: scopedHash("Review configuration: semantic judgment only.\n"), RequiredEvidence: []SubjectRef{subject}, AuthorizationRevision: authority, DeadlineUTCNS: base + 24*60*60*1000000000}
	}
	addReview := func(s ReviewSubmission, requestID uuid.UUID, version, taskVersion int64, outcome string, effect payload) {
		command := reviewID(0x1000 + len(out) + 1)
		hash := Digest(sha256.Sum256(mustEncode(s)))
		typ := TypeRuntimeReviewRequested
		if _, ok := effect.(RuntimeReviewResult); ok {
			typ = TypeRuntimeReviewResult
		}
		drafts := []Draft{draft(s.Origin, command, TypeCommand, Actor(s.Actor), Command{command, hash}), draft(s.Origin, command, typ, Actor(s.Actor), effect)}
		if result, ok := effect.(RuntimeReviewResult); ok && result.Verdict != "reject" {
			drafts = append(drafts, draft(s.Origin, command, TypeBodyUnpin, ActorCore, BodyUnpin{result.Binding.TaskID}))
		}
		r := RuntimeReviewReceipt{CommandID: command, SubmissionSHA256: hash, Submission: s, Outcome: outcome, Version: version, RequestID: requestID, TaskVersion: taskVersion}
		if s.Actor == "agent" {
			r.Usage = &ReviewUsage{authority, s.GrantID, []ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: s.Origin.AgentID}, {Kind: "context", ID: s.Origin.ContextID}, {Kind: "grant", ID: s.GrantID}}, 1, 0}
		}
		drafts = append(drafts, draft(s.Origin, command, TypeRuntimeReviewReceipt, Actor(s.Actor), r))
		appendCommit(drafts)
	}
	request := func(id uuid.UUID, kind, actor string, o Origin, grant uuid.UUID, b ReviewBinding, replaces uuid.UUID) RuntimeReviewRequested {
		p := RuntimeReviewRequested{RequestID: id, Kind: kind, Version: 1, RequesterActor: actor, Requester: o, RequesterGrantID: grant, Binding: b, ReplacesRequestID: replaces, ObservedUTCNS: base + int64(len(out)+1)*1000000000}
		if kind == "failure" {
			p.FailureCause = "semantic_failure"
			p.FailingEvidence = &p.Binding.RequiredEvidence[0]
		}
		s := ReviewSubmission{Schema: 3, Actor: actor, Origin: o, Operation: kind + ".request", GrantID: grant, Arguments: ReviewRequestArguments{id, b, replaces, p.FailureCause, p.FailingEvidence}}
		addReview(s, id, 1, b.TaskVersion, "requested", p)
		return p
	}
	resolve := func(p RuntimeReviewRequested, source int64, verdict string) {
		b := p.Binding
		r := RuntimeReviewResult{RequestID: p.RequestID, RequestSequence: source, ExpectedVersion: 1, Version: 2, Binding: b, Verdict: verdict, TaskVersion: b.TaskVersion, ObservedUTCNS: base + int64(len(out)+1)*1000000000}
		s := ReviewSubmission{Schema: 3, Actor: b.Reviewer.Actor, Origin: b.Reviewer.Origin, GrantID: b.Reviewer.GrantID, Operation: "review.resolve", Arguments: ReviewResolveArguments{p.RequestID, source, b, verdict}}
		outcome := "rejected"
		if verdict != "reject" {
			r.TaskVersion++
			outcome = "approved"
		}
		if verdict == "fail" {
			r.Cause, r.FailingEvidence = p.FailureCause, p.FailingEvidence
			s.Operation = "failure.resolve"
			s.Arguments = FailureResolveArguments{p.RequestID, source, b, r.Cause, *r.FailingEvidence}
			outcome = "failed"
		}
		addReview(s, p.RequestID, 2, r.TaskVersion, outcome, r)
	}
	agentReviewer := ReviewPrincipal{"agent", reviewer, reviewerGrant}
	ownerReviewer := ReviewPrincipal{Actor: "owner", Origin: worker}
	q := request(reviewID(0x400), "review", "agent", w1, requestGrant, makeBinding(taskA, SubjectRef{Kind: "artifact", Artifact: artifactA}, agentReviewer), uuid.UUID{})
	resolve(q, sequence-1, "reject")
	q = request(reviewID(0x401), "review", "owner", worker, uuid.UUID{}, makeBinding(taskA, SubjectRef{Kind: "artifact", Artifact: artifactA}, ownerReviewer), q.RequestID)
	resolve(q, sequence-1, "approve")
	private := PrivateRevisionRef{binding(w2), "context", "memory", 1, memorySource, emptyMemory.SHA256, emptyMemory.Bytes}
	q = request(reviewID(0x402), "failure", "owner", worker, uuid.UUID{}, makeBinding(taskB, SubjectRef{Kind: "private_revision", PrivateRevision: &private}, ownerReviewer), uuid.UUID{})
	resolve(q, sequence-1, "fail")
	q = request(reviewID(0x403), "failure", "agent", w3, failureGrant, makeBinding(taskD, SubjectRef{Kind: "artifact", Artifact: artifactD}, agentReviewer), uuid.UUID{})
	resolve(q, sequence-1, "fail")
	return out
}

func TestGoldenF3_12(t *testing.T) {
	t.Parallel()
	g := mustReview(t)
	files := reviewFixtureFiles()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(reviewDir)
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
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: reviewTypes})
	if err != nil || len(commits) != 25 || cursor != 90 {
		t.Fatalf("commits=%d records=%d error=%v; want 25/90", len(commits), cursor, err)
	}
	requests := map[uuid.UUID]RuntimeReviewRequested{}
	sources := map[int64]Record{}
	shapes := map[string]bool{}
	for _, c := range commits {
		for _, r := range c.Records {
			sources[int64(r.Seq)] = r
			if !Defined().Contains(TypeVersion{r.Type, r.TypeVersion}) {
				t.Fatalf("unregistered type %s", r.Type)
			}
		}
		last := c.Records[len(c.Records)-1]
		if last.Type != TypeRuntimeReviewReceipt {
			continue
		}
		r, err := DecodeRuntimeReviewReceipt(last.Payload)
		if err != nil {
			t.Fatal(err)
		}
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		if command.CommandID != r.CommandID || command.SubmissionSHA256 != r.SubmissionSHA256 {
			t.Fatal("command/receipt mismatch")
		}
		var types []string
		for _, record := range c.Records {
			types = append(types, record.Type)
			actor := Actor(r.Submission.Actor)
			if record.Type == TypeBodyUnpin {
				actor = ActorCore
			}
			o := r.Submission.Origin
			if record.Actor != actor || record.AgentID != o.AgentID || record.ExecutionSessionID != o.SessionID || record.ContextID != o.ContextID || record.OperationID != r.CommandID || record.TurnID != (uuid.UUID{}) || record.MeasurementPath != "" || record.BodyRefs != nil {
				t.Fatalf("invalid review envelope at %d", record.Seq)
			}
		}
		shapes[strings.Join(types, ",")] = true
		effect := c.Records[1]
		var b ReviewBinding
		if effect.Type == TypeRuntimeReviewRequested {
			q, err := DecodeRuntimeReviewRequested(effect.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if q.RequestID != r.RequestID || r.Version != q.Version || r.TaskVersion != q.Binding.TaskVersion || r.Outcome != "requested" {
				t.Fatal("request receipt mismatch")
			}
			if q.ReplacesRequestID != (uuid.UUID{}) {
				old, ok := requests[q.ReplacesRequestID]
				if !ok || old.Binding.TaskID != q.Binding.TaskID {
					t.Fatal("missing replacement predecessor")
				}
			}
			requests[q.RequestID] = q
			b = q.Binding
		} else {
			v, err := DecodeRuntimeReviewResult(effect.Payload)
			if err != nil {
				t.Fatal(err)
			}
			q, ok := requests[v.RequestID]
			source := sources[v.RequestSequence]
			if !ok || source.Type != TypeRuntimeReviewRequested || !bytes.Equal(source.Payload, mustEncode(q)) || !reflect.DeepEqual(v.Binding, q.Binding) {
				t.Fatal("result lost exact request identity")
			}
			if v.RequestID != r.RequestID || r.Version != v.Version || r.TaskVersion != v.TaskVersion {
				t.Fatal("result receipt mismatch")
			}
			if v.Verdict != "reject" {
				u, err := DecodeBodyUnpin(c.Records[2].Payload)
				if err != nil || u.PinID != v.Binding.TaskID {
					t.Fatal("terminal result unpinned wrong task")
				}
			}
			b = v.Binding
		}
		for _, subject := range append([]SubjectRef{b.Subject}, b.RequiredEvidence...) {
			if subject.Kind == "artifact" {
				a := subject.Artifact
				source := sources[a.SourceSequence]
				if !slices.Contains(source.BodyRefs, a.SHA256) {
					t.Fatal("artifact source mismatch")
				}
				if data := files["bodies/"+a.SHA256.String()]; int64(len(data)) != a.Bytes || Digest(sha256.Sum256(data)) != a.SHA256 {
					t.Fatal("artifact bytes mismatch")
				}
			}
			if subject.Kind == "private_revision" {
				p := *subject.PrivateRevision
				source := sources[p.SourceSequence]
				revision, err := DecodePrivateRevision(source.Payload)
				if err != nil || source.Type != TypePrivateRevision || source.AgentID != p.Binding.AgentID || source.ContextID != p.Binding.ContextID || revision.Scope != p.Scope || revision.Revision != p.Revision {
					t.Fatal("private source mismatch")
				}
				found := false
				for _, change := range revision.Changes {
					if change.Slot == p.Slot && change.SHA256 == p.SHA256 && change.Bytes == p.Bytes && change.Mode != "clear" {
						found = true
					}
				}
				if !found {
					t.Fatal("private occurrence absent")
				}
				data, ok := files["objects/"+p.SHA256.String()]
				if !ok || int64(len(data)) != p.Bytes || Digest(sha256.Sum256(data)) != p.SHA256 {
					t.Fatal("private object mismatch")
				}
			}
		}
	}
	for _, shape := range []string{TypeCommand + "," + TypeRuntimeReviewRequested + "," + TypeRuntimeReviewReceipt, TypeCommand + "," + TypeRuntimeReviewResult + "," + TypeRuntimeReviewReceipt, TypeCommand + "," + TypeRuntimeReviewResult + "," + TypeBodyUnpin + "," + TypeRuntimeReviewReceipt} {
		if !shapes[shape] {
			t.Fatalf("missing ordered group %s", shape)
		}
	}
	t.Log("commits=25 records=90; request C,Q,R; reject C,V,R; approve/fail C,V,U,R")
}
