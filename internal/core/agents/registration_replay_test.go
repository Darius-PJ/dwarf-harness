package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func registrationGoldenID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1390-0000-7000-8000-%012x", n))
}

func registrationGoldenAddress() ledger.Resource {
	return ledger.Resource{Kind: "external", ResourceKey: "paid-route/" + registrationGoldenID(0x450).String()}
}

func openRegistrationFixture(t testing.TB, prefix int) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	f, commits := scopedGoldenFixture(t, "139", prefix)
	limits := DefaultLimits()
	limits.MaxRegistrations, limits.MaxDeclarations = 1, 1
	var report Reconciliation
	var err error
	before := f.store.Durable()
	f.service, report, err = openService(context.Background(), f.store, f.root, limits, serviceDeps{
		reviewClock:  func() (int64, error) { t.Error("registration replay/query sampled a clock"); return 0, ErrInvalid },
		beforeRead:   func(Binding, string) error { t.Error("registration query read private content"); return ErrInvalid },
		beforeSubmit: func(d []ledger.Draft) []ledger.Draft { t.Error("registration query/replay appended"); return d },
	})
	if err != nil || len(report.Issues) != 0 || f.store.Durable() != before {
		t.Fatalf("registration golden Open: %+v %v durable=%d/%d", report, err, f.store.Durable(), before)
	}
	return f, commits
}

func retryRegistration(ctx context.Context, s *Service, r ledger.RuntimeRegistrationReceipt) (RegistrationReceipt, error) {
	origin := bindingOf(r.Submission.Origin)
	switch a := r.Submission.Arguments.(type) {
	case ledger.ResourceRegistrationArguments:
		return s.SetResourceRegistration(ctx, origin, ResourceRegistration{CommandID: r.CommandID, ResourceID: a.ResourceID, ExpectedVersion: uint64(a.ExpectedVersion), Resource: a.Resource, Audience: a.Audience})
	case ledger.WorkDomainRegistrationArguments:
		return s.SetWorkDomainRegistration(ctx, origin, WorkDomainRegistration{CommandID: r.CommandID, WorkDomainID: a.WorkDomainID, ExpectedVersion: uint64(a.ExpectedVersion), Audience: a.Audience})
	case ledger.DeclarationArguments:
		return s.SetDeclaration(ctx, origin, DeclarationChange{CommandID: r.CommandID, DeclarationID: a.DeclarationID, ExpectedVersion: uint64(a.ExpectedVersion), Subject: a.Subject, Audience: a.Audience, Consumers: a.Consumers})
	default:
		return RegistrationReceipt{}, ErrInvalid
	}
}

// Expectations are complete frozen snapshots, not a second state reducer.
func assertRegistrationPrefix(t *testing.T, s *Service, commits []ledger.Commit, prefix int, cursor uint64) {
	t.Helper()
	ctx := context.Background()
	through := cursor
	if cursor == ledger.ToEnd {
		through = s.store.Durable()
	}
	resources := map[uuid.UUID]ResourceRegistrationView{}
	domains := map[uuid.UUID]WorkDomainRegistrationView{}
	declarations := map[uuid.UUID]DeclarationView{}
	for _, c := range commits[:prefix] {
		for _, r := range c.Records {
			origin := ledger.Origin{AgentID: r.AgentID, SessionID: r.ExecutionSessionID, ContextID: r.ContextID}
			switch r.Type {
			case ledger.TypeRuntimeResourceRegistered:
				p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, r.Payload)
				resources[p.ResourceID] = ResourceRegistrationView{Record: p, Origin: origin, EffectSequence: r.Seq, ReceiptSequence: c.Last, ThroughSequence: through}
			case ledger.TypeRuntimeWorkDomainRegistered:
				p := scopedDecode(t, ledger.DecodeRuntimeWorkDomainRegistered, r.Payload)
				domains[p.WorkDomainID] = WorkDomainRegistrationView{Record: p, Origin: origin, EffectSequence: r.Seq, ReceiptSequence: c.Last, ThroughSequence: through}
			case ledger.TypeRuntimeDeclarationChanged:
				p := scopedDecode(t, ledger.DecodeRuntimeDeclarationChanged, r.Payload)
				declarations[p.DeclarationID] = DeclarationView{Record: p, Origin: origin, EffectSequence: r.Seq, ReceiptSequence: c.Last, ThroughSequence: through}
			}
		}
	}
	resource, err := s.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: registrationGoldenAddress(), ThroughSequence: cursor})
	if want, found := resources[registrationGoldenID(0x400)]; found {
		if err != nil || !reflect.DeepEqual(resource, want) {
			t.Fatalf("prefix %d resource: %+v want %+v: %v", prefix, resource, want, err)
		}
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("prefix %d borrowed future resource: %+v %v", prefix, resource, err)
	}
	domain, err := s.WorkDomainStatus(ctx, RegistrationLookup{ID: registrationGoldenID(0x401), ThroughSequence: cursor})
	if want, found := domains[registrationGoldenID(0x401)]; found {
		if err != nil || !reflect.DeepEqual(domain, want) {
			t.Fatalf("prefix %d domain: %+v want %+v: %v", prefix, domain, want, err)
		}
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("prefix %d borrowed future domain: %+v %v", prefix, domain, err)
	}
	for _, id := range []int{0x500, 0x501, 0x502, 0x503} {
		view, err := s.DeclarationStatus(ctx, RegistrationLookup{ID: registrationGoldenID(id), ThroughSequence: cursor})
		if want, found := declarations[registrationGoldenID(id)]; found {
			if err != nil || !reflect.DeepEqual(view, want) {
				t.Fatalf("prefix %d declaration %x: %+v want %+v: %v", prefix, id, view, want, err)
			}
			evidence, err := s.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: view.Record.Subject, ThroughSequence: cursor})
			if err != nil {
				t.Fatalf("pinned source unavailable: %v", err)
			}
			if evidence.Resource != nil && evidence.Resource.Record.Version != view.Record.Subject.Version || evidence.WorkDomain != nil && evidence.WorkDomain.Record.Version != view.Record.Subject.Version {
				t.Fatalf("pinned source borrowed another version: %+v", evidence)
			}
		} else if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("prefix %d borrowed future declaration %x: %+v %v", prefix, id, view, err)
		}
	}
}

