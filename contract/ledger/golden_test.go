package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"
)

var update = flag.Bool("update", false, "rewrite the golden F0 logs in testdata")

const (
	// goldenDir holds the golden F0 log, a two-segment log directory.
	goldenDir = "testdata/f0/log"
	// catalogueDir holds the golden catalogue log, a one-segment log
	// directory holding every F0 record type.
	catalogueDir = "testdata/f0/catalogue/log"
	// workspaceDir holds the F2-05 catalogue's one-segment log.
	workspaceDir = "testdata/f2/05/log"
	// promotionDir holds ticket 06's part of F2, frozen (D118), in one
	// segment.
	promotionDir = "testdata/f2/06/log"
	// scratchChangeDir holds ticket 07's part of F2, frozen (D119),
	// in one segment.
	scratchChangeDir = "testdata/f2/07/log"
	// finalizationDir holds ticket 08's part of F2, frozen (D123),
	// in one segment.
	finalizationDir = "testdata/f2/08/log"
)

// fid returns a fixed fixture UUIDv7 ending in suffix.
func fid(suffix string) uuid.UUID {
	return uuid.MustParse("0199c000-0000-7000-8000-0000000000" + suffix)
}

var (
	goldenColony        = fid("01")
	catalogueColony     = fid("02")
	workspaceColony     = fid("03")
	promotionColony     = fid("04")
	scratchChangeColony = fid("05")
	finalizationColony  = fid("40")
)

// The bodies and the submission that the catalogue log references.
var (
	bodyOne          = []byte("golden body one\n")
	bodyTwo          = []byte("golden body two\n")
	bodyThree        = []byte("golden body three\n")
	goldenB1         = Digest(sha256.Sum256(bodyOne))
	goldenB2         = Digest(sha256.Sum256(bodyTwo))
	goldenB3         = Digest(sha256.Sum256(bodyThree))
	goldenSubmission = Digest(sha256.Sum256([]byte("golden submission\n")))
)

var (
	promotionI1  = Digest(sha256.Sum256([]byte("promotion capture inputs one\n")))
	promotionM1  = Digest(sha256.Sum256([]byte("promotion manifest one\n")))
	promotionM2  = Digest(sha256.Sum256([]byte("promotion manifest two\n")))
	promotionM3  = Digest(sha256.Sum256([]byte("promotion manifest three\n")))
	promotionM4  = Digest(sha256.Sum256([]byte("promotion manifest four\n")))
	promotionCR1 = Digest(sha256.Sum256([]byte("promotion conflict report one\n")))
	promotionCR2 = Digest(sha256.Sum256([]byte("promotion conflict report two\n")))
)

var (
	scratchChangeI  = Digest(sha256.Sum256([]byte("scratch change capture inputs one\n")))
	scratchChangeM1 = Digest(sha256.Sum256([]byte("scratch change manifest one\n")))
	scratchChangeM2 = Digest(sha256.Sum256([]byte("scratch change manifest two\n")))
	scratchChangeM3 = Digest(sha256.Sum256([]byte("scratch change manifest three\n")))
	scratchChangeM4 = Digest(sha256.Sum256([]byte("scratch change manifest four\n")))
	scratchChangeM5 = Digest(sha256.Sum256([]byte("scratch change manifest five\n")))
	scratchChangeM6 = Digest(sha256.Sum256([]byte("scratch change manifest six\n")))
	scratchChangeM7 = Digest(sha256.Sum256([]byte("scratch change manifest seven\n")))
	scratchChangeCR = Digest(sha256.Sum256([]byte("scratch change conflict report one\n")))
)

var (
	finalizationI  = Digest(sha256.Sum256([]byte("finalization capture inputs one\n")))
	finalizationM1 = Digest(sha256.Sum256([]byte("finalization manifest one\n")))
	finalizationM2 = Digest(sha256.Sum256([]byte("finalization manifest two\n")))
	finalizationM3 = Digest(sha256.Sum256([]byte("finalization manifest three\n")))
	finalizationM4 = Digest(sha256.Sum256([]byte("finalization manifest four\n")))
	finalizationM5 = Digest(sha256.Sum256([]byte("finalization manifest five\n")))
	finalizationM6 = Digest(sha256.Sum256([]byte("finalization manifest six\n")))
	finalizationM7 = Digest(sha256.Sum256([]byte("finalization manifest seven\n")))
)

// sortedDigests returns ds in ascending order.
func sortedDigests(ds ...Digest) []Digest {
	return slices.SortedFunc(slices.Values(ds), func(a, b Digest) int { return bytes.Compare(a[:], b[:]) })
}

// mustEncode returns the payload p encodes. The golden inputs are fixed, so
// an error is a bug in the inputs or the encoder.
func mustEncode(p payload) json.RawMessage {
	js, err := p.Encode()
	if err != nil {
		panic(err)
	}
	return js
}

// goldenCommit is one commit of a golden log's fixed inputs.
type goldenCommit struct {
	header     CommitHeader
	newSegment bool // the commit starts a new segment
	drafts     []Draft
}

func goldenInputs() []goldenCommit {
	header := func(commit string, t int64, held bool) CommitHeader {
		return CommitHeader{ColonyID: goldenColony, CommitID: fid(commit), EventTimeNS: t, ClockHeld: held}
	}
	return []goldenCommit{
		{header: header("c1", 1791000000000000000, false), drafts: []Draft{
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":1}`)},
		}},
		{header: header("c2", 1791000000000000500, false), drafts: []Draft{
			{
				Type: "fixture.sample", TypeVersion: 1, Actor: ActorAgent,
				AgentID: fid("a1"), ExecutionSessionID: fid("b1"), ContextID: fid("d1"), TurnID: fid("e1"), OperationID: fid("f1"),
				MeasurementPath: "process_exit",
				Payload:         json.RawMessage(`{"n":2,"text":"dwarf ⚒ <&>"}`),
			},
			{Type: "fixture.other", TypeVersion: 1, Actor: ActorRunner, OperationID: fid("f1"), Payload: json.RawMessage(`{}`)},
		}},
		{header: header("c3", 1791000000000000500, true), drafts: []Draft{
			{Type: "fixture.sample", TypeVersion: 2, Actor: ActorOwner, Payload: json.RawMessage(`{"n":4,"note":"v2"}`)},
		}},
		{header: header("c4", 1791000001000000000, false), newSegment: true, drafts: []Draft{
			{Type: "fixture.other", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{}`)},
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":6}`)},
		}},
	}
}

