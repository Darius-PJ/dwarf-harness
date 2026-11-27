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
	"testing"
	"uuid"
)

const validationGoldenDir = "testdata/f4/17/log"

func validationID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1700-0000-7000-8000-%012x", n))
}

// All paths in this fixture are synthetic examples, never resolved on the host.
// The setup follows the real registration, activation and acceptance groups.
// The bound/native-ended turn is format evidence, not evidence of live behavior.
type validationGoldenSetup struct {
	inputs            []goldenCommit
	files             map[string][]byte
	sequence          uint64
	origin            ValidationOrigin
	workspace         uuid.UUID
	capture           Digest
	checkPolicy       Digest
	environmentPolicy Digest
	law               Digest
	policy            Digest
	activation        uint64
	observation       uint64
	turn              uuid.UUID
	acceptance        uint64
	registration      uint64
	specAdmission     uint64
	specGeneration    uint64
	manifest          Digest
	checks            WorkspaceChecks
	environment       EnvironmentPolicy
}

func (s *validationGoldenSetup) body(data []byte) Digest {
	d := Digest(sha256.Sum256(data))
	s.files["bodies/"+d.String()] = data
	return d
}

func (s *validationGoldenSetup) add(count int, build func(*goldenLog) []Draft) uint64 {
	first := s.sequence + 1
	s.inputs = append(s.inputs, goldenCommit{header: CommitHeader{ColonyID: validationID(1), CommitID: validationID(0x1000 + len(s.inputs)), EventTimeNS: 1793000800000000000 + int64(len(s.inputs))*1000000000}, build: build})
	s.sequence += uint64(count)
	return first
}

func (s *validationGoldenSetup) fixed(ds ...Draft) uint64 {
	return s.add(len(ds), func(*goldenLog) []Draft { return ds })
}