func TestRegistrationFrozenGoldenReplay(t *testing.T) {
	t.Parallel()
	for prefix := range 20 {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := openRegistrationFixture(t, prefix)
			before := f.store.Durable()
			var original []byte
			if prefix != 0 {
				original = readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))
			}
			assertRegistrationPrefix(t, f.service, commits, prefix, ledger.ToEnd)
			for _, c := range commits[:prefix] {
				if c.Records[len(c.Records)-1].Type != ledger.TypeRuntimeRegistrationReceipt {
					continue
				}
				r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, c.Records[2].Payload)
				got, err := f.service.RegistrationCommandStatus(ctx, RegistrationCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
				want := RegistrationReceipt{RuntimeRegistrationReceipt: r, EffectSequence: c.First + 1, Sequence: c.Last}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("original frozen receipt: %+v want %+v: %v", got, want, err)
				}
				got, err = retryRegistration(ctx, f.service, r)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("frozen retry after source/CAS/limit change: %+v %v", got, err)
				}
			}
			if prefix == 19 {
				assertRegistrationPrefix(t, f.service, commits, 0, 0)
				for i, c := range commits {
					assertRegistrationPrefix(t, f.service, commits, i+1, c.Last)
				}
				for _, row := range []struct {
					ref    ledger.RegistrationRef
					before uint64
				}{
					{ledger.RegistrationRef{Kind: "resource", ID: registrationGoldenID(0x400), Version: 2}, 41},
					{ledger.RegistrationRef{Kind: "resource", ID: registrationGoldenID(0x400), Version: 3}, 59},
					{ledger.RegistrationRef{Kind: "work_domain", ID: registrationGoldenID(0x401), Version: 2}, 44},
				} {
					if got, err := f.service.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: row.ref, ThroughSequence: row.before}); !errors.Is(err, ErrUnavailable) {
						t.Fatalf("exact source borrowed future snapshot: %+v %v", got, err)
					}
				}
				for _, cursor := range []uint64{1, 24, 25, 64, 66} {
					for _, query := range []func() error{
						func() error {
							_, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: registrationGoldenAddress(), ThroughSequence: cursor})
							return err
						},
						func() error {
							_, err := f.service.WorkDomainStatus(ctx, RegistrationLookup{ID: registrationGoldenID(0x401), ThroughSequence: cursor})
							return err
						},
						func() error {
							_, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: registrationGoldenID(0x500), ThroughSequence: cursor})
							return err
						},
						func() error {
							_, err := f.service.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: ledger.RegistrationRef{Kind: "resource", ID: registrationGoldenID(0x400), Version: 1}, ThroughSequence: cursor})
							return err
						},
					} {
						if err := query(); !errors.Is(err, ledger.ErrCursor) {
							t.Fatalf("cursor %d: %v", cursor, err)
						}
					}
				}
			}
			if f.store.Durable() != before || prefix != 0 && !bytes.Equal(original, readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))) {
				t.Fatal("registration replay/query/retry changed ledger")
			}
		})
	}
}