// catalogueInputs covers every F0 type version, body references on fixture,
// pin and command-commit records, a command commit with an effect, both
// states of keep_bytes and a pin's release. Its two collection records are
// the ones Core's collection must produce at their policies.
func catalogueInputs() []goldenCommit {
	header := func(commit string, t int64) CommitHeader {
		return CommitHeader{ColonyID: catalogueColony, CommitID: fid(commit), EventTimeNS: t}
	}
	return []goldenCommit{
		{header: header("c5", 1791000010000000000), drafts: []Draft{
			{
				Type: "fixture.sample", TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"),
				BodyRefs: sortedDigests(goldenB1, goldenB2, goldenB3),
				Payload:  json.RawMessage(`{"n":1}`),
			},
		}},
		{header: header("c6", 1791000011000000000), drafts: []Draft{
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(BodyPin{PinID: fid("21")})},
		}},
		{header: header("c7", 1791000012000000000), drafts: []Draft{
			{
				Type: TypeCommand, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"),
				Payload: mustEncode(Command{CommandID: fid("31"), SubmissionSHA256: goldenSubmission}),
			},
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, BodyRefs: []Digest{goldenB3}, Payload: json.RawMessage(`{"n":4}`)},
		}},
		{header: header("c8", 1791000013000000000), drafts: []Draft{
			{Type: TypeBodiesCollected, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodiesCollected{
				BeforeNS: 1791000011500000000,
				Bodies:   []CollectedBody{{SHA256: goldenB1, Bytes: int64(len(bodyOne))}},
			})},
		}},
		{header: header("c9", 1791000014000000000), drafts: []Draft{
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodyUnpin{PinID: fid("21")})},
		}},
		{header: header("ca", 1791000015000000000), drafts: []Draft{
			{Type: TypeBodiesCollected, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodiesCollected{
				BeforeNS:  1791000014500000000,
				KeepBytes: 20,
				Bodies:    []CollectedBody{{SHA256: goldenB2, Bytes: int64(len(bodyTwo))}},
			})},
		}},
	}
}

// workspaceInputs follows the F2-05 registration, scratch and closure
// history, with both Git object formats and a pinned-input revision update.
func workspaceInputs() []goldenCommit {
	header := func(commit string, t int64) CommitHeader {
		return CommitHeader{ColonyID: workspaceColony, CommitID: fid(commit), EventTimeNS: t}
	}
	return []goldenCommit{
		{header: header("c1", 1791000020000000000), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("41"), CommonDir: "C:/fixture/repo-a/.git", ObjectFormat: "sha1"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB1, goldenSubmission), Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("41"), Revision: 1, CaptureInputsSHA256: goldenB1, PinID: fid("21")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB1, goldenSubmission), Payload: mustEncode(BodyPin{PinID: fid("21")})},
		}},
		{header: header("c2", 1791000021000000000), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("42"), CommonDir: "/fixture/repo-b/.git", ObjectFormat: "sha256"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("42"), Revision: 1, CaptureInputsSHA256: goldenB2, PinID: fid("22")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(BodyPin{PinID: fid("22")})},
		}},
		{header: header("c3", 1791000022000000000), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("d1"), Payload: mustEncode(ScratchCreated{ScratchID: fid("51"), WorkspaceID: fid("41"), ContextID: fid("d1"), Dir: "0a1b2c3d", Branch: "refs/heads/dwarf-harness/0a1b2c3d", BaseCommit: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", AnchorTree: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"})},
		}},
		{header: header("c4", 1791000023000000000), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("d2"), Payload: mustEncode(ScratchCreated{ScratchID: fid("52"), WorkspaceID: fid("42"), ContextID: fid("d2"), Dir: "4e5f6a7b", Branch: "refs/heads/dwarf-harness/4e5f6a7b", BaseCommit: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", AnchorTree: "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"})},
		}},
		{header: header("c5", 1791000024000000000), drafts: []Draft{
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB3, goldenSubmission), Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("41"), Revision: 2, CaptureInputsSHA256: goldenB3, PinID: fid("23")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB3, goldenSubmission), Payload: mustEncode(BodyPin{PinID: fid("23")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodyUnpin{PinID: fid("21")})},
		}},
		{header: header("c6", 1791000025000000000), drafts: []Draft{
			{Type: TypeScratchClosed, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"), Payload: mustEncode(ScratchClosed{ScratchID: fid("51"), ContextID: fid("d1"), Reason: "abandoned"})},
		}},
	}
}