func validationSetup() *validationGoldenSetup {
	s := &validationGoldenSetup{files: map[string][]byte{}, workspace: validationID(2), origin: ValidationOrigin{AgentID: fid("a1"), ExecutionSessionID: fid("b1"), ContextID: fid("d2")}}
	prior, priorFiles := continuationInputs()
	var nativeEnding, loopEnding []Draft
	for _, c := range prior {
		include := false
		if s.turn == (uuid.UUID{}) {
			s.fixed(c.drafts...)
			s.inputs[len(s.inputs)-1].header.EventTimeNS = c.header.EventTimeNS
			include = true
			for _, d := range c.drafts {
				if d.Type == TypeRuntimeTurnBound {
					binding, err := DecodeRuntimeTurnBound(d.Payload)
					if err != nil {
						panic(err)
					}
					s.turn = binding.TurnID
				}
			}
		} else {
			for _, d := range c.drafts {
				if d.TurnID == s.turn && d.Type == TypeRuntimeWorkEnded {
					nativeEnding, include = slices.Clone(c.drafts), true
				}
				if d.TurnID == s.turn && d.Type == TypeRuntimeTurnResult {
					loopEnding, include = slices.Clone(c.drafts), true
				}
			}
		}
		if include {
			for _, d := range c.drafts {
				for path, data := range priorFiles {
					if bytes.Contains(d.Payload, []byte(filepath.Base(path))) {
						s.files[path] = data
					}
				}
			}
		}
	}
	// These are the exact immutable private objects the reused F3 registrations
	// name. The object directory follows Core's per-agent/context layout.
	for _, text := range []string{scopedInstructions, ""} {
		d := Digest(sha256.Sum256([]byte(text)))
		s.files["objects/agents/Ada-a1/core/objects/"+d.String()] = []byte(text)
		for _, id := range []uuid.UUID{fid("d1"), fid("d2")} {
			s.files["objects/agents/Ada-a1/contexts/"+id.String()+"/core/objects/"+d.String()] = []byte(text)
		}
	}
	s.checks, s.environment, _ = validationConfigFixture()
	s.checks.WorkspaceID, s.environment.WorkspaceID = s.workspace, s.workspace
	goMod := []byte("module example.test/unit\n\ngo 1.27\n")
	goTest := []byte("package unit\n\nimport \"testing\"\n\nfunc TestUnit(t *testing.T) {}\n")
	mod := ValidationFile{Path: "go.mod", Mode: "100644", Bytes: int64(len(goMod)), SHA256: s.body(goMod)}
	testFile := ValidationFile{Path: "unit_test.go", Mode: "100644", Bytes: int64(len(goTest)), SHA256: s.body(goTest)}
	s.checks.Checks[0].DefiningFiles = []ValidationFile{mod, testFile}
	s.environment.Environments[0].LockFiles = []ValidationFile{mod}
	s.checks.Hooks = []ValidationHook{{HookID: "on_write", Event: "observation.committed"}, {HookID: "on_write_secondary", Event: "observation.committed"}}
	s.checks.Checks[0].Canary.TemplateSHA256 = s.body([]byte("go-fail-v1 testing-error-v1 fixture template\n"))
	toolBytes := []byte("synthetic executable bytes\n")
	toolDigest := s.body(toolBytes)
	s.environment.Environments[0].ExecutableSHA256 = toolDigest
	s.environment.Environments[0].ToolchainManifestSHA256 = s.body(mustEncode(FileManifest{FileManifestFormat, 1, []ValidationFile{{"bin/go.exe", "100755", int64(len(toolBytes)), toolDigest}}}))
	s.environment.Environments[0].DependencyManifestSHA256 = s.body(mustEncode(FileManifest{FileManifestFormat, 1, []ValidationFile{}}))
	s.checkPolicy = s.body(mustEncode(s.checks))
	s.environmentPolicy = s.body(mustEncode(s.environment))
	s.capture = s.body(scopedCaptureFixture())
	s.law = s.body([]byte("synthetic immutable field law fixture\n"))
	s.policy = s.body([]byte(`{"format":"dwarf-harness/validation-policy","schema_version":1,"flake_rate_prior":{"numerator":1,"denominator":20},"failure_repair_confidence":{"numerator":99,"denominator":100},"quarantine_threshold":{"numerator":1,"denominator":2}}`))
	s.registration = s.fixed(
		Draft{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{s.workspace, "C:/fixture/validation-repo/.git", "sha1"})},
		Draft{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{s.capture}, Payload: mustEncode(CaptureInputsPinned{s.workspace, 1, s.capture, validationID(3)})},
		Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{s.capture}, Payload: mustEncode(BodyPin{validationID(3)})},
	)
	approval := PolicyActivationRequested{RequestID: validationID(4), OwnerApprovalID: validationID(5), FieldLawSHA256: s.law, ValidationPolicyHash: s.policy,
		WorkspacePolicies: []WorkspacePolicy{{s.workspace, s.checkPolicy, s.environmentPolicy}}, AffectedWorkspaceIDs: []uuid.UUID{s.workspace}, Schedule: ScheduleImmediate,
		AdmissionUTCNS: 1791000803000000000, NotBeforeUTCNS: 1791000803000000000, PinID: validationID(6)}
	refs := sortedDigests(s.law, s.policy, s.checkPolicy, s.environmentPolicy)
	request := s.fixed(
		Draft{Type: TypePolicyActivationRequested, TypeVersion: 1, Actor: ActorOwner, BodyRefs: refs, Payload: mustEncode(approval)},
		Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: refs, Payload: mustEncode(BodyPin{approval.PinID})},
	)
	s.add(1, func(g *goldenLog) []Draft {
		return []Draft{{Type: TypePolicyActivationHoldChanged, TypeVersion: 1, Actor: ActorCore, Payload: mustEncode(PolicyActivationHoldChanged{RequestID: approval.RequestID, ActivationRequestRecordHash: g.hash(request), Hold: HoldDue, ObservedUTCNS: 1791000804000000000})}}
	})
	s.activation = s.add(1, func(g *goldenLog) []Draft {
		return []Draft{{Type: TypePolicyActivated, TypeVersion: 1, Actor: ActorCore, BodyRefs: refs, Payload: mustEncode(PolicyActivated{ActivationID: approval.RequestID, Epoch: 1, FieldLawSHA256: s.law, ValidationPolicyHash: s.policy, WorkspacePolicies: approval.WorkspacePolicies, OwnerApprovalID: approval.OwnerApprovalID, ActivationRequestRecordHash: g.hash(request), NotBeforeUTCNS: approval.NotBeforeUTCNS, EffectiveUTCNS: 1791000805000000000})}}
	})
	b0, t0, d1, t1 := strings.Repeat("a1", 20), strings.Repeat("b2", 20), strings.Repeat("c3", 20), strings.Repeat("d4", 20)
	s.fixed(Draft{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(ScratchCreated{ScratchID: validationID(7), WorkspaceID: s.workspace, ContextID: s.origin.ContextID, Dir: "17171717", Branch: "refs/heads/dwarf-harness/17171717", BaseCommit: b0, AnchorTree: t0})})
	manifest := s.body([]byte("synthetic capture manifest\n"))
	s.manifest = manifest
	s.fixed(
		Draft{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(PromotionAdmitted{RequestID: validationID(8), WorkspaceID: s.workspace, ScratchID: validationID(7), ContextID: s.origin.ContextID, LineageID: validationID(9), Checkpoint: 1, ScratchBase: b0, BaselineTree: t0, DeltaBaseTree: t0, CaptureID: validationID(10), SealedTree: t1, ManifestSHA256: manifest, InputsRevision: 1, CaptureInputsSHA256: s.capture, PinID: validationID(11)})},
		Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(BodyPin{validationID(11)})},
	)
	s.fixed(Draft{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(GenerationDerived{GenerationID: validationID(12), RequestID: validationID(8), PredecessorCommit: b0, PredecessorTree: t0, InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d1, DerivedTree: t1})})
	s.specAdmission = s.fixed(
		Draft{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(PromotionAdmitted{RequestID: validationID(30), WorkspaceID: s.workspace, ScratchID: validationID(7), ContextID: s.origin.ContextID, LineageID: validationID(9), Checkpoint: 2, ParentRequestID: validationID(8), ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: validationID(31), SealedTree: strings.Repeat("17", 20), ManifestSHA256: manifest, InputsRevision: 1, CaptureInputsSHA256: s.capture, PinID: validationID(32)})},
		Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(BodyPin{validationID(32)})},
	)
	s.specGeneration = s.fixed(Draft{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(GenerationDerived{GenerationID: validationID(33), RequestID: validationID(30), PredecessorRequestID: validationID(8), PredecessorGenerationID: validationID(12), PredecessorCommit: d1, PredecessorTree: t1, InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: strings.Repeat("28", 20), DerivedTree: strings.Repeat("17", 20)})})
	s.observation = s.fixed(
		Draft{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(ScratchChanged{ObservationID: validationID(14), WorkspaceID: s.workspace, ScratchID: validationID(7), ContextID: s.origin.ContextID, PreviousCommit: b0, PreviousTree: t0, ScratchCommit: strings.Repeat("e5", 20), ScratchTree: t1, WrittenPaths: []string{"unit.go"}, ChangedPaths: []string{"unit.go"}, InputsRevision: 1, CaptureInputsSHA256: s.capture, ScratchRules: "scratch-rules/1", ManifestSHA256: manifest, PinID: validationID(15)})},
		Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, BodyRefs: []Digest{manifest}, Payload: mustEncode(BodyPin{validationID(15)})},
	)
	// Without an F3-16 owner, cancellation is authenticated to the owner,
	// not Core's continuation-only cancellation path.
	nativeReceipt, err := DecodeRuntimeScopedReceipt(nativeEnding[len(nativeEnding)-1].Payload)
	if err != nil {
		panic(err)
	}
	nativeReceipt.Submission.Actor = "owner"
	nativeReceipt.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(nativeReceipt.Submission)))
	nativeEnding[0].Actor, nativeEnding[len(nativeEnding)-1].Actor = ActorOwner, ActorOwner
	nativeEnding[0].Payload = mustEncode(Command{nativeReceipt.CommandID, nativeReceipt.SubmissionSHA256})
	nativeEnding[len(nativeEnding)-1].Payload = mustEncode(nativeReceipt)
	endingSequence := s.fixed(nativeEnding...) + 1
	for i, d := range loopEnding {
		switch d.Type {
		case TypeRuntimeTurnResult:
			p, err := DecodeRuntimeTurnResult(d.Payload)
			if err != nil {
				panic(err)
			}
			p.EndingSequence = int64(endingSequence)
			loopEnding[i].Payload = mustEncode(p)
		case TypeRuntimeLoopReceipt:
			p, err := DecodeRuntimeLoopReceipt(d.Payload)
			if err != nil {
				panic(err)
			}
			p.EffectSequence = int64(s.sequence + 3)
			loopEnding[i].Payload = mustEncode(p)
		}
	}
	s.fixed(loopEnding...)
	return s
}