func rewriteRegistrationReceipt(t *testing.T, d []ledger.Draft, receipt ledger.RuntimeRegistrationReceipt) {
	t.Helper()
	receipt.SubmissionSHA256 = ledger.Digest(sha256.Sum256(testPayload(t, receipt.Submission)))
	d[0].Payload = testPayload(t, ledger.Command{CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256})
	d[2].Payload = testPayload(t, receipt)
}

func TestRegistrationOriginalRequestPreflight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	b := registerPrivate(t, f.service, 200)
	r := registrationRequest(a, 300, 400, "original")
	var changed []ledger.Draft
	f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
		p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
		p.Audience = []uuid.UUID{b.AgentID}
		d[1].Payload = testPayload(t, p)
		receipt := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
		args := receipt.Submission.Arguments.(ledger.ResourceRegistrationArguments)
		args.Audience = p.Audience
		receipt.Submission.Arguments = args
		rewriteRegistrationReceipt(t, d, receipt)
		changed = d
		return d
	}
	before := f.store.Durable()
	if _, err := f.service.SetResourceRegistration(ctx, a, r); !errors.Is(err, ErrInconsistent) || f.store.Durable() != before {
		t.Fatalf("self-consistent mutation escaped original request: %v", err)
	}
	// The attack is valid on its own: only the original-request binding can
	// refuse it. A malformed codec or unrelated state guard is not this proof.
	_, _, _, _, g, _, err := decodeFamily(changed, before+1)
	if err != nil || g == nil {
		t.Fatalf("mutation was not codec-valid: %v", err)
	}
	f.service.mu.Lock()
	_, err = f.service.planRegistration(ctx, g, before+1)
	f.service.mu.Unlock()
	if err != nil {
		t.Fatalf("mutation did not reach original-request guard: %v", err)
	}
	if _, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("preflight leaked projection: %v", err)
	}
	f.service.deps.beforeSubmit = nil
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatalf("rejection consumed command or entity ID: %v", err)
	}
}

func TestRegistrationGroupRules(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		prefix int
		mutate func(*testing.T, []ledger.Draft) []ledger.Draft
	}{
		{"missing-effect", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft { return append(d[:1], d[2:]...) }},
		{"extra-effect", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft { return append(d, d[1]) }},
		{"reordered", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft { d[1], d[2] = d[2], d[1]; return d }},
		{"mixed-family", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			d[1].Type = ledger.TypeRuntimeScopedReceipt
			return d
		}},
		{"agent-envelope", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			for i := range d {
				d[i].Actor = ledger.ActorAgent
			}
			return d
		}},
		{"session", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			r.Submission.Origin.SessionID = registrationGoldenID(0x999)
			for i := range d {
				d[i].ExecutionSessionID = r.Submission.Origin.SessionID
			}
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"unknown-success", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			r.Outcome = "future_success"
			d[2].Payload = testPayload(t, r)
			return d
		}},
		{"effect-request-disagreement", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
			p.Audience = p.Audience[:1]
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{"unregistered-audience", 5, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
			p.Audience = []uuid.UUID{registrationGoldenID(0x999)}
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.ResourceRegistrationArguments)
			a.Audience = p.Audience
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"immutable-address", 11, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
			p.Resource.ResourceKey = "changed"
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.ResourceRegistrationArguments)
			a.Resource = p.Resource
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"stale-source", 13, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeDeclarationChanged, d[1].Payload)
			p.Subject.Version = 1
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.DeclarationArguments)
			a.Subject = p.Subject
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"address-collision", 11, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
			p.ResourceID, p.ExpectedVersion, p.Version = registrationGoldenID(0x499), 0, 1
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.ResourceRegistrationArguments)
			a.ResourceID, a.ExpectedVersion, r.Version = p.ResourceID, 0, 1
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"stale-cas", 11, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeResourceRegistered, d[1].Payload)
			p.ExpectedVersion, p.Version = 0, 1
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.ResourceRegistrationArguments)
			a.ExpectedVersion, r.Version = 0, 1
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"context-outside-audience", 13, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeDeclarationChanged, d[1].Payload)
			p.Consumers = []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding{AgentID: registrationGoldenID(0x200), ContextID: registrationGoldenID(0x202)}}}
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.DeclarationArguments)
			a.Consumers = p.Consumers
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
		{"source-audience", 13, func(t *testing.T, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeDeclarationChanged, d[1].Payload)
			p.Audience = []uuid.UUID{registrationGoldenID(0x200)}
			p.Consumers = []ledger.DeclarationConsumer{}
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeRegistrationReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.DeclarationArguments)
			a.Audience, a.Consumers = p.Audience, p.Consumers
			r.Submission.Arguments = a
			rewriteRegistrationReceipt(t, d, r)
			return d
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := openRegistrationFixture(t, row.prefix)
			before := f.store.Durable()
			d := row.mutate(t, scopedGoldenDrafts(commits[row.prefix]))
			_, _, _, _, g, _, err := decodeFamily(d, before+1)
			if err == nil && g != nil {
				_, err = f.service.planRegistration(ctx, g, before+1)
			}
			if err == nil {
				t.Fatal("invalid complete group passed preflight planner")
			}
			assertRegistrationPrefix(t, f.service, commits, row.prefix, ledger.ToEnd)
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			if _, err := f.store.Submit(ctx, d[0], d[1:]); err != nil {
				t.Fatalf("semantic rejection fixture did not reach Core replay: %v", err)
			}
			log := readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			service, _, err := Open(ctx, f.store, f.root, DefaultLimits())
			if service != nil {
				_ = service.Close()
			}
			if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("invalid committed registration was not inconsistency: %v", err)
			}
			if !bytes.Equal(log, readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))) {
				t.Fatal("failed replay modified committed ledger")
			}
		})
	}
}

