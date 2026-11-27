package ledger

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"
)

func TestSetupSubmissionRefusals(t *testing.T) {
	t.Parallel()
	const valid = `{"schema":12,"actor":"owner","operation":"context.set","target":{"kind":"context","id":"019a1500-0000-7000-8000-000000000103","agent_id":"019a1500-0000-7000-8000-000000000100"},"expected_revision":0,"arguments":{"display_title":"General work","route_id":"019a1500-0000-7000-8000-000000000200","expected_route_version":1,"credential_scope_id":"019a1500-0000-7000-8000-000000000201","max_output_tokens":1024,"turn_policy":{"max_model_calls":4,"max_tool_calls":16,"timeout_ms":60000,"max_argument_bytes":8192,"max_result_bytes":8192,"max_discovery_entries":32,"max_search_entries":64,"max_search_matches":128,"max_live_bytes":65536}}}`
	for _, row := range []struct {
		name, from, to string
	}{
		{"missing turn policy member", `,"max_live_bytes":65536`, ""},
		{"context target without agent", `,"agent_id":"019a1500-0000-7000-8000-000000000100"`, ""},
		{"another operation member", `"display_title":`, `"settings_bytes":1,"display_title":`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeSetupSubmission([]byte(strings.Replace(valid, row.from, row.to, 1))); !errors.Is(err, ErrInvalid) {
				t.Fatalf("malformed setup submission accepted: %v", err)
			}
		})
	}
}

const setupDir = "testdata/f3/150/log"

var setupColony = setupID(1)
var setupTypes = Consumption{
	TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1},
	TypeRuntimeAgentRegistered: {1}, TypeRuntimeContextCreated: {1},
	TypePrivateRevision: {1}, TypeRuntimeCommandReceipt: {1},
	TypeRuntimeSetupApplied: {1}, TypeRuntimeSetupReceipt: {1},
}
var setupLog = sync.OnceValues(func() (*goldenLog, error) {
	return buildLog(setupDir, setupColony, setupTypes, setupInputs())
})

func setupID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1500-0000-7000-8000-%012x", n))
}

var setupSettings = []byte("{}\n")
var setupCheck = []byte("Setup check body fixture.\n")
var setupEnvironment = []byte("Setup environment body fixture.\n")

func setupBodySetPreimage() []byte {
	var out []byte
	for _, digest := range sortedDigests(Digest(sha256.Sum256(setupCheck)), Digest(sha256.Sum256(setupEnvironment))) {
		out = append(out, digest.String()...)
		out = append(out, '\n')
	}
	return out
}

func setupUpdatedCapture() []byte {
	return bytes.Replace(scopedCaptureFixture(), []byte(`"core.eol":"native"`), []byte(`"core.eol":"lf"`), 1)
}

func setupFixtureFiles() map[string][]byte {
	files := map[string][]byte{
		"objects/" + scopedHash(scopedInstructions).String(): []byte(scopedInstructions),
		"objects/" + scopedHash("").String():                 {},
		"body-set-preimage.txt":                              setupBodySetPreimage(),
	}
	for _, body := range [][]byte{scopedCaptureFixture(), setupUpdatedCapture(), setupSettings, setupCheck, setupEnvironment} {
		files["bodies/"+Digest(sha256.Sum256(body)).String()] = body
	}
	return files
}