func (s *validationGoldenSetup) accept() {
	b0, t0, d1, t1 := strings.Repeat("a1", 20), strings.Repeat("b2", 20), strings.Repeat("c3", 20), strings.Repeat("d4", 20)
	s.add(1, func(g *goldenLog) []Draft {
		return []Draft{{Type: TypeFinalizationIntended, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(FinalizationIntended{IntentID: validationID(13), RequestID: validationID(8), GenerationID: validationID(12), PredecessorCommit: b0, PredecessorTree: t0, TargetCommit: d1, TargetTree: t1, FinalizationRules: "finalization-rules/1", ActivationRecordHash: g.hash(s.activation)})}}
	})
	s.acceptance = s.fixed(
		Draft{Type: TypePromotionAccepted, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(PromotionAccepted{IntentID: validationID(13), RequestID: validationID(8), GenerationID: validationID(12), PredecessorCommit: b0, PredecessorTree: t0, AcceptedCommit: d1, AcceptedTree: t1, ChangedPaths: []string{"unit.go"}})},
		Draft{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: s.origin.ContextID, Payload: mustEncode(BodyUnpin{validationID(11)})},
	)
}

func validationFixtureDigest(text string) Digest { return Digest(sha256.Sum256([]byte(text))) }

func validationFixtureRefs(p payload) []Digest {
	refs, err := ValidationBodyRefs(p, nil, nil)
	if err != nil {
		panic(err)
	}
	return refs
}

func validationFixtureDraft(typ string, p payload) Draft {
	return Draft{Type: typ, TypeVersion: 1, Actor: ActorCore, BodyRefs: validationFixtureRefs(p), Payload: mustEncode(p)}
}

func validationFixturePin(id uuid.UUID, refs []Digest) Draft {
	return Draft{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, BodyRefs: refs, Payload: mustEncode(BodyPin{id})}
}

func (s *validationGoldenSetup) group(count int, build func(*goldenLog) (ValidationSubmission, []Draft)) uint64 {
	first := s.sequence + 1
	command := validationID(0x2000 + len(s.inputs))
	return s.add(count+2, func(g *goldenLog) []Draft {
		sub, effects := build(g)
		if len(effects) != count {
			panic("validation fixture group count")
		}
		digest := Digest(sha256.Sum256(mustEncode(sub)))
		v, d, version := validationSubmissionTarget(sub)
		receipt := ValidationReceipt{command, digest, sub, v, d, sub.Operation, version, int64(first + 1)}
		drafts := append([]Draft{{Type: TypeCommand, TypeVersion: 1, Actor: Actor(sub.Actor), Payload: mustEncode(Command{command, digest})}}, effects...)
		drafts = append(drafts, Draft{Type: TypeValidationReceipt, TypeVersion: 1, Actor: Actor(sub.Actor), Payload: mustEncode(receipt)})
		for i := range drafts {
			drafts[i].AgentID, drafts[i].ExecutionSessionID, drafts[i].ContextID = sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID
			drafts[i].OperationID = command
		}
		return drafts
	})
}

func (s *validationGoldenSetup) submission(operation, actor string, version int64, args any) ValidationSubmission {
	return ValidationSubmission{10, actor, Origin{s.origin.AgentID, s.origin.ExecutionSessionID, s.origin.ContextID}, "validation." + operation, version, args}
}

func (s *validationGoldenSetup) subject(g *goldenLog, kind string) ValidationSubject {
	p := ValidationSubject{Kind: "canonical", WorkspaceID: s.workspace, ObjectFormat: "sha1", SourceSequence: int64(s.registration), SourceRecordHash: g.hash(s.registration), Commit: strings.Repeat("a1", 20), Tree: strings.Repeat("b2", 20), CaptureInputsRevision: 1, CaptureInputsSHA256: s.capture}
	switch kind {
	case "after":
		p.SourceSequence, p.SourceRecordHash = int64(s.acceptance), g.hash(s.acceptance)
		p.Commit, p.Tree = strings.Repeat("c3", 20), strings.Repeat("d4", 20)
	case "observation":
		p.Kind, p.SourceSequence, p.SourceRecordHash = "observation", int64(s.observation), g.hash(s.observation)
		p.Commit, p.Tree, p.SourceManifestSHA256 = strings.Repeat("e5", 20), strings.Repeat("d4", 20), s.manifest
	case "generation":
		p.Kind, p.SourceSequence, p.SourceRecordHash = "generation", int64(s.specAdmission), g.hash(s.specAdmission)
		p.Commit, p.Tree, p.SourceManifestSHA256 = strings.Repeat("28", 20), strings.Repeat("17", 20), s.manifest
		p.RequestID, p.CaptureID = validationID(30), validationID(31)
		p.SubmissionTree, p.SubmissionBase = p.Tree, strings.Repeat("d4", 20)
		p.GenerationID, p.GenerationSequence, p.GenerationHash = validationID(33), int64(s.specGeneration), g.hash(s.specGeneration)
		p.PredecessorRequestID, p.PredecessorGenerationID = validationID(8), validationID(12)
		p.PredecessorCommit, p.PredecessorTree, p.IntegrationRules = strings.Repeat("c3", 20), strings.Repeat("d4", 20), "integration-rules/1"
	}
	return p
}

type validationGoldenBatch struct {
	request  ValidationRequested
	attempts []ValidationAttemptQueued
	started  map[int]ValidationAttemptStarted
	version  int64
	pin      uuid.UUID
	evidence []uuid.UUID
}

type validationGoldenPlan struct {
	kind       string
	executions int64
}
type validationGoldenObservation struct {
	attempt uuid.UUID
	outcome string
}