func TestRegistrationReviewTerminalConsumer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _ := openReviewFixture(t, -1)
	task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: reviewGoldenID(0x200)})
	if err != nil || task.Task.State != "completed" {
		t.Fatalf("review-derived terminal fixture: %+v %v", task, err)
	}
	origin := bindingOf(task.Task.Creator)
	r := registrationRequest(origin, 300, 400, "terminal-review")
	r.Audience = task.Task.Audience
	if _, err := f.service.SetResourceRegistration(ctx, origin, r); err != nil {
		t.Fatal(err)
	}
	d := DeclarationChange{CommandID: privateID(301), DeclarationID: privateID(401), Subject: ledger.RegistrationRef{Kind: "resource", ID: r.ResourceID, Version: 1}, Audience: r.Audience, Consumers: []ledger.DeclarationConsumer{{Kind: "task", TaskID: task.Task.TaskID}}}
	if _, err := f.service.SetDeclaration(ctx, origin, d); err != nil {
		t.Fatalf("live-map absence hid review terminal task: %v", err)
	}
	reopenScoped(t, f, DefaultLimits())
	view, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: d.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || !reflect.DeepEqual(view.Record.Consumers, d.Consumers) {
		t.Fatalf("review terminal declaration not replayable: %+v %v", view, err)
	}
}

// Prepare immutable real files once for the fuzz target, not for every rejected
// byte string. Accepted candidates alone pay for a copied store and Open.
func preparedRegistrationFixture(t testing.TB, prefix int) (*serviceFixture, []ledger.Commit, string) {
	t.Helper()
	f, commits := openRegistrationFixture(t, prefix)
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	f.service = nil
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store = nil
	prepared := testRoot(t)
	if err := os.CopyFS(prepared, os.DirFS(f.root)); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits, prepared
}

func TestRegistrationUnsupportedVersion(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name           string
		prefix, effect int
	}{
		{"resource", 5, 1}, {"domain", 6, 1}, {"declaration", 7, 1}, {"receipt", 5, 2},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, commits := openRegistrationFixture(t, row.prefix)
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			d := scopedGoldenDrafts(commits[row.prefix])
			d[row.effect].TypeVersion = 2
			reviewFutureCatalogue(t, f, commits, d)
			if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
				t.Fatal(err)
			}
			before := f.store.Durable()
			service, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if service != nil {
				_ = service.Close()
			}
			var unsupported *ledger.UnsupportedVersionError
			if !errors.As(err, &unsupported) || f.store.Durable() != before {
				t.Fatalf("future registration version was interpreted or recovery appended: %v", err)
			}
		})
	}
}

func TestRegistrationPostcommitReadbackFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	r := registrationRequest(a, 300, 400, "readback")
	segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	var saved []byte
	defer func() {
		if saved != nil {
			writeTestFile(t, segment, saved)
		}
	}()
	f.service.deps.afterScopedSubmit = func() {
		saved = readTestFile(t, segment)
		broken := bytes.Clone(saved)
		broken[len(broken)-2] = '!'
		writeTestFile(t, segment, broken)
	}
	before := f.store.Durable()
	if receipt, err := f.service.SetResourceRegistration(ctx, a, r); err == nil || receipt.Sequence != 0 || f.store.Durable() != before+3 {
		t.Fatalf("unestablished committed result was acknowledged: %+v %v", receipt, err)
	}
	writeTestFile(t, segment, saved)
	saved = nil
	if _, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd}); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("readback failure left service queryable: %v", err)
	}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("readback failure allowed another write: %v", err)
	}
	reopenScoped(t, f, DefaultLimits())
	receipt, err := f.service.SetResourceRegistration(ctx, a, r)
	if err != nil || receipt.Version != 1 || receipt.Sequence != before+3 || f.store.Durable() != before+3 {
		t.Fatalf("restart did not recover the actual commit: %+v %v", receipt, err)
	}
}