func setupInputs() []goldenCommit {
	var out []goldenCommit
	appendCommit := func(drafts []Draft) {
		i := len(out) + 1
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: setupColony, CommitID: setupID(0x2000 + i), EventTimeNS: 1791000800000000000 + int64(i)*1000000000}, drafts: drafts})
	}
	add := func(operation string, target SetupTarget, expected int64, arguments any, refs ...Digest) {
		command := setupID(0x1000 + len(out) + 1)
		s := SetupSubmission{12, "owner", operation, target, expected, arguments}
		digest := Digest(sha256.Sum256(mustEncode(s)))
		draft := func(typ string, p payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: ActorOwner, OperationID: command, Payload: mustEncode(p)}
		}
		effect := draft(TypeRuntimeSetupApplied, RuntimeSetupApplied{s})
		effect.BodyRefs = refs
		drafts := []Draft{draft(TypeCommand, Command{command, digest}), effect}
		if len(refs) > 0 {
			pin := draft(TypeBodyPin, BodyPin{PinID: command})
			pin.BodyRefs = refs
			drafts = append(drafts, pin)
		}
		drafts = append(drafts, draft(TypeRuntimeSetupReceipt, RuntimeSetupReceipt{command, digest, "applied", expected + 1}))
		appendCommit(drafts)
	}
	colony := SetupTarget{Kind: "colony", ID: setupColony}
	add("colony.create", colony, 0, SetupColonyCreateArguments{})

	// Reuse earlier-family native groups verbatim, reframed under this colony.
	native := scopedInputs()
	appendCommit(native[4].drafts)
	workspace := SetupTarget{Kind: "workspace", ID: scopedID(0x400)}
	add("workspace.register", workspace, 0, SetupWorkspaceArguments{Digest(sha256.Sum256(scopedCaptureFixture()))})
	capture := Digest(sha256.Sum256(setupUpdatedCapture()))
	appendCommit([]Draft{
		{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{capture}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: workspace.ID, Revision: 2, CaptureInputsSHA256: capture, PinID: setupID(0x400)})},
		{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{capture}, Payload: mustEncode(BodyPin{PinID: setupID(0x400)})},
		{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodyUnpin{PinID: scopedID(0x402)})},
	})
	add("workspace.inputs", workspace, 1, SetupWorkspaceArguments{capture})

	appendCommit(native[0].drafts)
	general := SetupTarget{Kind: "context", ID: fid("d1"), AgentID: fid("a1")}
	settings := SetupContextArguments{
		DisplayTitle: "General work", RouteID: setupID(0x200), ExpectedRouteVersion: 1,
		CredentialScopeID: setupID(0x201), MaxOutputTokens: 1024,
		TurnPolicy: SetupTurnPolicy{4, 16, 60000, 8192, 8192, 32, 64, 128, 65536},
	}
	add("context.set", general, 0, settings)
	appendCommit(native[1].drafts)
	work := SetupTarget{Kind: "context", ID: fid("d2"), AgentID: fid("a1")}
	settings.DisplayTitle = "Setup work"
	settings.TaskBinding = &TaskBinding{setupID(0x300), setupID(0x301), 1}
	add("context.set", work, 0, settings)
	settings.DisplayTitle = "Reviewed setup work"
	settings.MaxOutputTokens = 2048
	add("context.set", work, 1, settings)

	settingsHash := Digest(sha256.Sum256(setupSettings))
	add("startup.set", colony, 0, SetupStartupArguments{settingsHash, int64(len(setupSettings))}, settingsHash)
	add("checks.publish", colony, 0, SetupChecksArguments{Digest(sha256.Sum256(setupBodySetPreimage())), 2},
		sortedDigests(Digest(sha256.Sum256(setupCheck)), Digest(sha256.Sum256(setupEnvironment)))...)
	return out
}

