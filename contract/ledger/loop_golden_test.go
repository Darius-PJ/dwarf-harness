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
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"
)

const loopDir = "testdata/f3/15/log"

var loopColony = loopID(0x100)
var loopTypes = func() Consumption {
	c := maps.Clone(paidTypes)
	for _, f := range loopCodecFixtures()[:6] {
		c[f.typ] = []int{1}
	}
	return c
}()
var loopLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := loopInputs()
	return buildLog(loopDir, loopColony, loopTypes, inputs)
})

func mustLoop(t testing.TB) *goldenLog { t.Helper(); return mustBuild(t, loopLog) }

// loopInputs reuses only the service-ordered F3-13 prerequisites. Every new
// digest is derived from encoded bytes; no predecessor golden is rewritten.
func loopInputs() ([]goldenCommit, map[string][]byte) {
	prior, priorFiles := paidInputs()
	var out []goldenCommit
	var sequence int64
	files := map[string][]byte{}
	general, work := scopedOriginFixture(), scopedOriginFixture()
	work.ContextID = fid("d2")
	turn := paidID(0x421)
	var admissionSequence int64
	var admissionCommand uuid.UUID
	var reserve RuntimePaidReserved
	appendCommit := func(ds []Draft) {
		now := prior[0].header.EventTimeNS
		if len(out) > 0 {
			now = out[len(out)-1].header.EventTimeNS + int64(time.Second)
		}
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: loopColony, CommitID: loopID(0x2000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	for _, c := range prior {
		if len(c.drafts) == 3 && c.drafts[1].Type == TypeRuntimePaidReserved {
			reserve, _ = DecodeRuntimePaidReserved(c.drafts[1].Payload)
			break
		}
		// An open synthetic source cannot dispatch; Open reads only metadata.
		if len(c.drafts) == 3 && c.drafts[1].Type == TypeRuntimeCredentialChanged {
			p, _ := DecodeRuntimeCredentialChanged(c.drafts[1].Payload)
			p.SourceKind, p.SourceLocator = "synthetic_source", "synthetic-only:not-a-variable-or-target"
			r, _ := DecodeRuntimePaidReceipt(c.drafts[2].Payload)
			a := r.Submission.Arguments.(PaidCredentialBindArguments)
			a.SourceKind, a.SourceLocator = p.SourceKind, p.SourceLocator
			r.Submission.Arguments = a
			r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
			c.drafts[0].Payload = mustEncode(Command{r.CommandID, r.SubmissionSHA256})
			c.drafts[1].Payload, c.drafts[2].Payload = mustEncode(p), mustEncode(r)
		}
		if len(c.drafts) == 3 && c.drafts[1].Type == TypeRuntimeGrantIssued {
			p, _ := DecodeRuntimeGrantIssued(c.drafts[1].Payload)
			if p.GrantID == paidID(0x413) {
				p.Operations = []string{"turn.inspect", "turn.start"}
				r, _ := DecodeRuntimeScopedReceipt(c.drafts[2].Payload)
				r.Submission.Arguments = scopedIssueArguments(p)
				r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
				c.drafts[0].Payload = mustEncode(Command{r.CommandID, r.SubmissionSHA256})
				c.drafts[1].Payload, c.drafts[2].Payload = mustEncode(p), mustEncode(r)
			}
		}
		appendCommit(c.drafts)
		out[len(out)-1].header.EventTimeNS = c.header.EventTimeNS
		for _, d := range c.drafts {
			for path, data := range priorFiles {
				if bytes.Contains(d.Payload, []byte(filepath.Base(path))) {
					files[path] = data
				}
			}
			if d.Type == TypeRuntimeWorkAdmitted {
				p, _ := DecodeRuntimeWorkAdmitted(d.Payload)
				if p.WorkID == turn {
					admissionSequence, admissionCommand = sequence-1, d.OperationID
				}
			}
		}
	}
	// The reused reservation's sealed input is retained only for this chapter.
	for path, data := range priorFiles {
		if strings.HasSuffix(path, reserve.InputObject.SHA256.String()) {
			files[path] = data
		}
	}
	object := func(text string) ObjectRef {
		h := scopedHash(text)
		files["objects/agents/Ada-a1/contexts/"+work.ContextID.String()+"/core/objects/"+h.String()] = []byte(text)
		return ObjectRef{h, int64(len(text))}
	}
	draft := func(o Origin, command, turnID uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: o.AgentID, ExecutionSessionID: o.SessionID, ContextID: o.ContextID, OperationID: command, TurnID: turnID, Payload: mustEncode(p)}
	}
	addLoop := func(s LoopSubmission, effects ...payload) int64 {
		command := loopID(0x1000 + len(out))
		h := Digest(sha256.Sum256(mustEncode(s)))
		ds := []Draft{draft(work, command, turn, TypeCommand, Actor(s.Actor), Command{command, h})}
		for _, p := range effects {
			ds = append(ds, draft(work, command, turn, loopPayloadType(p), ActorCore, p))
		}
		effectSequence := sequence + int64(len(ds))
		r := RuntimeLoopReceipt{command, h, s, "applied", effectSequence}
		ds = append(ds, draft(work, command, turn, TypeRuntimeLoopReceipt, Actor(s.Actor), r))
		appendCommit(ds)
		return effectSequence
	}
	addPaid := func(s PaidSubmission, version int64, outcome string, typ string, p payload, attempt uuid.UUID) {
		command := loopID(0x1000 + len(out))
		h := Digest(sha256.Sum256(mustEncode(s)))
		r := RuntimePaidReceipt{CommandID: command, SubmissionSHA256: h, Submission: s, Outcome: outcome, Version: version, AttemptID: attempt}
		appendCommit([]Draft{draft(work, command, turn, TypeCommand, Actor(s.Actor), Command{command, h}), draft(work, command, turn, typ, ActorCore, p), draft(work, command, turn, TypeRuntimePaidReceipt, Actor(s.Actor), r)})
	}
	endNative := func(o Origin, turnID, grant uuid.UUID) (uuid.UUID, int64) {
		command := loopID(0x1000 + len(out))
		s := ScopedSubmission{Schema: 2, Actor: "core", Origin: o, Operation: "turn.end", Target: Resource{Kind: "context", Binding: Binding{o.AgentID, o.ContextID}, Scope: "context", Slots: []string{}}, GrantID: grant, Expected: ScopedExpected{Version: 1}, Arguments: WorkEndArguments{turnID, "released"}}
		h := Digest(sha256.Sum256(mustEncode(s)))
		p := RuntimeWorkEnded{WorkID: turnID, ExpectedVersion: 1, Version: 2, Outcome: "released"}
		r := RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: h, Submission: s, Outcome: "recorded", Version: 2, WorkID: turnID}
		appendCommit([]Draft{draft(o, command, turnID, TypeCommand, ActorCore, Command{command, h}), draft(o, command, turnID, TypeRuntimeWorkEnded, ActorCore, p), draft(o, command, turnID, TypeRuntimeScopedReceipt, ActorCore, r)})
		return command, sequence - 1
	}
	// Close the setup's unused general-context turn; it is not a queued loop.
	endNative(general, paidID(0x420), paidID(0x412))
	input, policy, render := object(`{"schema":1,"text":"Synthetic loop input."}`), object(`{"schema":1,"max_model_calls":2,"max_tool_calls":2}`), object(`{"schema":1,"text":"Synthetic rendered context."}`)
	bind := LoopSubmission{6, "owner", work, turn, "loop.bind", 0, LoopBindArguments{turn, admissionCommand, input, policy, render}}
	type entry struct {
		Schema    int             `json:"schema"`
		Kind      string          `json:"kind"`
		Origin    Origin          `json:"origin"`
		TurnID    uuid.UUID       `json:"turn_id"`
		Ordinal   int             `json:"ordinal"`
		Provider  string          `json:"provider,omitempty"`
		Model     string          `json:"model,omitempty"`
		AttemptID string          `json:"attempt_id,omitempty"`
		ToolID    string          `json:"tool_id,omitempty"`
		Content   json.RawMessage `json:"content"`
	}
	var entries []entry
	encoded := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return b
	}
	journal := func(kind string, attemptID, toolID uuid.UUID, content string) {
		e := entry{Schema: 1, Kind: kind, Origin: work, TurnID: turn, Ordinal: len(entries) + 1, Content: json.RawMessage(content)}
		if attemptID != (uuid.UUID{}) {
			e.AttemptID = attemptID.String()
			e.Provider, e.Model = reserve.Request.Provider, reserve.Request.Model
		}
		if toolID != (uuid.UUID{}) {
			e.ToolID = toolID.String()
		}
		entries = append(entries, e)
	}
	journal("turn_input", uuid.UUID{}, uuid.UUID{}, fmt.Sprintf(`{"input_object":%s}`, encoded(input)))
	boundSequence := addLoop(bind, RuntimeTurnBound{turn, admissionSequence, input, policy, render})
	modelCall := func(attempt uuid.UUID, text string) ObjectRef {
		p := reserve
		p.AttemptID = attempt
		p.AdmissionUTCNS = out[len(out)-1].header.EventTimeNS
		p.AdmissionPeriod = time.Unix(0, p.AdmissionUTCNS).UTC().Format("2006-01-02")
		addPaid(PaidSubmission{4, "agent", work, "paid.admit", p.GrantID, paidAdmitArguments(p)}, 1, "reserved", TypeRuntimePaidReserved, p, attempt)
		intent := RuntimePaidDispatchIntent{attempt, 1, 2, p.AuthorityVersion, p.Quote.RequestSHA256, p.Quote.QuoteSHA256, p.Request.Credential, out[len(out)-1].header.EventTimeNS}
		addPaid(PaidSubmission{4, "core", work, "paid.dispatch", p.GrantID, PaidAttemptArguments{attempt, 1}}, 2, "sent", TypeRuntimePaidDispatchIntent, intent, attempt)
		output := object(text)
		observation := object(fmt.Sprintf(`{"kind":"paid_observation","synthetic":true,"attempt_id":%q}`, attempt))
		result := paidResultFixture("usage")
		result.AttemptID, result.EvidenceObject, result.EvidenceSHA256, result.MessageObjects = attempt, &observation, observation.SHA256, []ObjectRef{output}
		*result.OutputStarted = true
		addPaid(PaidSubmission{4, "core", work, "paid.observe", p.GrantID, PaidObserveArguments{attempt, 2, "usage", observation.SHA256, observation, []ObjectRef{output}}}, 3, "settled", TypeRuntimePaidResult, result, attempt)
		replay := object(`[]`)
		journal("model_response", attempt, uuid.UUID{}, fmt.Sprintf(`{"output_objects":[%s],"replay_object":%s}`, encoded(output), encoded(replay)))
		return output
	}
	attempt := loopID(0x500)
	modelCall(attempt, `{"kind":"paid_output","synthetic":true,"calls":["status","invalid"]}`)
	for i, operation := range []string{"turn.inspect", "invalid_argument"} {
		target := &Resource{Kind: "context", Binding: Binding{work.AgentID, work.ContextID}, Scope: "context", Slots: []string{}}
		raw := fmt.Sprintf(`{"schema":1,"tool_name":"core_status_v1","operation_schema":1,"operation":"turn.inspect","target":%s,"arguments":{"schema":1,"subject":"turn","id":%q}}`, encoded(target), turn)
		if i != 0 {
			raw = fmt.Sprintf(`{"schema":1,"tool_name":"core_status_v1","raw_arguments_sha256":%q}`, scopedHash("synthetic invalid arguments"))
		}
		request := object(raw)
		p := RuntimeToolStarted{ToolID: loopID(0x600 + i), ParentTurnID: turn, AttemptID: attempt, ProviderCallID: fmt.Sprintf("synthetic-call-%d", i), ToolName: "core_status_v1", ToolSchema: 1, RequestObject: request, RequestSHA256: request.SHA256, Operation: operation}
		outcome := "invalid_argument"
		if i == 0 {
			p.GrantID = paidID(0x413)
			p.Target = target
			outcome = "ok"
		}
		journal("tool_request", attempt, p.ToolID, fmt.Sprintf(`{"request_object":%s}`, encoded(request)))
		started := addLoop(LoopSubmission{6, "agent", work, turn, "tool.begin", 0, LoopToolBeginArguments(p)}, p)
		resultBytes := fmt.Sprintf(`{"schema":1,"tool_id":%q,"status":"ok","data":{"work_id":%q,"outcome":"admitted"}}`, p.ToolID, turn)
		if i != 0 {
			resultBytes = fmt.Sprintf(`{"schema":1,"tool_id":%q,"status":"error","error":{"code":"invalid_argument","field":"subject","allowed":"context, turn or task"}}`, p.ToolID)
		}
		resultObject := object(resultBytes)
		journal("tool_result", attempt, p.ToolID, fmt.Sprintf(`{"result_object":%s}`, encoded(resultObject)))
		result := RuntimeToolResult{ToolID: p.ToolID, StartedSequence: started, Outcome: outcome, ResultObject: resultObject}
		addLoop(LoopSubmission{6, "core", work, turn, "tool.result", 1, LoopToolResultArguments(result)}, result)
	}
	answer := modelCall(loopID(0x501), "Synthetic final answer.")
	final := object(fmt.Sprintf(`{"schema":1,"kind":"final_message","origin":%s,"turn_id":%q,"provider":%q,"model":%q,"text_objects":[%s]}`, encoded(work), turn, reserve.Request.Provider, reserve.Request.Model, encoded(answer)))
	journal("turn_end", uuid.UUID{}, uuid.UUID{}, fmt.Sprintf(`{"outcome":"finished","reason":"natural_stop","final_message_object":%s}`, encoded(final)))
	endingCommand, endingSequence := endNative(work, turn, paidID(0x413))
	manifest := object(fmt.Sprintf(`{"schema":1,"entries":%s}`, encoded(entries)))
	end := LoopEndArguments{turn, boundSequence, "finished", "natural_stop", 2, 2, &final, endingCommand}
	addLoop(LoopSubmission{6, "core", work, turn, "loop.end", 1, end}, PrivateTurnRevision{"context", 1, 2, []PrivateChange{{Slot: "transcript", Mode: "append", SHA256: manifest.SHA256, Bytes: manifest.Bytes}}}, RuntimeTurnResult{turn, boundSequence, "finished", "natural_stop", 2, 2, &final, endingSequence})
	return out, files
}