// promotionInputs covers ticket 06's part of F2, frozen (D118):
// admissions, generations, report retention and a failed
// lineage's cascade followed by a new root. No fixture ID is reused from
// the other goldens.
func promotionInputs() []goldenCommit {
	header := func(commit string, seconds int64) CommitHeader {
		return CommitHeader{ColonyID: promotionColony, CommitID: fid(commit), EventTimeNS: 1791000030000000000 + seconds*1000000000}
	}
	b0, a0 := strings.Repeat("a0", 20), strings.Repeat("a1", 20)
	b1, bt1 := strings.Repeat("a2", 20), strings.Repeat("a3", 20)
	t1, t2, t3, t4 := strings.Repeat("b1", 20), strings.Repeat("b2", 20), strings.Repeat("b3", 20), strings.Repeat("b4", 20)
	d1, dt1 := strings.Repeat("d1", 20), strings.Repeat("e1", 20)
	d2, dt2 := strings.Repeat("d2", 20), strings.Repeat("e2", 20)
	d5, dt5 := strings.Repeat("d5", 20), strings.Repeat("e5", 20)
	r1 := PromotionAdmitted{
		RequestID: fid("71"), WorkspaceID: fid("61"), ScratchID: fid("62"), ContextID: fid("64"), LineageID: fid("81"), Checkpoint: 1,
		ScratchBase: b0, BaselineTree: a0, DeltaBaseTree: a0, CaptureID: fid("91"), SealedTree: t1, ManifestSHA256: promotionM1,
		InputsRevision: 1, CaptureInputsSHA256: promotionI1, PinID: fid("a3"),
	}
	r2 := PromotionAdmitted{
		RequestID: fid("72"), WorkspaceID: fid("61"), ScratchID: fid("62"), ContextID: fid("64"), LineageID: fid("81"), Checkpoint: 2, ParentRequestID: fid("71"),
		ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: fid("92"), SealedTree: t2, ManifestSHA256: promotionM2,
		InputsRevision: 1, CaptureInputsSHA256: promotionI1, PinID: fid("a4"),
	}
	r3 := PromotionAdmitted{
		RequestID: fid("73"), WorkspaceID: fid("61"), ScratchID: fid("63"), ContextID: fid("65"), LineageID: fid("82"), Checkpoint: 1,
		ScratchBase: b0, BaselineTree: a0, DeltaBaseTree: a0, CaptureID: fid("93"), SealedTree: t3, ManifestSHA256: promotionM3,
		InputsRevision: 1, CaptureInputsSHA256: promotionI1, PinID: fid("a5"),
	}
	r4 := PromotionAdmitted{
		RequestID: fid("74"), WorkspaceID: fid("61"), ScratchID: fid("62"), ContextID: fid("64"), LineageID: fid("83"), Checkpoint: 1, RestartFrom: fid("72"),
		ScratchBase: b0, BaselineTree: a0, DeltaBaseTree: a0, CaptureID: fid("94"), SealedTree: t4, ManifestSHA256: promotionM4,
		InputsRevision: 1, CaptureInputsSHA256: promotionI1, PinID: fid("a6"),
	}
	g1 := GenerationDerived{
		GenerationID: fid("11"), RequestID: fid("71"), PredecessorCommit: b0, PredecessorTree: a0,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d1, DerivedTree: dt1,
	}
	g2 := GenerationDerived{
		GenerationID: fid("12"), RequestID: fid("72"), PredecessorRequestID: fid("71"), PredecessorGenerationID: fid("11"), PredecessorCommit: d1, PredecessorTree: dt1,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d2, DerivedTree: dt2,
	}
	g3 := GenerationDerived{
		GenerationID: fid("13"), RequestID: fid("73"), PredecessorRequestID: fid("72"), PredecessorGenerationID: fid("12"), PredecessorCommit: d2, PredecessorTree: dt2,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationConflict, ConflictSHA256: promotionCR1, ReportPinID: fid("b2"),
	}
	g4 := GenerationDerived{
		GenerationID: fid("14"), RequestID: fid("71"), Supersedes: fid("11"), PredecessorCommit: b1, PredecessorTree: bt1,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationConflict, ConflictSHA256: promotionCR2, ReportPinID: fid("b3"),
	}
	g5 := GenerationDerived{
		GenerationID: fid("15"), RequestID: fid("73"), Supersedes: fid("13"), PredecessorCommit: b1, PredecessorTree: bt1,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d5, DerivedTree: dt5,
	}
	return []goldenCommit{
		{header: header("06", 0), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("61"), CommonDir: "C:/fixture/repo-c/.git", ObjectFormat: "sha1"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{promotionI1}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("61"), Revision: 1, CaptureInputsSHA256: promotionI1, PinID: fid("a2")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{promotionI1}, Payload: mustEncode(BodyPin{PinID: fid("a2")})},
		}},
		{header: header("07", 1), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(ScratchCreated{ScratchID: fid("62"), WorkspaceID: fid("61"), ContextID: fid("64"), Dir: "1a2b3c4d", Branch: "refs/heads/dwarf-harness/1a2b3c4d", BaseCommit: b0, AnchorTree: a0})},
		}},
		{header: header("08", 2), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("65"), Payload: mustEncode(ScratchCreated{ScratchID: fid("63"), WorkspaceID: fid("61"), ContextID: fid("65"), Dir: "5e6f7a8b", Branch: "refs/heads/dwarf-harness/5e6f7a8b", BaseCommit: b0, AnchorTree: a0})},
		}},
		{header: header("09", 3), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM1}, Payload: mustEncode(r1)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM1}, Payload: mustEncode(BodyPin{PinID: fid("a3")})},
		}},
		{header: header("0a", 4), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM2}, Payload: mustEncode(r2)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM2}, Payload: mustEncode(BodyPin{PinID: fid("a4")})},
		}},
		{header: header("0b", 5), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorOwner, ContextID: fid("65"), BodyRefs: []Digest{promotionM3}, Payload: mustEncode(r3)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, ContextID: fid("65"), BodyRefs: []Digest{promotionM3}, Payload: mustEncode(BodyPin{PinID: fid("a5")})},
		}},
		{header: header("0c", 6), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(g1)},
		}},
		{header: header("0d", 7), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(g2)},
		}},
		{header: header("0e", 8), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("65"), BodyRefs: []Digest{promotionCR1}, Payload: mustEncode(g3)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("65"), BodyRefs: []Digest{promotionCR1}, Payload: mustEncode(BodyPin{PinID: fid("b2")})},
		}},
		{header: header("0f", 9), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), BodyRefs: []Digest{promotionCR2}, Payload: mustEncode(g4)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), BodyRefs: []Digest{promotionCR2}, Payload: mustEncode(BodyPin{PinID: fid("b3")})},
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(PromotionFailed{RequestID: fid("71"), Outcome: FailedConflict, GenerationID: fid("14")})},
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(PromotionFailed{RequestID: fid("72"), Outcome: FailedDependencyFailed, FailedRequestID: fid("71")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(BodyUnpin{PinID: fid("a3")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("64"), Payload: mustEncode(BodyUnpin{PinID: fid("a4")})},
		}},
		{header: header("10", 10), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("65"), Payload: mustEncode(g5)},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("65"), Payload: mustEncode(BodyUnpin{PinID: fid("b2")})},
		}},
		{header: header("16", 11), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM4}, Payload: mustEncode(r4)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("64"), BodyRefs: []Digest{promotionM4}, Payload: mustEncode(BodyPin{PinID: fid("a6")})},
		}},
	}
}