func TestGoldenF3_150(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, setupLog)
	files := setupFixtureFiles()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(setupDir)
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
		if (strings.HasPrefix(name, "bodies/") || strings.HasPrefix(name, "objects/")) && filepath.Base(name) != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("incorrect content address %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: setupTypes})
	if err != nil || len(commits) != 12 || cursor != 42 {
		t.Fatalf("commits=%d cursor=%d: %v; want 12/42", len(commits), cursor, err)
	}
	var operations []string
	revisions := map[string]int64{}
	for _, commit := range commits {
		for _, r := range commit.Records {
			var p payload
			var err error
			switch r.Type {
			case TypeRuntimeSetupApplied:
				p, err = DecodeRuntimeSetupApplied(r.Payload)
			case TypeRuntimeSetupReceipt:
				p, err = DecodeRuntimeSetupReceipt(r.Payload)
			case TypeCommand, TypeBodyPin, TypeBodyUnpin:
				p, err = decodeF0(r.Type, r.Payload)
			case TypeWorkspaceRegistered, TypeCaptureInputsPinned:
				p, err = decodeF2_05(r.Type, r.Payload)
			default:
				p, err = decodeF3_10(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d decode: %v", r.Seq, err)
			}
			if js, err := p.Encode(); err != nil || !bytes.Equal(js, r.Payload) {
				t.Fatalf("seq %d round-trip: %v", r.Seq, err)
			}
		}
		if commit.Records[1].Type != TypeRuntimeSetupApplied {
			continue
		}
		command, err := DecodeCommand(commit.Records[0].Payload)
		if err != nil || commit.Records[0].Type != TypeCommand {
			t.Fatalf("setup command: %v", err)
		}
		applied, err := DecodeRuntimeSetupApplied(commit.Records[1].Payload)
		if err != nil {
			t.Fatal(err)
		}
		s := applied.Submission
		operations = append(operations, s.Operation)
		js := mustEncode(s)
		decoded, err := DecodeSetupSubmission(js)
		if err != nil || !bytes.Equal(mustEncode(decoded), js) {
			t.Fatalf("submission round-trip: %v", err)
		}
		last := commit.Records[len(commit.Records)-1]
		receipt, err := DecodeRuntimeSetupReceipt(last.Payload)
		if err != nil || last.Type != TypeRuntimeSetupReceipt || receipt.CommandID != command.CommandID ||
			receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.SubmissionSHA256 != Digest(sha256.Sum256(js)) ||
			receipt.Revision != s.ExpectedRevision+1 || receipt.Outcome != "applied" {
			t.Fatalf("setup receipt at %d: %+v %v", last.Seq, receipt, err)
		}
		class := s.Operation
		if strings.HasPrefix(class, "workspace.") {
			class = "workspace"
		}
		key := class + "/" + s.Target.ID.String()
		if s.ExpectedRevision != revisions[key] {
			t.Fatalf("revision discontinuity for %s", key)
		}
		revisions[key] = receipt.Revision
		var refs []Digest
		switch a := s.Arguments.(type) {
		case SetupStartupArguments:
			body := files["bodies/"+a.SettingsSHA256.String()]
			if int64(len(body)) != a.SettingsBytes || Digest(sha256.Sum256(body)) != a.SettingsSHA256 {
				t.Fatal("startup body identity differs")
			}
			refs = []Digest{a.SettingsSHA256}
		case SetupChecksArguments:
			refs = commit.Records[1].BodyRefs
			var preimage []byte
			for i, digest := range refs {
				if i > 0 && bytes.Compare(refs[i-1][:], digest[:]) >= 0 || Digest(sha256.Sum256(files["bodies/"+digest.String()])) != digest {
					t.Fatal("published bodies are not exact and strictly ascending")
				}
				preimage = append(preimage, digest.String()...)
				preimage = append(preimage, '\n')
			}
			if int64(len(refs)) != a.BodyCount || !bytes.Equal(preimage, files["body-set-preimage.txt"]) || Digest(sha256.Sum256(preimage)) != a.BodySetSHA256 {
				t.Fatal("published body-set preimage differs")
			}
		}
		wantCount := 3
		if len(refs) > 0 {
			wantCount = 4
		}
		if len(commit.Records) != wantCount || wantCount == 4 && commit.Records[2].Type != TypeBodyPin {
			t.Fatalf("setup group shape at %d", commit.First)
		}
		for i, r := range commit.Records {
			if r.Actor != ActorOwner || r.OperationID != command.CommandID || r.AgentID != (uuid.UUID{}) || r.ExecutionSessionID != (uuid.UUID{}) || r.ContextID != (uuid.UUID{}) || r.TurnID != (uuid.UUID{}) || r.MeasurementPath != "" {
				t.Fatalf("setup envelope at %d", r.Seq)
			}
			wantRefs := refs
			if i == 0 || i == len(commit.Records)-1 {
				wantRefs = nil
			}
			if !slices.Equal(r.BodyRefs, wantRefs) {
				t.Fatalf("setup body references at %d", r.Seq)
			}
		}
	}
	wantOperations := []string{"colony.create", "workspace.register", "workspace.inputs", "context.set", "context.set", "context.set", "startup.set", "checks.publish"}
	if !slices.Equal(operations, wantOperations) {
		t.Fatalf("setup operations=%v, want %v", operations, wantOperations)
	}
	t.Logf("commits=12 records=42 setup_groups=8 Defined=%d", len(Defined().set))
}