func loopPayloadType(p payload) string {
	switch p.(type) {
	case PrivateTurnRevision:
		return TypePrivateTurnRevision
	case RuntimeTurnBound:
		return TypeRuntimeTurnBound
	case RuntimeToolStarted:
		return TypeRuntimeToolStarted
	case RuntimeToolResult:
		return TypeRuntimeToolResult
	case RuntimeTurnResult:
		return TypeRuntimeTurnResult
	case RuntimeLoopReceipt:
		return TypeRuntimeLoopReceipt
	default:
		panic("not a loop payload")
	}
}

func TestGoldenF3_15(t *testing.T) {
	t.Parallel()
	g := mustLoop(t)
	_, files := loopInputs()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(loopDir)
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
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(name)]; ok {
			return nil
		}
		if *update {
			return os.Remove(path)
		}
		return fmt.Errorf("unexpected fixture %s", path)
	}); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("CR in %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: loopTypes})
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]bool{}
	for _, c := range commits {
		for _, r := range c.Records {
			if !Defined().Contains(TypeVersion{r.Type, r.TypeVersion}) {
				t.Fatalf("unregistered %s", r.Type)
			}
		}
		if c.Records[len(c.Records)-1].Type != TypeRuntimeLoopReceipt {
			continue
		}
		if err := checkLoopGoldenGroup(c, commits, files); err != nil {
			t.Fatal(err)
		}
		r, _ := DecodeRuntimeLoopReceipt(c.Records[len(c.Records)-1].Payload)
		shapes[r.Submission.Operation] = true
	}
	if len(shapes) != 4 {
		t.Fatalf("missing loop group: %v", shapes)
	}
	t.Logf("commits=%d records=%d shapes=%s", len(commits), cursor, strings.Join(slices.Sorted(maps.Keys(shapes)), ","))
}