// scratchChangeInputs covers ticket 07's part of F2, frozen (D119):
// observations, private merge-back plans and results,
// temporary closures and admission provenance. The IDs are disjoint from
// the earlier goldens.
func scratchChangeInputs() []goldenCommit {
	header := func(commit string, seconds int64) CommitHeader {
		return CommitHeader{ColonyID: scratchChangeColony, CommitID: fid(commit), EventTimeNS: 1791000040000000000 + seconds*1000000000}
	}
	b0, a0 := strings.Repeat("c0", 20), strings.Repeat("c1", 20)
	t1, tt1 := strings.Repeat("c2", 20), strings.Repeat("c3", 20)
	s1, st1 := strings.Repeat("c4", 20), strings.Repeat("c5", 20)
	s2, st2 := strings.Repeat("c6", 20), strings.Repeat("c7", 20)
	t2, tt2 := strings.Repeat("c8", 20), strings.Repeat("c9", 20)
	g2, gt2 := strings.Repeat("ca", 20), strings.Repeat("cb", 20)
	s3, st3 := strings.Repeat("cc", 20), strings.Repeat("cd", 20)
	t3, tt3 := strings.Repeat("ce", 20), strings.Repeat("cf", 20)
	d0 := strings.Repeat("d0", 20)
	o1 := ScratchChanged{
		ObservationID: fid("58"), WorkspaceID: fid("43"), ScratchID: fid("53"), ContextID: fid("44"),
		PreviousCommit: b0, PreviousTree: a0, ScratchCommit: t1, ScratchTree: tt1,
		WrittenPaths: []string{"task.txt"}, ChangedPaths: []string{"task.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM1, PinID: fid("a8"),
	}
	o2 := ScratchChanged{
		ObservationID: fid("59"), WorkspaceID: fid("43"), ScratchID: fid("55"), ContextID: fid("45"), ParentScratchID: fid("53"),
		PreviousCommit: t1, PreviousTree: tt1, ScratchCommit: s1, ScratchTree: st1,
		WrittenPaths: []string{"new.txt"}, ChangedPaths: []string{"new.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM2, PinID: fid("a9"),
	}
	o3 := ScratchChanged{
		ObservationID: fid("5a"), WorkspaceID: fid("43"), ScratchID: fid("56"), ContextID: fid("45"), ParentScratchID: fid("53"),
		PreviousCommit: s1, PreviousTree: st1, ScratchCommit: s2, ScratchTree: st2,
		WrittenPaths: []string{"other.txt"}, ChangedPaths: []string{"other.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM3, PinID: fid("aa"),
	}
	o4 := ScratchChanged{
		ObservationID: fid("5b"), WorkspaceID: fid("43"), ScratchID: fid("53"), ContextID: fid("44"),
		PreviousCommit: s1, PreviousTree: st1, ScratchCommit: t2, ScratchTree: tt2,
		WrittenPaths: []string{"task.txt"}, ChangedPaths: []string{"new.txt", "task.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM4, PinID: fid("ab"),
	}
	o5 := ScratchChanged{
		ObservationID: fid("5c"), WorkspaceID: fid("43"), ScratchID: fid("57"), ContextID: fid("45"), ParentScratchID: fid("53"),
		PreviousCommit: g2, PreviousTree: gt2, ScratchCommit: s3, ScratchTree: st3,
		WrittenPaths: []string{"a.txt"}, ChangedPaths: []string{"a.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM5, PinID: fid("ac"),
	}
	o6 := ScratchChanged{
		ObservationID: fid("5d"), WorkspaceID: fid("43"), ScratchID: fid("53"), ContextID: fid("44"),
		PreviousCommit: g2, PreviousTree: gt2, ScratchCommit: t3, ScratchTree: tt3,
		WrittenPaths: []string{"a.txt"}, ChangedPaths: []string{"a.txt", "new.txt", "other.txt", "task.txt"},
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, ScratchRules: "scratch-rules/1", ManifestSHA256: scratchChangeM6, PinID: fid("ad"),
	}
	mb1 := ScratchMergeBack{
		MergeBackID: fid("95"), WorkspaceID: fid("43"), ScratchID: fid("55"), ParentScratchID: fid("53"),
		ContextID: fid("44"), AuthoringContextID: fid("45"), SourceObservationID: fid("59"),
		ForkCommit: t1, ForkTree: tt1, ParentCommit: t1, ParentTree: tt1, SourceCommit: s1, SourceTree: st1,
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, IntegrationRules: "scratch-merge-rules/1",
		Outcome: MergeBackPrepared, TargetCommit: s1, TargetTree: st1, WrittenPaths: []string{"new.txt"},
		ManifestSHA256: scratchChangeM1, PinID: fid("ae"),
	}
	mb1Applied := mb1
	mb1Applied.Outcome, mb1Applied.ManifestSHA256, mb1Applied.PinID = MergeBackFastForward, Digest{}, uuid.UUID{}
	mb1Applied.ChangedPaths = []string{"new.txt", "task.txt"}
	mb2 := ScratchMergeBack{
		MergeBackID: fid("96"), WorkspaceID: fid("43"), ScratchID: fid("56"), ParentScratchID: fid("53"),
		ContextID: fid("44"), AuthoringContextID: fid("45"), SourceObservationID: fid("5a"),
		ForkCommit: s1, ForkTree: st1, ParentCommit: t2, ParentTree: tt2, SourceCommit: s2, SourceTree: st2,
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, IntegrationRules: "scratch-merge-rules/1",
		Outcome: MergeBackPrepared, TargetCommit: g2, TargetTree: gt2, WrittenPaths: []string{"other.txt"},
		ManifestSHA256: scratchChangeM4, PinID: fid("af"),
	}
	mb2Failed := mb2
	mb2Failed.Outcome, mb2Failed.ManifestSHA256, mb2Failed.PinID = MergeBackWriteFailed, Digest{}, uuid.UUID{}
	mb2Failed.Failure = MergeBackFailureIndexLock
	mb2Applied := mb2
	mb2Applied.Outcome, mb2Applied.ManifestSHA256, mb2Applied.PinID = MergeBackMerged, Digest{}, uuid.UUID{}
	mb2Applied.ChangedPaths = []string{"new.txt", "other.txt", "task.txt"}
	mb3 := ScratchMergeBack{
		MergeBackID: fid("97"), WorkspaceID: fid("43"), ScratchID: fid("57"), ParentScratchID: fid("53"),
		ContextID: fid("44"), AuthoringContextID: fid("45"), SourceObservationID: fid("5c"),
		ForkCommit: g2, ForkTree: gt2, ParentCommit: t3, ParentTree: tt3, SourceCommit: s3, SourceTree: st3,
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, IntegrationRules: "scratch-merge-rules/1",
		Outcome: MergeBackConflict, WrittenPaths: []string{"a.txt"}, ConflictSHA256: scratchChangeCR, ReportPinID: fid("b4"),
	}
	r1 := PromotionAdmitted{
		RequestID: fid("b5"), WorkspaceID: fid("43"), ScratchID: fid("53"), ContextID: fid("44"), LineageID: fid("b6"), Checkpoint: 1,
		ScratchBase: b0, BaselineTree: a0, DeltaBaseTree: a0, CaptureID: fid("b7"), SealedTree: d0, ManifestSHA256: scratchChangeM7,
		InputsRevision: 1, CaptureInputsSHA256: scratchChangeI, PinID: fid("b8"), ScratchObservationID: fid("5d"), MergeBackIDs: []uuid.UUID{fid("95"), fid("96")},
	}
	return []goldenCommit{
		{header: header("17", 0), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("43"), CommonDir: "C:/fixture/repo-d/.git", ObjectFormat: "sha1"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{scratchChangeI}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("43"), Revision: 1, CaptureInputsSHA256: scratchChangeI, PinID: fid("a7")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{scratchChangeI}, Payload: mustEncode(BodyPin{PinID: fid("a7")})},
		}},
		{header: header("18", 1), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(ScratchCreated{ScratchID: fid("53"), WorkspaceID: fid("43"), ContextID: fid("44"), Dir: "9c8d7e6f", Branch: "refs/heads/dwarf-harness/9c8d7e6f", BaseCommit: b0, AnchorTree: a0})},
		}},
		{header: header("19", 2), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM1}, Payload: mustEncode(o1)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM1}, Payload: mustEncode(BodyPin{PinID: fid("a8")})},
		}},
		{header: header("1a", 3), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), Payload: mustEncode(ScratchCreated{ScratchID: fid("54"), WorkspaceID: fid("43"), ContextID: fid("45"), Dir: "0a1b2c3e", Branch: "refs/heads/dwarf-harness/0a1b2c3e", BaseCommit: b0, AnchorTree: a0})},
		}},
		{header: header("1b", 4), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), Payload: mustEncode(ScratchCreated{ScratchID: fid("55"), WorkspaceID: fid("43"), ContextID: fid("45"), Dir: "3d4e5f60", Branch: "refs/heads/dwarf-harness/3d4e5f60", BaseCommit: t1, AnchorTree: tt1, ParentScratchID: fid("53")})},
		}},
		{header: header("1c", 5), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM2}, Payload: mustEncode(o2)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM2}, Payload: mustEncode(BodyPin{PinID: fid("a9")})},
		}},
		{header: header("1d", 6), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: sortedDigests(scratchChangeI, scratchChangeM1), Payload: mustEncode(mb1)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: sortedDigests(scratchChangeI, scratchChangeM1), Payload: mustEncode(BodyPin{PinID: fid("ae")})},
		}},
		{header: header("1e", 7), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(mb1Applied)},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(BodyUnpin{PinID: fid("ae")})},
		}},
		{header: header("1f", 8), drafts: []Draft{
			{Type: TypeScratchClosed, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(ScratchClosed{ScratchID: fid("55"), ContextID: fid("45"), Reason: ReasonTemporaryMergedBack})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(BodyUnpin{PinID: fid("a9")})},
		}},
		{header: header("24", 9), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), Payload: mustEncode(ScratchCreated{ScratchID: fid("56"), WorkspaceID: fid("43"), ContextID: fid("45"), Dir: "7a8b9c0d", Branch: "refs/heads/dwarf-harness/7a8b9c0d", BaseCommit: s1, AnchorTree: st1, ParentScratchID: fid("53")})},
		}},
		{header: header("25", 10), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM3}, Payload: mustEncode(o3)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM3}, Payload: mustEncode(BodyPin{PinID: fid("aa")})},
		}},
		{header: header("26", 11), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM4}, Payload: mustEncode(o4)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM4}, Payload: mustEncode(BodyPin{PinID: fid("ab")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(BodyUnpin{PinID: fid("a8")})},
		}},
		{header: header("27", 12), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: sortedDigests(scratchChangeI, scratchChangeM4), Payload: mustEncode(mb2)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: sortedDigests(scratchChangeI, scratchChangeM4), Payload: mustEncode(BodyPin{PinID: fid("af")})},
		}},
		{header: header("28", 13), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(mb2Failed)},
		}},
		{header: header("29", 14), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(mb2Applied)},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(BodyUnpin{PinID: fid("af")})},
		}},
		{header: header("2a", 15), drafts: []Draft{
			{Type: TypeScratchClosed, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(ScratchClosed{ScratchID: fid("56"), ContextID: fid("45"), Reason: ReasonTemporaryMergedBack})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(BodyUnpin{PinID: fid("aa")})},
		}},
		{header: header("2b", 16), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), Payload: mustEncode(ScratchCreated{ScratchID: fid("57"), WorkspaceID: fid("43"), ContextID: fid("45"), Dir: "b1c2d3e4", Branch: "refs/heads/dwarf-harness/b1c2d3e4", BaseCommit: g2, AnchorTree: gt2, ParentScratchID: fid("53")})},
		}},
		{header: header("2c", 17), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM5}, Payload: mustEncode(o5)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("45"), BodyRefs: []Digest{scratchChangeM5}, Payload: mustEncode(BodyPin{PinID: fid("ac")})},
		}},
		{header: header("2d", 18), drafts: []Draft{
			{Type: TypeScratchChanged, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM6}, Payload: mustEncode(o6)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM6}, Payload: mustEncode(BodyPin{PinID: fid("ad")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), Payload: mustEncode(BodyUnpin{PinID: fid("ab")})},
		}},
		{header: header("2e", 19), drafts: []Draft{
			{Type: TypeScratchMergeBack, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeCR}, Payload: mustEncode(mb3)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeCR}, Payload: mustEncode(BodyPin{PinID: fid("b4")})},
		}},
		{header: header("2f", 20), drafts: []Draft{
			{Type: TypeScratchClosed, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(ScratchClosed{ScratchID: fid("57"), ContextID: fid("45"), Reason: "abandoned"})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("45"), Payload: mustEncode(BodyUnpin{PinID: fid("ac")})},
		}},
		{header: header("30", 21), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM7}, Payload: mustEncode(r1)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("44"), BodyRefs: []Digest{scratchChangeM7}, Payload: mustEncode(BodyPin{PinID: fid("b8")})},
		}},
	}
}