func validationInputs() (*goldenLog, map[string][]byte, error) {
	s := validationSetup()
	check, env := s.checks.Checks[0], s.environment.Environments[0]
	preimage := func(name string, data []byte) Digest {
		s.files["preimages/"+name+".json"] = data
		return Digest(sha256.Sum256(data))
	}
	checkBytes, err := check.CheckPreimage()
	if err != nil {
		return nil, nil, err
	}
	envBytes, err := env.EnvironmentPreimage()
	if err != nil {
		return nil, nil, err
	}
	inventoryBytes, err := check.InventoryPreimage()
	if err != nil {
		return nil, nil, err
	}
	definition := ValidationCheckDefined{validationID(50), s.workspace, check.CheckID, preimage("check", checkBytes), s.checkPolicy, s.environmentPolicy, preimage("environment", envBytes), preimage("inventory", inventoryBytes), check.DefiningFiles, ValidationLineage{OriginKind: "owner_policy"}, validationID(51)}
	s.group(2, func(*goldenLog) (ValidationSubmission, []Draft) {
		refs, err := ValidationBodyRefs(definition, &check, &env)
		if err != nil {
			panic(err)
		}
		d := validationFixtureDraft(TypeValidationCheckDefined, definition)
		d.BodyRefs = refs
		return s.submission("define", "core", 0, ValidationDefineArguments(definition)), []Draft{d, validationFixturePin(definition.PinID, refs)}
	})
	required := ValidationRequiredCheck{check.CheckID, definition.DefinitionID, definition.CheckSHA256, definition.EnvironmentSHA256, definition.InventorySHA256}
	incident := validationID(500)
	incidentSource := uint64(0)
	history := map[Digest][]validationGoldenObservation{}
	evidenceNumber := 0
	enqueue := func(number int, trigger string, plans []validationGoldenPlan, incidentVersion, firstIndex int64, runner string) *validationGoldenBatch {
		b := &validationGoldenBatch{version: 1, pin: validationID(1000 + number), started: map[int]ValidationAttemptStarted{}}
		count := 0
		for _, plan := range plans {
			count += int(plan.executions)
		}
		s.group(count+2, func(g *goldenLog) (ValidationSubmission, []Draft) {
			r := ValidationRequested{ValidationID: validationID(100 + number), Origin: s.origin, Trigger: ValidationTrigger{Kind: trigger}, Subject: s.subject(g, plans[0].kind), ActivationRecordHash: g.hash(s.activation), Epoch: 1, FieldLawSHA256: s.law, ValidationPolicyHash: s.policy, CheckPolicyHash: s.checkPolicy, EnvironmentPolicyHash: s.environmentPolicy, RequiredChecks: []ValidationRequiredCheck{required}, CapacityProfileSHA256: validationFixtureDigest("validation capacity profile")}
			switch trigger {
			case "hook":
				r.Origin.TurnID = s.turn
				r.Trigger.SourceSequence, r.Trigger.SourceRecordHash, r.Trigger.HookID = int64(s.observation), g.hash(s.observation), "on_write"
			case "promotion":
				r.Trigger.SourceSequence, r.Trigger.SourceRecordHash = int64(s.specAdmission), g.hash(s.specAdmission)
			case "reproduction":
				r.Trigger.SourceSequence, r.Trigger.SourceRecordHash = int64(incidentSource), g.hash(incidentSource)
				r.IncidentID, r.IncidentVersion = incident, incidentVersion
				for _, plan := range plans {
					r.ReproductionPlan = append(r.ReproductionPlan, ValidationReproductionPlan{s.subject(g, plan.kind), definition.DefinitionID, plan.executions})
				}
			}
			auth := r.AuthorizationDocument()
			r.Authorization = ValidationAuthorization{auth.Principal, s.body(mustEncode(auth))}
			s.files["authorization/"+auth.Principal+".json"] = mustEncode(auth)
			b.request = r
			drafts := []Draft{validationFixtureDraft(TypeValidationRequested, r)}
			index := 0
			for _, plan := range plans {
				for range plan.executions {
					q := ValidationAttemptQueued{ValidationID: r.ValidationID, AttemptID: validationID(2000 + number*10 + index), RunID: validationID(3000 + number*10 + index), Ordinal: int64(index + 1), Subject: s.subject(g, plan.kind), DefinitionID: definition.DefinitionID, CheckSHA256: definition.CheckSHA256, EnvironmentSHA256: definition.EnvironmentSHA256, InventorySHA256: definition.InventorySHA256, RunnerSHA256: validationFixtureDigest(runner), AdapterSHA256: validationFixtureDigest("go-test-json/1 adapter")}
					binding := ValidationBinding{1, validationID(1), r.Origin, r.Trigger, q.Subject, r.ActivationRecordHash, r.Epoch, r.FieldLawSHA256, r.ValidationPolicyHash, r.CheckPolicyHash, r.EnvironmentPolicyHash, q.CheckSHA256, q.EnvironmentSHA256, q.InventorySHA256, q.RunnerSHA256, q.AdapterSHA256}
					data := mustEncode(binding)
					q.BindingSHA256 = Digest(sha256.Sum256(data))
					if number == 1 {
						preimage("binding", data)
					}
					if trigger == "reproduction" {
						q.IncidentID, q.IncidentExecutionIndex = incident, firstIndex+int64(index)
					}
					b.attempts = append(b.attempts, q)
					drafts = append(drafts, validationFixtureDraft(TypeValidationAttemptQueued, q))
					index++
				}
			}
			drafts = append(drafts, validationFixturePin(b.pin, []Digest{r.Authorization.SourcePermissionsSHA256}))
			operation, actor := "request", "owner"
			if trigger == "reproduction" {
				operation = "reproduce"
			}
			if trigger == "hook" || trigger == "promotion" {
				actor = "core"
			}
			return s.submission(operation, actor, 0, ValidationRequestArguments{r, b.attempts, b.pin}), drafts
		})
		return b
	}
	change := func(b *validationGoldenBatch, operation, reason string) {
		s.group(1, func(*goldenLog) (ValidationSubmission, []Draft) {
			state, actor := "running", "core"
			if operation == "cancel" {
				state, actor = "stopping", "owner"
			}
			sub := s.submission(operation, actor, b.version, ValidationChangeArguments{b.request.ValidationID, reason})
			p := ValidationRequestChanged{b.request.ValidationID, b.version, state, reason, []ValidationClosedAttempt{}}
			b.version++
			return sub, []Draft{validationFixtureDraft(TypeValidationRequestChanged, p)}
		})
	}
	launch := func(b *validationGoldenBatch, index int) {
		s.group(1, func(*goldenLog) (ValidationSubmission, []Draft) {
			q := b.attempts[index]
			canary := []byte("package unit\n\nimport \"testing\"\n\nfunc TestDwarfCanaryV1(t *testing.T) { t.Error(\"fixture canary\") }\n")
			p := ValidationAttemptStarted{q.ValidationID, q.AttemptID, q.RunID, s.body(mustEncode(FileManifest{FileManifestFormat, 1, check.DefiningFiles})), s.body(mustEncode(FileManifest{FileManifestFormat, 1, []ValidationFile{{check.Canary.File, "100644", int64(len(canary)), Digest(sha256.Sum256(canary))}}})), Digest(sha256.Sum256(canary)), validationFixtureDigest(q.RunID.String() + " nonce"), validationFixtureDigest(q.RunID.String() + " concrete environment")}
			b.started[index] = p
			return s.submission("launch", "core", b.version, ValidationLaunchArguments(p)), []Draft{validationFixtureDraft(TypeValidationAttemptStarted, p)}
		})
	}
	result := func(b *validationGoldenBatch, index int, outcome, reason string, unconfirmed bool) ValidationAttemptFinished {
		q := b.attempts[index]
		start, started := b.started[index]
		exit := int64(1)
		p := ValidationAttemptFinished{ValidationID: q.ValidationID, AttemptID: q.AttemptID, RunID: q.RunID, Outcome: outcome, Reason: reason, Started: started, ExitKnown: started, ExitCode: &exit, RootExited: started, TreeClosed: started, IOJoined: started, ClosureProof: "live", Canary: ValidationCanaryResult{State: "expected_failed", TemplateSHA256: check.Canary.TemplateSHA256, CanarySHA256: start.CanarySHA256, NonceSHA256: start.NonceSHA256, ObservedPID: 42, MarkerSHA256: validationFixtureDigest(q.RunID.String() + " marker")}, RequiredResults: []ValidationRequiredResult{{"example.test/unit", "TestUnit", "passed"}}, Artifacts: []ValidationArtifact{}, InputIntegrity: "verified", RootPID: 42, ProcessRunID: validationFixtureProcessID(q.AttemptID)}
		if outcome == "failed" {
			p.RequiredResults[0].Outcome = "failed"
		}
		if outcome == "skipped" {
			p.RequiredResults[0].Outcome = "skipped"
		}
		if outcome == "no_status" || outcome == "incomplete" || outcome == "timeout" {
			p.RequiredResults[0].Outcome = "unfinished"
			if outcome == "no_status" {
				p.RequiredResults[0].Outcome = "missing"
			}
			p.Canary.State, p.Canary.ObservedPID, p.Canary.MarkerSHA256, p.RunnerSuspect = "missing", 0, Digest{}, true
		}
		if unconfirmed {
			p.TreeClosed, p.ClosureProof = false, "unconfirmed"
		}
		if !started {
			p.ExitCode, p.RootPID, p.ProcessRunID = nil, 0, uuid.UUID{}
			p.ClosureProof, p.InputIntegrity = "not_started", "unavailable"
			p.Canary = ValidationCanaryResult{State: "not_run", TemplateSHA256: check.Canary.TemplateSHA256}
			p.RequiredResults = []ValidationRequiredResult{}
		} else {
			for _, kind := range []string{"events", "integrity", "manifest", "overlay", "stderr", "stdout"} {
				data := []byte(kind + " " + q.AttemptID.String() + " " + outcome + "\n")
				if kind == "manifest" {
					data = s.files["bodies/"+start.MaterializedManifestSHA256.String()]
				}
				if kind == "overlay" {
					data = s.files["bodies/"+start.OverlaySHA256.String()]
				}
				p.Artifacts = append(p.Artifacts, ValidationArtifact{kind, s.body(data), int64(len(data)), !unconfirmed})
			}
		}
		return p
	}
	finish := func(b *validationGoldenBatch, index int, outcome, reason string, unconfirmed bool, adjacent func(*goldenLog) *ValidationAdjacent) uint64 {
		// Only canonical admissible failures retain the run-artifact obligation.
		pinFailure := outcome == "failed"
		count := 2
		if pinFailure {
			count++
		}
		first := s.group(count, func(g *goldenLog) (ValidationSubmission, []Draft) {
			p := result(b, index, outcome, reason, unconfirmed)
			q := b.attempts[index]
			comparison := ValidationComparison{1, q.Subject, q.CheckSHA256, q.EnvironmentSHA256, q.InventorySHA256, b.request.FieldLawSHA256, b.request.ValidationPolicyHash, q.RunnerSHA256, q.AdapterSHA256}
			data := mustEncode(comparison)
			digest := Digest(sha256.Sum256(data))
			if b.request.ValidationID == validationID(101) {
				preimage("comparison", data)
			}
			history[digest] = append(history[digest], validationGoldenObservation{q.AttemptID, outcome})
			evidenceNumber++
			e := ValidationEvidence{EvidenceID: validationID(6000 + evidenceNumber), ValidationID: q.ValidationID, Subject: q.Subject, DefinitionID: q.DefinitionID, CheckSHA256: q.CheckSHA256, EnvironmentSHA256: q.EnvironmentSHA256, ComparisonSHA256: digest, BindingSHA256: q.BindingSHA256, AttemptIDs: []uuid.UUID{}, Summary: "inconclusive", Canonical: q.Subject.Kind == "canonical", FailureGrade: "none", RequiredCount: 2, Prior: ValidationRational{1, 20}, Confidence: ValidationRational{99, 100}}
			for _, old := range history[digest] {
				e.AttemptIDs = append(e.AttemptIDs, old.attempt)
				if old.outcome == "passed" {
					e.PassCount++
				}
				if old.outcome == "failed" {
					e.FailureCount++
				}
			}
			switch {
			case e.PassCount > 0 && e.FailureCount > 0:
				e.Summary = "flaky"
			case e.PassCount == int64(len(e.AttemptIDs)):
				e.Summary = "passed"
			case e.FailureCount == int64(len(e.AttemptIDs)):
				e.Summary = "failed"
			}
			if b.request.IncidentID != (uuid.UUID{}) {
				e.IncidentID = b.request.IncidentID
			}
			if e.Canonical && e.FailureCount > 0 {
				e.IncidentID, e.FailureGrade = incident, "F1"
				if e.Summary == "failed" && e.FailureCount >= e.RequiredCount {
					e.FailureGrade = "F2"
				}
			}
			if adjacent != nil {
				e.Adjacent = adjacent(g)
			}
			b.evidence = append(b.evidence, e.EvidenceID)
			args := ValidationFinishArguments{AttemptFinished: p, Evidence: e}
			drafts := []Draft{validationFixtureDraft(TypeValidationAttemptFinished, p), validationFixtureDraft(TypeValidationEvidence, e)}
			if e.Canonical && outcome == "failed" {
				args.FailurePinID = validationID(7000 + evidenceNumber)
				refs := append(validationFixtureRefs(b.started[index]), validationFixtureRefs(p)...)
				slices.SortFunc(refs, func(a, b Digest) int { return bytes.Compare(a[:], b[:]) })
				refs = slices.Compact(refs)
				drafts = append(drafts, validationFixturePin(args.FailurePinID, refs))
			}
			return s.submission("finish", "core", b.version, args), drafts
		})
		return first + 2
	}
	end := func(b *validationGoldenBatch, outcome, reason string, retain bool) {
		count := 2
		if retain {
			count = 1
		}
		s.group(count, func(*goldenLog) (ValidationSubmission, []Draft) {
			ids := slices.Clone(b.evidence)
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
			args := ValidationEndArguments{b.request.ValidationID, outcome, reason, ids, []string{}}
			p := ValidationRequestEnded{args.ValidationID, b.version, args.Outcome, args.Reason, args.EvidenceIDs, args.NotLaunchedCheckIDs}
			sub := s.submission("end", "core", b.version, args)
			b.version++
			drafts := []Draft{validationFixtureDraft(TypeValidationRequestEnded, p)}
			if !retain {
				drafts = append(drafts, Draft{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, Payload: mustEncode(BodyUnpin{b.pin})})
			}
			return sub, drafts
		})
	}
	visibility := func(b *validationGoldenBatch, state string) {
		s.group(1, func(g *goldenLog) (ValidationSubmission, []Draft) {
			ids := []uuid.UUID{}
			for _, q := range b.attempts {
				ids = append(ids, q.AttemptID)
			}
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
			p := ValidationVisibilityChanged{ValidationID: b.request.ValidationID, AttemptIDs: ids, ExpectedVersion: b.version, State: state, AuthorizationSHA256: b.request.Authorization.SourcePermissionsSHA256}
			if state == "released" {
				p.PredecessorAcceptanceSequence, p.PredecessorAcceptanceHash = int64(s.acceptance), g.hash(s.acceptance)
			}
			args := ValidationVisibilityArguments{p.ValidationID, p.AttemptIDs, p.State, p.AuthorizationSHA256, p.PredecessorAcceptanceSequence, p.PredecessorAcceptanceHash}
			sub := s.submission("visibility", "core", b.version, args)
			b.version++
			return sub, []Draft{validationFixtureDraft(TypeValidationVisibilityChanged, p)}
		})
	}
	before := []validationGoldenPlan{{"before", 1}}
	passed := enqueue(1, "explicit", before, 0, 0, "runner/1")
	change(passed, "advance", "started")
	launch(passed, 0)
	finish(passed, 0, "passed", "required_passed", false, nil)
	end(passed, "finished", "completed", false)
	failed := enqueue(2, "explicit", before, 0, 0, "runner/failure")
	change(failed, "advance", "started")
	launch(failed, 0)
	incidentSource = finish(failed, 0, "failed", "required_failed", false, nil)
	end(failed, "finished", "completed", true)
	repro := enqueue(3, "reproduction", before, 1, 2, "runner/failure")
	change(repro, "advance", "started")
	launch(repro, 0)
	finish(repro, 0, "failed", "required_failed", false, nil)
	end(repro, "finished", "completed", true)
	hook := enqueue(4, "hook", []validationGoldenPlan{{"observation", 1}}, 0, 0, "runner/1")
	s.group(1, func(g *goldenLog) (ValidationSubmission, []Draft) {
		p := ValidationHookRefused{"on_write_secondary", s.workspace, int64(s.observation), g.hash(s.observation), "busy"}
		return s.submission("refuse", "core", 0, ValidationRefuseArguments(p)), []Draft{validationFixtureDraft(TypeValidationHookRefused, p)}
	})
	change(hook, "advance", "started")
	launch(hook, 0)
	finish(hook, 0, "passed", "required_passed", false, nil)
	end(hook, "finished", "completed", false)
	for i, outcome := range []string{"no_status", "timeout", "skipped", "infra_failure"} {
		b := enqueue(5+i, "explicit", []validationGoldenPlan{{"observation", 1}}, 0, 0, fmt.Sprintf("runner/outcome/%d", i))
		reason := "environment_unavailable"
		if outcome != "infra_failure" {
			change(b, "advance", "started")
			launch(b, 0)
			reason = map[string]string{"no_status": "terminal_missing", "timeout": "timeout", "skipped": "required_skipped"}[outcome]
		}
		finish(b, 0, outcome, reason, false, nil)
		end(b, "unavailable", reason, false)
	}
	cancelled := enqueue(9, "explicit", before, 0, 0, "runner/cancel")
	change(cancelled, "advance", "started")
	launch(cancelled, 0)
	change(cancelled, "cancel", "cancelled")
	finish(cancelled, 0, "incomplete", "cancelled", true, nil)
	s.group(3, func(*goldenLog) (ValidationSubmission, []Draft) {
		p := ValidationRequestChanged{cancelled.request.ValidationID, cancelled.version, "stopping", "closure_confirmed", []ValidationClosedAttempt{{cancelled.attempts[0].AttemptID, validationFixtureProcessID(cancelled.attempts[0].AttemptID), "retained_job"}}}
		e := ValidationRequestEnded{p.ValidationID, p.ExpectedVersion + 1, "cancelled", "cancelled", slices.Clone(cancelled.evidence), []string{}}
		a := ValidationReconcileArguments{ValidationID: p.ValidationID, Changed: &ValidationReconcileChanged{p.Reason, p.ClosedAttempts}, Ended: &ValidationReconcileEnded{e.Outcome, e.Reason, e.EvidenceIDs, e.NotLaunchedCheckIDs}}
		sub := s.submission("reconcile", "core", cancelled.version, a)
		cancelled.version += 2
		return sub, []Draft{validationFixtureDraft(TypeValidationRequestChanged, p), validationFixtureDraft(TypeValidationRequestEnded, e), {Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, Payload: mustEncode(BodyUnpin{cancelled.pin})}}
	})
	interrupted := enqueue(10, "explicit", before, 0, 0, "runner/restart")
	change(interrupted, "advance", "started")
	launch(interrupted, 0)
	s.group(4, func(*goldenLog) (ValidationSubmission, []Draft) {
		p := result(interrupted, 0, "incomplete", "interrupted", false)
		p.ExitKnown, p.ExitCode, p.ClosureProof = false, nil, "name_absent"
		c := ValidationRequestChanged{p.ValidationID, interrupted.version, "stopping", "interrupted", []ValidationClosedAttempt{}}
		e := ValidationRequestEnded{p.ValidationID, interrupted.version + 1, "interrupted", "interrupted", []uuid.UUID{}, []string{}}
		a := ValidationReconcileArguments{p.ValidationID, &p, &ValidationReconcileChanged{c.Reason, c.ClosedAttempts}, &ValidationReconcileEnded{e.Outcome, e.Reason, e.EvidenceIDs, e.NotLaunchedCheckIDs}}
		sub := s.submission("reconcile", "core", interrupted.version, a)
		interrupted.version += 2
		return sub, []Draft{validationFixtureDraft(TypeValidationAttemptFinished, p), validationFixtureDraft(TypeValidationRequestChanged, c), validationFixtureDraft(TypeValidationRequestEnded, e), {Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, Payload: mustEncode(BodyUnpin{interrupted.pin})}}
	})
	sealed := enqueue(11, "promotion", []validationGoldenPlan{{"generation", 1}}, 0, 0, "runner/1")
	visibility(sealed, "sealed")
	change(sealed, "advance", "started")
	launch(sealed, 0)
	finish(sealed, 0, "passed", "required_passed", false, nil)
	obsolete := enqueue(12, "promotion", []validationGoldenPlan{{"generation", 1}}, 0, 0, "runner/obsolete")
	visibility(obsolete, "sealed")
	change(obsolete, "advance", "started")
	launch(obsolete, 0)
	change(obsolete, "cancel", "superseded")
	finish(obsolete, 0, "incomplete", "superseded", false, nil)
	visibility(obsolete, "audit_only_obsolete")
	end(obsolete, "cancelled", "superseded", false)
	s.accept()
	visibility(sealed, "released")
	end(sealed, "finished", "completed", false)
	adjacent := enqueue(13, "reproduction", []validationGoldenPlan{{"before", 2}, {"after", 2}}, 2, 3, "runner/adjacent")
	change(adjacent, "advance", "started")
	for i := range 4 {
		launch(adjacent, i)
		outcome, reason := "passed", "required_passed"
		if i >= 2 {
			outcome, reason = "failed", "required_failed"
		}
		var packet func(*goldenLog) *ValidationAdjacent
		if i == 3 {
			packet = func(g *goldenLog) *ValidationAdjacent {
				return &ValidationAdjacent{validationID(8), int64(s.acceptance), g.hash(s.acceptance), strings.Repeat("a1", 20), strings.Repeat("b2", 20), strings.Repeat("c3", 20), strings.Repeat("d4", 20), slices.Clone(adjacent.evidence[:2]), []uuid.UUID{adjacent.evidence[2], validationID(6000 + evidenceNumber)}, true}
			}
		}
		finish(adjacent, i, outcome, reason, false, packet)
	}
	end(adjacent, "finished", "completed", true)
	consume := Consumption{}
	for tv := range Defined().set {
		consume[tv.Type] = append(consume[tv.Type], tv.Version)
	}
	g, err := buildLog(validationGoldenDir, validationID(1), consume, s.inputs)
	if err != nil {
		return nil, nil, err
	}
	for name, data := range g.files {
		s.files["log/"+name] = data
	}
	return g, s.files, nil
}