// finalizationInputs covers ticket 08's part of F2, frozen (D123):
// accepted witnesses, no-change and failure endings. Its IDs
// are disjoint from the earlier goldens.
func finalizationInputs() []goldenCommit {
	header := func(commit string, seconds int64) CommitHeader {
		return CommitHeader{ColonyID: finalizationColony, CommitID: fid(commit), EventTimeNS: 1791000050000000000 + seconds*1000000000}
	}
	b0, t0 := strings.Repeat("e6", 20), strings.Repeat("e7", 20)
	t1, d1, d2 := strings.Repeat("e8", 20), strings.Repeat("e9", 20), strings.Repeat("ea", 20)
	t3, t4, t5 := strings.Repeat("eb", 20), strings.Repeat("ec", 20), strings.Repeat("ed", 20)
	d5, t6, d6, t7 := strings.Repeat("ee", 20), strings.Repeat("ef", 20), strings.Repeat("f2", 20), strings.Repeat("f3", 20)
	r1 := PromotionAdmitted{
		RequestID: fid("98"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("84"), Checkpoint: 1,
		ScratchBase: b0, BaselineTree: t0, DeltaBaseTree: t0, CaptureID: fid("d3"), SealedTree: t1, ManifestSHA256: finalizationM1,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e2"),
	}
	r2 := PromotionAdmitted{
		RequestID: fid("99"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("84"), Checkpoint: 2, ParentRequestID: fid("98"),
		ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: fid("d4"), SealedTree: t1, ManifestSHA256: finalizationM2,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e3"),
	}
	r3 := PromotionAdmitted{
		RequestID: fid("9a"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("84"), Checkpoint: 3, ParentRequestID: fid("99"),
		ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: fid("d5"), SealedTree: t3, ManifestSHA256: finalizationM3,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e4"),
	}
	r4 := PromotionAdmitted{
		RequestID: fid("9b"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("84"), Checkpoint: 4, ParentRequestID: fid("9a"),
		ScratchBase: b0, BaselineTree: t3, DeltaBaseTree: t3, CaptureID: fid("d6"), SealedTree: t4, ManifestSHA256: finalizationM4,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e5"),
	}
	r5 := PromotionAdmitted{
		RequestID: fid("9c"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("85"), Checkpoint: 1, RestartFrom: fid("9b"),
		ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: fid("d7"), SealedTree: t5, ManifestSHA256: finalizationM5,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e6"),
	}
	r6 := PromotionAdmitted{
		RequestID: fid("9d"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("86"), Checkpoint: 1, RestartFrom: fid("9c"),
		ScratchBase: b0, BaselineTree: t1, DeltaBaseTree: t1, CaptureID: fid("d8"), SealedTree: t6, ManifestSHA256: finalizationM6,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e7"),
	}
	r7 := PromotionAdmitted{
		RequestID: fid("9e"), WorkspaceID: fid("46"), ScratchID: fid("48"), ContextID: fid("47"), LineageID: fid("86"), Checkpoint: 2, ParentRequestID: fid("9d"),
		ScratchBase: b0, BaselineTree: t6, DeltaBaseTree: t6, CaptureID: fid("d9"), SealedTree: t7, ManifestSHA256: finalizationM7,
		InputsRevision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e8"),
	}
	g1 := GenerationDerived{
		GenerationID: fid("ea"), RequestID: fid("98"), PredecessorCommit: b0, PredecessorTree: t0,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d1, DerivedTree: t1,
	}
	g2 := GenerationDerived{
		GenerationID: fid("eb"), RequestID: fid("99"), PredecessorCommit: d1, PredecessorTree: t1, PredecessorWitnessSeq: 9,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d2, DerivedTree: t1,
	}
	g5 := GenerationDerived{
		GenerationID: fid("ec"), RequestID: fid("9c"), PredecessorCommit: d1, PredecessorTree: t1, PredecessorWitnessSeq: 9,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d5, DerivedTree: t5,
	}
	g6 := GenerationDerived{
		GenerationID: fid("ed"), RequestID: fid("9d"), PredecessorCommit: d1, PredecessorTree: t1, PredecessorWitnessSeq: 9,
		InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: GenerationClean, DerivedCommit: d6, DerivedTree: t6,
	}
	i1 := FinalizationIntended{
		IntentID: fid("ee"), RequestID: fid("98"), GenerationID: fid("ea"),
		PredecessorCommit: b0, PredecessorTree: t0, TargetCommit: d1, TargetTree: t1, FinalizationRules: "finalization-rules/1",
	}
	i5 := FinalizationIntended{
		IntentID: fid("ef"), RequestID: fid("9c"), GenerationID: fid("ec"), PredecessorWitnessSeq: 9,
		PredecessorCommit: d1, PredecessorTree: t1, TargetCommit: d5, TargetTree: t5, FinalizationRules: "finalization-rules/1",
	}
	i6 := FinalizationIntended{
		IntentID: fid("f0"), RequestID: fid("9d"), GenerationID: fid("ed"), PredecessorWitnessSeq: 9,
		PredecessorCommit: d1, PredecessorTree: t1, TargetCommit: d6, TargetTree: t6, FinalizationRules: "finalization-rules/1",
	}
	a1 := PromotionAccepted{
		IntentID: fid("ee"), RequestID: fid("98"), GenerationID: fid("ea"),
		PredecessorCommit: b0, PredecessorTree: t0, AcceptedCommit: d1, AcceptedTree: t1, ChangedPaths: []string{"file.txt"},
	}
	a6 := PromotionAccepted{
		IntentID: fid("f0"), RequestID: fid("9d"), GenerationID: fid("ed"), PredecessorWitnessSeq: 9,
		PredecessorCommit: d1, PredecessorTree: t1, AcceptedCommit: d6, AcceptedTree: t6, ChangedPaths: []string{"a.txt", "b/c.txt"},
	}
	return []goldenCommit{
		{header: header("66", 0), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("46"), CommonDir: "C:/fixture/repo-e/.git", ObjectFormat: "sha1"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{finalizationI}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("46"), Revision: 1, CaptureInputsSHA256: finalizationI, PinID: fid("e9")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{finalizationI}, Payload: mustEncode(BodyPin{PinID: fid("e9")})},
		}},
		{header: header("67", 1), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(ScratchCreated{ScratchID: fid("48"), WorkspaceID: fid("46"), ContextID: fid("47"), Dir: "5f6e7d8c", Branch: "refs/heads/dwarf-harness/5f6e7d8c", BaseCommit: b0, AnchorTree: t0})},
		}},
		{header: header("68", 2), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM1}, Payload: mustEncode(r1)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM1}, Payload: mustEncode(BodyPin{PinID: fid("e2")})},
		}},
		{header: header("69", 3), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(g1)},
		}},
		{header: header("6a", 4), drafts: []Draft{
			{Type: TypeFinalizationIntended, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(i1)},
		}},
		{header: header("6b", 5), drafts: []Draft{
			{Type: TypePromotionAccepted, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(a1)},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e2")})},
		}},
		{header: header("6c", 6), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM2}, Payload: mustEncode(r2)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM2}, Payload: mustEncode(BodyPin{PinID: fid("e3")})},
		}},
		{header: header("6d", 7), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(g2)},
		}},
		{header: header("6e", 8), drafts: []Draft{
			{Type: TypePromotionNoChange, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(PromotionNoChange{RequestID: fid("99"), GenerationID: fid("eb"), PredecessorWitnessSeq: 9, PredecessorCommit: d1, PredecessorTree: t1})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e3")})},
		}},
		{header: header("6f", 9), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM3}, Payload: mustEncode(r3)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM3}, Payload: mustEncode(BodyPin{PinID: fid("e4")})},
		}},
		{header: header("75", 10), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM4}, Payload: mustEncode(r4)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM4}, Payload: mustEncode(BodyPin{PinID: fid("e5")})},
		}},
		{header: header("76", 11), drafts: []Draft{
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorOwner, ContextID: fid("47"), Payload: mustEncode(PromotionFailed{RequestID: fid("9a"), Outcome: FailedCancelled})},
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(PromotionFailed{RequestID: fid("9b"), Outcome: FailedDependencyFailed, FailedRequestID: fid("9a")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e4")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e5")})},
		}},
		{header: header("77", 12), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM5}, Payload: mustEncode(r5)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM5}, Payload: mustEncode(BodyPin{PinID: fid("e6")})},
		}},
		{header: header("78", 13), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(g5)},
		}},
		{header: header("79", 14), drafts: []Draft{
			{Type: TypeFinalizationIntended, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(i5)},
		}},
		{header: header("7a", 15), drafts: []Draft{
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(PromotionFailed{RequestID: fid("9c"), Outcome: FailedInterrupted})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e6")})},
		}},
		{header: header("7b", 16), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM6}, Payload: mustEncode(r6)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM6}, Payload: mustEncode(BodyPin{PinID: fid("e7")})},
		}},
		{header: header("7c", 17), drafts: []Draft{
			{Type: TypeGenerationDerived, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(g6)},
		}},
		{header: header("7d", 18), drafts: []Draft{
			{Type: TypeFinalizationIntended, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(i6)},
		}},
		{header: header("7e", 19), drafts: []Draft{
			{Type: TypePromotionAccepted, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(a6)},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e7")})},
		}},
		{header: header("7f", 20), drafts: []Draft{
			{Type: TypePromotionAdmitted, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM7}, Payload: mustEncode(r7)},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("47"), BodyRefs: []Digest{finalizationM7}, Payload: mustEncode(BodyPin{PinID: fid("e8")})},
		}},
		{header: header("80", 21), drafts: []Draft{
			{Type: TypePromotionFailed, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(PromotionFailed{RequestID: fid("9e"), Outcome: FailedInterrupted})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorCore, ContextID: fid("47"), Payload: mustEncode(BodyUnpin{PinID: fid("e8")})},
		}},
	}
}

// goldenRecord locates one record of a golden log.
type goldenRecord struct {
	seq  uint64
	seg  string
	off  int64
	line []byte
}

// goldenBoundary is the end of one golden commit.
type goldenBoundary struct {
	seg   string
	start int64 // offset of the commit's first record in seg
	end   int64 // offset after its last record
	first uint64
	after Chain // the chain position after the commit
}

type goldenLog struct {
	dir     string            // the testdata directory holding the log
	colony  uuid.UUID         // the log's colony
	consume Consumption       // every type version of the log
	inputs  []goldenCommit    // the fixed inputs it encodes
	names   []string          // segment names in order
	files   map[string][]byte // segment name -> bytes
	records []goldenRecord    // in seq order
	commits []goldenBoundary  // in order
}

func (g *goldenLog) record(seq uint64) goldenRecord { return g.records[seq-1] }

// end returns the chain position after the whole golden log.
func (g *goldenLog) end() Chain { return g.commits[len(g.commits)-1].after }

// final returns the name of the final golden segment.
func (g *goldenLog) final() string { return g.names[len(g.names)-1] }

// buildLog encodes inputs with EncodeCommit, the one encoder.
func buildLog(dir string, colony uuid.UUID, consume Consumption, inputs []goldenCommit) (*goldenLog, error) {
	g := &goldenLog{dir: dir, colony: colony, consume: consume, inputs: inputs, files: map[string][]byte{}}
	chain := Genesis()
	var seg string
	for i, c := range inputs {
		if i == 0 || c.newSegment {
			seg = SegmentName(chain.Seq + 1)
			g.names = append(g.names, seg)
		}
		data, next, err := EncodeCommit(chain, c.header, c.drafts)
		if err != nil {
			return nil, err
		}
		start := int64(len(g.files[seg]))
		off := start
		for line := range bytes.Lines(data) {
			g.records = append(g.records, goldenRecord{seq: uint64(len(g.records) + 1), seg: seg, off: off, line: line})
			off += int64(len(line))
		}
		g.files[seg] = append(g.files[seg], data...)
		g.commits = append(g.commits, goldenBoundary{seg: seg, start: start, end: off, first: chain.Seq + 1, after: next})
		chain = next
	}
	return g, nil
}