func decodeValidationFixture(typ string, data json.RawMessage) (payload, error) {
	switch typ {
	case TypeValidationCheckDefined:
		return DecodeValidationCheckDefined(data)
	case TypeValidationRequested:
		return DecodeValidationRequested(data)
	case TypeValidationRequestChanged:
		return DecodeValidationRequestChanged(data)
	case TypeValidationAttemptQueued:
		return DecodeValidationAttemptQueued(data)
	case TypeValidationAttemptStarted:
		return DecodeValidationAttemptStarted(data)
	case TypeValidationAttemptFinished:
		return DecodeValidationAttemptFinished(data)
	case TypeValidationEvidence:
		return DecodeValidationEvidence(data)
	case TypeValidationRequestEnded:
		return DecodeValidationRequestEnded(data)
	case TypeValidationVisibilityChanged:
		return DecodeValidationVisibilityChanged(data)
	case TypeValidationHookRefused:
		return DecodeValidationHookRefused(data)
	case TypeValidationReceipt:
		return DecodeValidationReceipt(data)
	default:
		return nil, fmt.Errorf("not a validation fixture type: %s", typ)
	}
}

func TestGoldenF4_17(t *testing.T) {
	t.Parallel()
	g, files, err := validationInputs()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(validationGoldenDir)
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
		if _, ok := files[filepath.ToSlash(name)]; !ok {
			if *update {
				return os.Remove(path)
			}
			return fmt.Errorf("unexpected validation fixture %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(data, files[name]) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("CR in %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})
	if err != nil {
		t.Fatal(err)
	}
	types, operations, outcomes, visibility, grades := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	retain := map[uuid.UUID]bool{}
	incidentNext := map[uuid.UUID]int64{}
	incidentVersion := map[uuid.UUID]int64{}
	turnResults := map[uuid.UUID]Record{}
	observations := map[uint64]Record{}
	var definition ValidationCheckDefined
	var firstQueue ValidationAttemptQueued
	var firstEvidence ValidationEvidence
	prelaunch, adjacent, closure, restart := false, false, false, false
	for _, c := range commits {
		for _, r := range c.Records {
			switch r.Type {
			case TypeRuntimeTurnResult:
				turnResults[r.TurnID] = r
			case TypeScratchChanged:
				observations[r.Seq] = r
			}
			if !strings.HasPrefix(r.Type, "validation.") {
				continue
			}
			p, err := decodeValidationFixture(r.Type, r.Payload)
			if err != nil {
				t.Fatalf("seq %d %s: %v", r.Seq, r.Type, err)
			}
			encoded, err := p.Encode()
			if err != nil || !bytes.Equal(encoded, r.Payload) {
				t.Fatalf("seq %d does not round-trip: %v", r.Seq, err)
			}
			types[r.Type] = true
			refs, err := ValidationBodyRefs(p, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if d, ok := p.(ValidationCheckDefined); ok {
				definition = d
				config, _, err := LoadWorkspaceChecks(files["bodies/"+d.CheckPolicyHash.String()])
				if err != nil {
					t.Fatal(err)
				}
				environment, _, err := LoadEnvironmentPolicy(files["bodies/"+d.EnvironmentPolicyHash.String()])
				if err != nil {
					t.Fatal(err)
				}
				refs, err = ValidationBodyRefs(d, &config.Checks[0], &environment.Environments[0])
				if err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(refs, r.BodyRefs) {
				t.Fatalf("seq %d body_refs differs", r.Seq)
			}
			for _, digest := range refs {
				data, ok := files["bodies/"+digest.String()]
				if !ok || Digest(sha256.Sum256(data)) != digest {
					t.Fatalf("seq %d missing or corrupt body %s", r.Seq, digest)
				}
			}
			switch p := p.(type) {
			case ValidationRequested:
				if p.Trigger.Kind == "hook" {
					ending := turnResults[p.Origin.TurnID]
					result, err := DecodeRuntimeTurnResult(ending.Payload)
					if err != nil || ending.AgentID != p.Origin.AgentID || ending.ExecutionSessionID != p.Origin.ExecutionSessionID || ending.ContextID != p.Origin.ContextID ||
						result.BoundSequence >= p.Trigger.SourceSequence || uint64(p.Trigger.SourceSequence) >= ending.Seq || ending.Seq >= c.Records[0].Seq {
						t.Fatal("hook observation is not inside its ended source turn")
					}
					source := observations[uint64(p.Trigger.SourceSequence)]
					observation, err := DecodeScratchChanged(source.Payload)
					if err != nil || source.Hash != p.Trigger.SourceRecordHash.String() || source.ContextID != p.Origin.ContextID || observation.WorkspaceID != p.Subject.WorkspaceID {
						t.Fatal("hook source is not its context's subject observation")
					}
				}
				if p.IncidentID != (uuid.UUID{}) {
					incidentVersion[p.IncidentID]++
					if p.IncidentVersion != incidentVersion[p.IncidentID] {
						t.Fatal("incident version reset")
					}
				}
			case ValidationAttemptQueued:
				if firstQueue.AttemptID == (uuid.UUID{}) {
					firstQueue = p
				}
				if p.IncidentID != (uuid.UUID{}) {
					if p.IncidentExecutionIndex != incidentNext[p.IncidentID] {
						t.Fatal("incident reservation is not contiguous")
					}
					incidentNext[p.IncidentID]++
				}
			case ValidationAttemptFinished:
				outcomes[p.Outcome] = true
				prelaunch = prelaunch || p.Outcome == "infra_failure" && !p.Started && p.Canary.State == "not_run"
				restart = restart || p.Reason == "interrupted"
			case ValidationEvidence:
				if firstEvidence.EvidenceID == (uuid.UUID{}) {
					firstEvidence = p
				}
				grades[p.FailureGrade] = true
				if p.Canonical && p.FailureGrade != "none" {
					retain[p.ValidationID] = true
					if incidentNext[p.IncidentID] == 0 {
						incidentNext[p.IncidentID] = 2
					}
				}
				adjacent = adjacent || p.Adjacent != nil && p.Adjacent.Complete
			case ValidationRequestChanged:
				closure = closure || len(p.ClosedAttempts) > 0
			case ValidationVisibilityChanged:
				visibility[p.State] = true
			}
		}
		last := c.Records[len(c.Records)-1]
		if last.Type != TypeValidationReceipt {
			continue
		}
		r, err := DecodeValidationReceipt(last.Payload)
		if err != nil {
			t.Fatal(err)
		}
		operations[r.Operation] = true
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil || c.Records[0].Type != TypeCommand || command.CommandID != r.CommandID || command.SubmissionSHA256 != r.SubmissionSHA256 || r.EffectSequence != int64(c.Records[1].Seq) {
			t.Fatal("command/receipt identity or effect sequence differs")
		}
		want := []string{TypeCommand}
		switch a := r.Submission.Arguments.(type) {
		case ValidationDefineArguments:
			want = append(want, TypeValidationCheckDefined, TypeBodyPin)
		case ValidationRequestArguments:
			want = append(want, TypeValidationRequested)
			for range a.Attempts {
				want = append(want, TypeValidationAttemptQueued)
			}
			want = append(want, TypeBodyPin)
		case ValidationLaunchArguments:
			want = append(want, TypeValidationAttemptStarted)
		case ValidationFinishArguments:
			want = append(want, TypeValidationAttemptFinished, TypeValidationEvidence)
			if a.FailurePinID != (uuid.UUID{}) {
				want = append(want, TypeBodyPin)
			}
		case ValidationChangeArguments:
			want = append(want, TypeValidationRequestChanged)
		case ValidationEndArguments:
			want = append(want, TypeValidationRequestEnded)
			if !retain[a.ValidationID] {
				want = append(want, TypeBodyUnpin)
			}
		case ValidationVisibilityArguments:
			want = append(want, TypeValidationVisibilityChanged)
		case ValidationRefuseArguments:
			want = append(want, TypeValidationHookRefused)
		case ValidationReconcileArguments:
			if a.AttemptFinished != nil {
				want = append(want, TypeValidationAttemptFinished)
			}
			if a.Changed != nil {
				want = append(want, TypeValidationRequestChanged)
			}
			if a.Ended != nil {
				want = append(want, TypeValidationRequestEnded)
				if !retain[a.ValidationID] {
					want = append(want, TypeBodyUnpin)
				}
			}
		}
		want = append(want, TypeValidationReceipt)
		got := []string{}
		for i, e := range c.Records {
			got = append(got, e.Type)
			actor := ActorCore
			if i == 0 || i == len(c.Records)-1 {
				actor = Actor(r.Submission.Actor)
			}
			if e.Actor != actor || e.OperationID != r.CommandID || e.AgentID != r.Submission.Origin.AgentID || e.ExecutionSessionID != r.Submission.Origin.SessionID || e.ContextID != r.Submission.Origin.ContextID || e.TurnID != (uuid.UUID{}) {
				t.Fatal("validation group loses its authenticated origin")
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s group=%v want=%v", r.Operation, got, want)
		}
	}
	for _, name := range []string{"define", "request", "reproduce", "launch", "finish", "advance", "cancel", "end", "visibility", "reconcile", "refuse"} {
		if !operations["validation."+name] {
			t.Fatalf("missing operation %s", name)
		}
	}
	for _, name := range []string{"check_defined", "requested", "request_changed", "attempt_queued", "attempt_started", "attempt_finished", "evidence", "request_ended", "visibility_changed", "hook_refused", "receipt"} {
		if !types["validation."+name] {
			t.Fatalf("missing type %s", name)
		}
	}
	for _, name := range []string{"passed", "failed", "skipped", "no_status", "incomplete", "timeout", "infra_failure"} {
		if !outcomes[name] {
			t.Fatalf("missing outcome %s", name)
		}
	}
	for _, name := range []string{"sealed", "released", "audit_only_obsolete"} {
		if !visibility[name] {
			t.Fatalf("missing visibility %s", name)
		}
	}
	if !grades["F1"] || !grades["F2"] || !prelaunch || !adjacent || !closure || !restart {
		t.Fatal("golden lacks an incident, prelaunch, adjacency or recovery boundary")
	}
	for name, want := range map[string]Digest{"check": definition.CheckSHA256, "environment": definition.EnvironmentSHA256, "inventory": definition.InventorySHA256, "binding": firstQueue.BindingSHA256, "comparison": firstEvidence.ComparisonSHA256} {
		if got := Digest(sha256.Sum256(files["preimages/"+name+".json"])); got != want {
			t.Fatalf("%s preimage=%s want=%s", name, got, want)
		}
	}
	t.Logf("Defined=79 commits=%d records=%d operations=%s", len(commits), cursor, strings.Join(slices.Sorted(maps.Keys(operations)), ","))
}

func validationFixtureProcessID(attempt uuid.UUID) uuid.UUID {
	return validationID(0x9000 + int(attempt[14])*256 + int(attempt[15]))
}