// allFixtures consumes every fixture type version of the golden log.
var allFixtures = Consumption{"fixture.sample": {1, 2}, "fixture.other": {1}}

// catalogueTypes consumes every type version of the catalogue log.
var catalogueTypes = Consumption{
	"fixture.sample": {1}, TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1}, TypeBodiesCollected: {1},
}

var workspaceTypes = Consumption{
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1},
	TypeScratchCreated: {1}, TypeScratchClosed: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
}

var promotionTypes = Consumption{
	TypePromotionAdmitted: {1}, TypeGenerationDerived: {1}, TypePromotionFailed: {1},
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1}, TypeScratchCreated: {1},
	TypeBodyPin: {1}, TypeBodyUnpin: {1},
}

var scratchChangeTypes = Consumption{
	TypeScratchChanged: {1}, TypeScratchMergeBack: {1},
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1}, TypeScratchCreated: {1}, TypeScratchClosed: {1},
	TypePromotionAdmitted: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
}

var finalizationTypes = Consumption{
	TypeFinalizationIntended: {1}, TypePromotionAccepted: {1}, TypePromotionNoChange: {1},
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1}, TypeScratchCreated: {1},
	TypePromotionAdmitted: {1}, TypeGenerationDerived: {1}, TypePromotionFailed: {1},
	TypeBodyPin: {1}, TypeBodyUnpin: {1},
}

var (
	golden = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(goldenDir, goldenColony, allFixtures, goldenInputs())
	})
	catalogueLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(catalogueDir, catalogueColony, catalogueTypes, catalogueInputs())
	})
	workspaceLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(workspaceDir, workspaceColony, workspaceTypes, workspaceInputs())
	})
	promotionLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(promotionDir, promotionColony, promotionTypes, promotionInputs())
	})
	scratchChangeLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(scratchChangeDir, scratchChangeColony, scratchChangeTypes, scratchChangeInputs())
	})
	finalizationLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(finalizationDir, finalizationColony, finalizationTypes, finalizationInputs())
	})
)

func mustGolden(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, golden)
}

func mustCatalogue(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, catalogueLog)
}

func mustWorkspace(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, workspaceLog)
}

func mustPromotion(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, promotionLog)
}

func mustScratchChange(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, scratchChangeLog)
}

func mustFinalization(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, finalizationLog)
}

func mustBuild(t testing.TB, build func() (*goldenLog, error)) *goldenLog {
	t.Helper()
	g, err := build()
	if err != nil {
		t.Fatalf("encoding the golden inputs: %v", err)
	}
	return g
}

// copyLog copies the testdata log in src into a fresh directory and returns
// the copy's path.
func copyLog(t testing.TB, src string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "log")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshot returns every file under dir by relative path.
func snapshot(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertUnchanged(t testing.TB, dir string, before map[string][]byte) {
	t.Helper()
	if after := snapshot(t, dir); !maps.EqualFunc(before, after, bytes.Equal) {
		t.Errorf("files under %s changed", dir)
	}
}

// appendFile appends data to the file at path.
func appendFile(t testing.TB, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// readAll reads dir to the end with opts and returns the commits, the final
// cursor, and the error that ended the read (nil for io.EOF).
func readAll(dir string, opts ReadOptions) ([]Commit, uint64, error) {
	r, err := OpenReader(dir, opts)
	if err != nil {
		return nil, opts.After, err
	}
	defer r.Close()
	var commits []Commit
	for {
		c, err := r.Next()
		if err == io.EOF {
			return commits, r.Cursor(), nil
		}
		if err != nil {
			return commits, r.Cursor(), err
		}
		commits = append(commits, c)
	}
}

func TestGoldenF0(t *testing.T) {
	for _, tc := range []struct {
		g       *goldenLog
		lastSeq uint64
	}{
		{mustGolden(t), 6},
		{mustCatalogue(t), 7},
	} {
		g := tc.g
		if *update {
			if err := os.MkdirAll(g.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range g.files {
				if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		stored := snapshot(t, g.dir)
		if got, want := slices.Sorted(maps.Keys(stored)), g.names; !slices.Equal(got, want) {
			t.Fatalf("%s files = %v, want %v", g.dir, got, want)
		}
		for _, name := range g.names {
			if !bytes.Equal(stored[name], g.files[name]) {
				t.Errorf("%s/%s differs from the encoding of the golden inputs; run go test -run '^TestGoldenF0$' -update only for an intended format change", g.dir, name)
			}
		}

		res, err := Check(g.dir, g.colony)
		if err != nil {
			t.Fatalf("Check(%s) = %v", g.dir, err)
		}
		want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
		if res != want || res.Last.Seq != tc.lastSeq {
			t.Errorf("Check(%s) = %+v, want %+v with Last.Seq %d", g.dir, res, want, tc.lastSeq)
		}
	}
}

func TestGoldenF2_05(t *testing.T) {
	g := mustWorkspace(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files = %v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s/%s differs from the F2-05 encoding", g.dir, name)
		}
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})
	if err != nil {
		t.Fatal(err)
	}
	inputs := workspaceInputs()
	if len(commits) != len(inputs) {
		t.Fatalf("read %d commits, want %d", len(commits), len(inputs))
	}
	for i, c := range commits {
		if len(c.Records) != len(inputs[i].drafts) {
			t.Fatalf("commit %d: read %d records, want %d", i, len(c.Records), len(inputs[i].drafts))
		}
		for j, r := range c.Records {
			var got payload
			var err error
			switch r.Type {
			case TypeBodyPin, TypeBodyUnpin:
				got, err = decodeF0(r.Type, r.Payload)
			default:
				got, err = decodeF2_05(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: decoding stored %s payload: %v", r.Seq, r.Type, err)
			}
			// Compare the decoded value's canonical encoding to the fixture
			// value, without decoding the expectation through the same code.
			want := inputs[i].drafts[j].Payload
			if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, want) {
				t.Errorf("seq %d: decoded %+v encodes as %s, %v; want %s", r.Seq, got, encoded, err, want)
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want || res.Last.Seq != 12 {
		t.Errorf("Check = %+v, %v; want %+v with Last.Seq 12", res, err, want)
	}
}

// TestGoldenF2_06 pins ticket 06's part of F2, frozen (D118), without
// changing any earlier golden.
func TestGoldenF2_06(t *testing.T) {
	g := mustPromotion(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files = %v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s/%s differs from the F2-06 encoding", g.dir, name)
		}
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: promotionTypes})
	if err != nil {
		t.Fatal(err)
	}
	inputs := promotionInputs()
	if len(commits) != 12 || len(commits) != len(inputs) {
		t.Fatalf("read %d commits, want 12", len(commits))
	}
	for i, c := range commits {
		if len(c.Records) != len(inputs[i].drafts) {
			t.Fatalf("commit %d: read %d records, want %d", i, len(c.Records), len(inputs[i].drafts))
		}
		for j, r := range c.Records {
			var got payload
			var err error
			switch r.Type {
			case TypeBodyPin, TypeBodyUnpin:
				got, err = decodeF0(r.Type, r.Payload)
			case TypeWorkspaceRegistered, TypeCaptureInputsPinned, TypeScratchCreated:
				got, err = decodeF2_05(r.Type, r.Payload)
			default:
				got, err = decodeF2_06(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: decoding stored %s payload: %v", r.Seq, r.Type, err)
			}
			want := inputs[i].drafts[j].Payload
			if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, want) || !bytes.Equal(encoded, r.Payload) {
				t.Errorf("seq %d: decoded %+v encodes as %s, %v; want %s", r.Seq, got, encoded, err, want)
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want || res.Last.Seq != 25 {
		t.Errorf("Check = %+v, %v; want %+v with Last.Seq 25", res, err, want)
	}
}

// TestGoldenF2_07 pins ticket 07's part of F2, frozen (D119), without
// changing any earlier golden.
func TestGoldenF2_07(t *testing.T) {
	t.Parallel()
	g := mustScratchChange(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files = %v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s/%s differs from the F2-07 encoding", g.dir, name)
		}
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: scratchChangeTypes})
	if err != nil {
		t.Fatal(err)
	}
	inputs := scratchChangeInputs()
	if len(commits) != 22 || len(commits) != len(inputs) {
		t.Fatalf("read %d commits, want 22", len(commits))
	}
	for i, c := range commits {
		if len(c.Records) != len(inputs[i].drafts) {
			t.Fatalf("commit %d: read %d records, want %d", i, len(c.Records), len(inputs[i].drafts))
		}
		for j, r := range c.Records {
			var got payload
			var err error
			switch r.Type {
			case TypeBodyPin, TypeBodyUnpin:
				got, err = decodeF0(r.Type, r.Payload)
			case TypeWorkspaceRegistered, TypeCaptureInputsPinned, TypeScratchCreated, TypeScratchClosed:
				got, err = decodeF2_05(r.Type, r.Payload)
			case TypePromotionAdmitted:
				got, err = decodeF2_06(r.Type, r.Payload)
			default:
				got, err = decodeF2_07(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: decoding stored %s payload: %v", r.Seq, r.Type, err)
			}
			want := inputs[i].drafts[j].Payload
			if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, want) || !bytes.Equal(encoded, r.Payload) {
				t.Errorf("seq %d: decoded %+v encodes as %s, %v; want %s", r.Seq, got, encoded, err, want)
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want || res.Last.Seq != 41 {
		t.Errorf("Check = %+v, %v; want %+v with Last.Seq 41", res, err, want)
	}
}

// TestGoldenF2_08 pins ticket 08's part of F2, frozen (D123), without
// changing any earlier golden.
func TestGoldenF2_08(t *testing.T) {
	t.Parallel()
	g := mustFinalization(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files = %v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s/%s differs from the F2-08 encoding", g.dir, name)
		}
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: finalizationTypes})
	if err != nil {
		t.Fatal(err)
	}
	inputs := finalizationInputs()
	if len(commits) != 22 || len(commits) != len(inputs) {
		t.Fatalf("read %d commits, want 22", len(commits))
	}
	for i, c := range commits {
		if len(c.Records) != len(inputs[i].drafts) {
			t.Fatalf("commit %d: read %d records, want %d", i, len(c.Records), len(inputs[i].drafts))
		}
		for j, r := range c.Records {
			var got payload
			var err error
			switch r.Type {
			case TypeBodyPin, TypeBodyUnpin:
				got, err = decodeF0(r.Type, r.Payload)
			case TypeWorkspaceRegistered, TypeCaptureInputsPinned, TypeScratchCreated:
				got, err = decodeF2_05(r.Type, r.Payload)
			case TypePromotionAdmitted, TypeGenerationDerived, TypePromotionFailed:
				got, err = decodeF2_06(r.Type, r.Payload)
			default:
				got, err = decodeF2_08(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: decoding stored %s payload: %v", r.Seq, r.Type, err)
			}
			want := inputs[i].drafts[j].Payload
			if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, want) || !bytes.Equal(encoded, r.Payload) {
				t.Errorf("seq %d: decoded %+v encodes as %s, %v; want %s", r.Seq, got, encoded, err, want)
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want || res.Last.Seq != 39 {
		t.Errorf("Check = %+v, %v; want %+v with Last.Seq 39", res, err, want)
	}
}

func TestGoldenDecodes(t *testing.T) {
	for _, tc := range []struct {
		g *goldenLog
		// payloads are the stored payloads by seq, with json.Marshal's
		// compaction and HTML escaping; nil when they equal the inputs'.
		payloads []string
	}{
		{mustGolden(t), []string{
			`{"n":1}`,
			`{"n":2,"text":"dwarf ⚒ \u003c\u0026\u003e"}`,
			`{}`,
			`{"n":4,"note":"v2"}`,
			`{}`,
			`{"n":6}`,
		}},
		{mustCatalogue(t), nil},
		{mustWorkspace(t), nil},
		{mustPromotion(t), nil},
		{mustScratchChange(t), nil},
		{mustFinalization(t), nil},
	} {
		g := tc.g
		commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})
		if err != nil {
			t.Fatalf("reading %s: %v", g.dir, err)
		}
		if want := g.end().Seq; cursor != want {
			t.Errorf("%s: Cursor() = %d, want %d", g.dir, cursor, want)
		}

		var want []Commit
		chain := Genesis()
		for _, in := range g.inputs {
			c := Commit{ID: in.header.CommitID, First: chain.Seq + 1, Last: chain.Seq + uint64(len(in.drafts))}
			for i, d := range in.drafts {
				seq := chain.Seq + 1
				rec := g.record(seq)
				js := rec.line[65 : len(rec.line)-1]
				if tc.payloads != nil {
					d.Payload = json.RawMessage(tc.payloads[seq-1])
				}
				c.Records = append(c.Records, Record{
					Envelope: Envelope{
						Format:      1,
						Seq:         seq,
						CommitID:    in.header.CommitID,
						CommitIndex: i,
						CommitCount: len(in.drafts),
						PrevHash:    chain.Hash,
						EventTimeNS: in.header.EventTimeNS,
						ClockHeld:   in.header.ClockHeld,
						ColonyID:    g.colony,
						Draft:       d,
					},
					Hash: string(rec.line[:64]),
					JSON: js,
				})
				chain = Chain{Seq: seq, Hash: string(rec.line[:64]), EventTimeNS: in.header.EventTimeNS}
			}
			want = append(want, c)
		}
		if !reflect.DeepEqual(commits, want) {
			t.Fatalf("%s commits:\n got %+v\nwant %+v", g.dir, commits, want)
		}
	}

	// Body references decode as listed, and an absent body_refs as nil.
	commits, _, err := readAll(catalogueDir, ReadOptions{Colony: catalogueColony, Through: ToEnd, Consume: catalogueTypes})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[uint64]int{1: 3, 2: 1, 4: 1}
	for _, c := range commits {
		for _, r := range c.Records {
			if n, ok := refs[r.Seq]; ok && len(r.BodyRefs) != n || !ok && r.BodyRefs != nil {
				t.Errorf("seq %d: BodyRefs %v, want %d digests (nil for none)", r.Seq, r.BodyRefs, n)
			}
		}
	}
}

// errorsAsCorrupt returns err's *CorruptError, failing the test if there is
// none.
func errorsAsCorrupt(t testing.TB, err error) *CorruptError {
	t.Helper()
	var ce *CorruptError
	if !errors.As(err, &ce) || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want a *CorruptError matching ErrCorrupt", err)
	}
	return ce
}
