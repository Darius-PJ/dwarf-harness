package agents

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func registrationRequest(origin Binding, command, id int, key string) ResourceRegistration {
	return ResourceRegistration{CommandID: privateID(command), ResourceID: privateID(id), Resource: ledger.Resource{Kind: "external", ResourceKey: key}, Audience: []uuid.UUID{origin.AgentID}}
}

func TestRegistrationAuthorityAndIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	b := registerPrivate(t, f.service, 200)
	r := registrationRequest(a, 300, 400, " Shared/Pool ")
	r.Audience = []uuid.UUID{b.AgentID, a.AgentID}
	original := slices.Clone(r.Audience)
	before := f.store.Durable()
	for _, actor := range []string{"agent", "core"} {
		if _, err := f.service.executeRegistration(ctx, scopedCaller{actor: actor, binding: a}, "resource.set", r); !errors.Is(err, ErrDenied) {
			t.Fatalf("non-owner registration: %v", err)
		}
	}
	for _, binding := range []Binding{{a.AgentID, b.ContextID}, {privateID(999), a.ContextID}} {
		if _, err := f.service.SetResourceRegistration(ctx, binding, r); !errors.Is(err, ErrNotBound) {
			t.Fatalf("forged origin: %v", err)
		}
	}
	agent, err := f.service.BindAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.command(ctx, "resource.set", r); err == nil || f.store.Durable() != before {
		t.Fatalf("alternate agent route published registration: %v", err)
	}
	receipt, err := f.service.SetResourceRegistration(ctx, a, r)
	if err != nil || receipt.Version != 1 || receipt.EffectSequence != before+2 || receipt.Sequence != before+3 || !slices.Equal(r.Audience, original) {
		t.Fatalf("owner registration or input ownership: %+v %v", receipt, err)
	}
	view, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd})
	if err != nil || view.Record.ResourceID != r.ResourceID || view.Record.Resource.ResourceKey != r.Resource.ResourceKey || !slices.Equal(view.Record.Audience, []uuid.UUID{a.AgentID, b.AgentID}) {
		t.Fatalf("exact opaque address: %+v %v", view, err)
	}
	for i, key := range []string{privateID(777).String(), "paid-route/" + privateID(778).String(), "shared/pool"} {
		request := registrationRequest(a, 310+i, 410+i, key)
		if _, err := f.service.SetResourceRegistration(ctx, a, request); err != nil {
			t.Fatalf("opaque key %q: %v", key, err)
		}
		if got, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: request.Resource, ThroughSequence: ledger.ToEnd}); err != nil || got.Record.ResourceID != request.ResourceID {
			t.Fatalf("exact lookup %q: %+v %v", key, got, err)
		}
	}
	if _, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: ledger.Resource{Kind: "external", ResourceKey: "Shared/Pool"}, ThroughSequence: ledger.ToEnd}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("address was trimmed or aliased: %v", err)
	}
	before = f.store.Durable()
	collision := r
	collision.CommandID, collision.ResourceID = privateID(320), privateID(420)
	if _, err := f.service.SetResourceRegistration(ctx, a, collision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("address collision: %v", err)
	}
	retarget := r
	retarget.CommandID, retarget.ExpectedVersion, retarget.Resource.ResourceKey = privateID(321), 1, "new-address"
	if _, err := f.service.SetResourceRegistration(ctx, a, retarget); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("address retarget: %v", err)
	}
	// The same UUID is independently valid in all three entity namespaces.
	if _, err := f.service.SetWorkDomainRegistration(ctx, a, WorkDomainRegistration{CommandID: privateID(322), WorkDomainID: r.ResourceID, Audience: []uuid.UUID{a.AgentID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetDeclaration(ctx, a, DeclarationChange{CommandID: privateID(323), DeclarationID: r.ResourceID, Subject: ledger.RegistrationRef{Kind: "resource", ID: r.ResourceID, Version: 1}, Audience: []uuid.UUID{a.AgentID}}); err != nil {
		t.Fatal(err)
	}
	declaration, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: r.ResourceID, ThroughSequence: ledger.ToEnd})
	if err != nil || declaration.Record.Consumers == nil || len(declaration.Record.Consumers) != 0 {
		t.Fatalf("nil consumer normalization: %+v %v", declaration, err)
	}
	if _, err := f.service.SetResourceRegistration(ctx, b, r); !errors.Is(err, ErrDenied) {
		t.Fatalf("same command changed principal origin: %v", err)
	}
	changed := r
	changed.Audience = []uuid.UUID{a.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, changed); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("same command changed digest: %v", err)
	}
	if _, err := f.service.SetWorkDomainRegistration(ctx, a, WorkDomainRegistration{CommandID: r.CommandID, WorkDomainID: privateID(430), Audience: []uuid.UUID{a.AgentID}}); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("same command changed operation: %v", err)
	}
	oldFamily := r
	oldFamily.CommandID = privateID(103)
	if _, err := f.service.SetResourceRegistration(ctx, a, oldFamily); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("private command reuse: %v", err)
	}
	if _, err := agent.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256}); err == nil {
		t.Fatal("agent disclosed owner registration receipt")
	}
	view.Record.Audience[0] = privateID(999)
	receipt.Submission.Arguments.(ledger.ResourceRegistrationArguments).Audience[0] = privateID(999)
	fresh, err := f.service.RegistrationCommandStatus(ctx, RegistrationCommandLookup{CommandID: r.CommandID, SubmissionSHA256: receipt.SubmissionSHA256})
	if err != nil || fresh.Submission.Arguments.(ledger.ResourceRegistrationArguments).Audience[0] != a.AgentID {
		t.Fatalf("caller changed receipt history: %+v %v", fresh, err)
	}
	view, err = f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd})
	if err != nil || view.Record.Audience[0] != a.AgentID {
		t.Fatalf("caller changed current projection: %+v %v", view, err)
	}
}

func TestRegistrationLimitsAndRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	limits := DefaultLimits()
	limits.MaxRegistrations, limits.MaxDeclarations = 2, 1
	f := newServiceFixtureWithLimits(t, serviceDeps{}, limits)
	a := registerPrivate(t, f.service, 100)
	r := registrationRequest(a, 300, 400, "bounded")
	first, err := f.service.SetResourceRegistration(ctx, a, r)
	if err != nil {
		t.Fatal(err)
	}
	d := WorkDomainRegistration{CommandID: privateID(301), WorkDomainID: privateID(401), Audience: r.Audience}
	if _, err := f.service.SetWorkDomainRegistration(ctx, a, d); err != nil {
		t.Fatal(err)
	}
	next := registrationRequest(a, 302, 402, "overflow")
	var busy *BusyError
	before := f.store.Durable()
	if _, err := f.service.SetResourceRegistration(ctx, a, next); !errors.As(err, &busy) || f.store.Durable() != before {
		t.Fatalf("combined registration population: %v", err)
	}
	revision := r
	revision.CommandID, revision.ExpectedVersion = privateID(303), 1
	if got, err := f.service.SetResourceRegistration(ctx, a, revision); err != nil || got.Version != 2 {
		t.Fatalf("revision incorrectly needed a slot: %+v %v", got, err)
	}
	decl := DeclarationChange{CommandID: privateID(304), DeclarationID: privateID(404), Subject: ledger.RegistrationRef{Kind: "resource", ID: r.ResourceID, Version: 2}, Audience: r.Audience, Consumers: []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding(a)}}}
	declFirst, err := f.service.SetDeclaration(ctx, a, decl)
	if err != nil {
		t.Fatal(err)
	}
	withdraw := decl
	withdraw.CommandID, withdraw.ExpectedVersion, withdraw.Consumers = privateID(305), 1, nil
	if got, err := f.service.SetDeclaration(ctx, a, withdraw); err != nil || got.Version != 2 {
		t.Fatalf("withdrawal: %+v %v", got, err)
	}
	another := decl
	another.CommandID, another.DeclarationID = privateID(306), privateID(406)
	before = f.store.Durable()
	if _, err := f.service.SetDeclaration(ctx, a, another); !errors.As(err, &busy) || f.store.Durable() != before {
		t.Fatalf("withdrawal freed identity capacity: %v", err)
	}
	limits.MaxRegistrations = 1
	reopenScoped(t, f, limits)
	if got, err := f.service.SetResourceRegistration(ctx, a, r); err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("retry after CAS and lowered population: %+v %v", got, err)
	}
	if got, err := f.service.SetDeclaration(ctx, a, decl); err != nil || !reflect.DeepEqual(got, declFirst) {
		t.Fatalf("retry after withdrawal: %+v %v", got, err)
	}
	revision.CommandID, revision.ExpectedVersion = privateID(307), 2
	if _, err := f.service.SetResourceRegistration(ctx, a, revision); err != nil {
		t.Fatal(err)
	}
	before = f.store.Durable()
	if got, err := f.service.SetDeclaration(ctx, a, decl); err != nil || !reflect.DeepEqual(got, declFirst) || f.store.Durable() != before {
		t.Fatalf("retry checked current source instead of original command: %+v %v", got, err)
	}
	for _, version := range []uint64{math.MaxInt64, math.MaxUint64} {
		revision.ExpectedVersion = version
		d.ExpectedVersion, d.CommandID = version, privateID(308)
		withdraw.ExpectedVersion, withdraw.CommandID = version, privateID(309)
		for _, call := range []func() (RegistrationReceipt, error){
			func() (RegistrationReceipt, error) { return f.service.SetResourceRegistration(ctx, a, revision) },
			func() (RegistrationReceipt, error) { return f.service.SetWorkDomainRegistration(ctx, a, d) },
			func() (RegistrationReceipt, error) { return f.service.SetDeclaration(ctx, a, withdraw) },
		} {
			if _, err := call(); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
				t.Fatalf("unchecked version conversion/increment: %v", err)
			}
		}
	}
}

func TestRegistrationNormalizationAndSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	r := registrationRequest(a, 300, 400, "bounded")
	before := f.store.Durable()
	for _, audience := range [][]uuid.UUID{nil, {a.AgentID, a.AgentID}, {privateID(999)}} {
		r.Audience = audience
		_, err := f.service.SetResourceRegistration(ctx, a, r)
		want := ErrInvalid
		if len(audience) == 1 {
			want = ErrNotBound
		}
		if !errors.Is(err, want) || f.store.Durable() != before {
			t.Fatalf("invalid audience committed: %v", err)
		}
	}
	r.Audience = make([]uuid.UUID, ledger.MaxRegistrationPayloadBytes+1)
	if _, err := f.service.SetResourceRegistration(ctx, a, r); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("allocation fence: %v", err)
	}
	// Valid distinct identities fit the allocation fence but exceed encoded size.
	r.Audience = make([]uuid.UUID, 2000)
	for i := range r.Audience {
		r.Audience[i] = privateID(1000 + i)
	}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
		t.Fatalf("encoded submission cap: %v", err)
	}
	// Find the whole-receipt boundary with valid canonical metadata. It must
	// refuse even when the embedded submission itself still fits.
	ids := submissionIDs{a.AgentID, privateID(101), a.ContextID}
	var boundary registrationInput
	for n := 1600; n < 1800; n++ {
		r.Audience = make([]uuid.UUID, n)
		for i := range r.Audience {
			r.Audience[i] = privateID(1000 + i)
		}
		input, err := makeRegistrationInput(ids, "resource.set", r)
		if err != nil {
			continue
		}
		p := ledger.RuntimeResourceRegistered{ResourceID: r.ResourceID, Version: 1, Resource: r.Resource, Audience: r.Audience}
		_, err = registrationDrafts(input, registrationPlan{resource: &ResourceRegistrationView{Record: p}})
		if errors.Is(err, ErrTooLarge) {
			boundary = input
			break
		}
	}
	if boundary.id == (uuid.UUID{}) {
		t.Fatal("did not reach a whole-receipt-only overflow")
	}
	if _, err := boundary.sub.Encode(); err != nil {
		t.Fatalf("boundary did not fit submission: %v", err)
	}
}

func TestRegistrationVersionCAS(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"resource", "work_domain", "declaration"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			a := registerPrivate(t, f.service, 100)
			r := registrationRequest(a, 300, 400, "cas")
			if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
				t.Fatal(err)
			}
			d := WorkDomainRegistration{CommandID: privateID(301), WorkDomainID: privateID(401), Audience: r.Audience}
			if _, err := f.service.SetWorkDomainRegistration(ctx, a, d); err != nil {
				t.Fatal(err)
			}
			decl := DeclarationChange{CommandID: privateID(302), DeclarationID: privateID(402), Subject: ledger.RegistrationRef{Kind: "resource", ID: r.ResourceID, Version: 1}, Audience: r.Audience}
			if _, err := f.service.SetDeclaration(ctx, a, decl); err != nil {
				t.Fatal(err)
			}
			arrived, release := make(chan struct{}, 2), make(chan struct{})
			f.service.deps.beforeLookup = func() { arrived <- struct{}{}; <-release }
			type result struct {
				receipt RegistrationReceipt
				err     error
			}
			results := make(chan result, 2)
			before := f.store.Durable()
			for i := range 2 {
				go func(i int) {
					var got RegistrationReceipt
					var err error
					switch kind {
					case "resource":
						request := r
						request.CommandID, request.ExpectedVersion = privateID(310+i), 1
						got, err = f.service.SetResourceRegistration(ctx, a, request)
					case "work_domain":
						request := d
						request.CommandID, request.ExpectedVersion = privateID(310+i), 1
						got, err = f.service.SetWorkDomainRegistration(ctx, a, request)
					case "declaration":
						request := decl
						request.CommandID, request.ExpectedVersion = privateID(310+i), 1
						got, err = f.service.SetDeclaration(ctx, a, request)
					}
					results <- result{got, err}
				}(i)
			}
			<-arrived
			<-arrived
			close(release)
			wins, conflicts := 0, 0
			var winner RegistrationReceipt
			for range 2 {
				got := <-results
				if got.err == nil {
					wins++
					winner = got.receipt
				} else if errors.Is(got.err, ErrRevision) {
					conflicts++
				} else {
					t.Fatal(got.err)
				}
			}
			if wins != 1 || conflicts != 1 || winner.Version != 2 || winner.Sequence != before+3 || f.store.Durable() != winner.Sequence {
				t.Fatalf("CAS lost linearization: wins=%d conflicts=%d receipt=%+v", wins, conflicts, winner)
			}
			reopenScoped(t, f, DefaultLimits())
			got, err := f.service.RegistrationCommandStatus(ctx, RegistrationCommandLookup{CommandID: winner.CommandID, SubmissionSHA256: winner.SubmissionSHA256})
			if err != nil || !reflect.DeepEqual(got, winner) {
				t.Fatalf("winner not replayable: %+v %v", got, err)
			}
		})
	}
}

func TestRegistrationCommitRetryAndClose(t *testing.T) {
	t.Parallel()
	t.Run("concurrent-retry", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		a := registerPrivate(t, f.service, 100)
		b := registerPrivate(t, f.service, 200)
		r := registrationRequest(a, 300, 400, "retry")
		r.Audience = []uuid.UUID{b.AgentID, a.AgentID}
		arrived, release := make(chan struct{}, 2), make(chan struct{})
		f.service.deps.beforeLookup = func() { arrived <- struct{}{}; <-release }
		type result struct {
			receipt RegistrationReceipt
			err     error
		}
		results := make(chan result, 2)
		before := f.store.Durable()
		for range 2 {
			go func() { receipt, err := f.service.SetResourceRegistration(ctx, a, r); results <- result{receipt, err} }()
		}
		<-arrived
		<-arrived
		close(release)
		one, two := <-results, <-results
		if one.err != nil || two.err != nil || !reflect.DeepEqual(one.receipt, two.receipt) || one.receipt.Sequence != before+3 || f.store.Durable() != before+3 {
			t.Fatalf("retry duplicated/lost effect: %+v %+v", one, two)
		}
		f.service.deps.beforeLookup = nil
		// Two unrelated F0 commits must not degrade a registration
		// positive into an untyped command or let a changed request through.
		for _, command := range []ledger.Command{
			{CommandID: privateID(900), SubmissionSHA256: ledger.Digest{1}},
			{CommandID: privateID(901), SubmissionSHA256: ledger.Digest{2}},
		} {
			draft := ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, command)}
			if _, err := f.store.Submit(ctx, draft, nil); err != nil {
				t.Fatal(err)
			}
		}
		before = f.store.Durable()
		r.Audience = []uuid.UUID{a.AgentID, b.AgentID}
		retried, err := f.service.SetResourceRegistration(ctx, a, r)
		if err != nil || !reflect.DeepEqual(retried, one.receipt) || f.store.Durable() != before {
			t.Fatalf("F0 traffic lost normalized original receipt: %+v %v", retried, err)
		}
		r.Audience = r.Audience[:1]
		if _, err := f.service.SetResourceRegistration(ctx, a, r); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != before {
			t.Fatalf("F0 traffic admitted changed normalized input: %v", err)
		}
		view, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd})
		if err != nil || view.Record.Version != 1 || !slices.Equal(view.Record.Audience, []uuid.UUID{a.AgentID, b.AgentID}) {
			t.Fatalf("F0 retry repeated or changed original effect: %+v %v", view, err)
		}
	})
	t.Run("installed-lost-ack-and-cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lost := errors.New("lost acknowledgement")
		entered, release := make(chan RegistrationReceipt, 1), make(chan struct{})
		result := make(chan error, 1)
		var once sync.Once
		defer once.Do(func() { close(release) })
		var armed atomic.Bool
		var observed RegistrationReceipt
		var f *serviceFixture
		f = newServiceFixture(t, serviceDeps{
			afterScopedSubmit: func() {
				if armed.Load() {
					cancel()
				}
			},
			afterRegistrationCommit: func(r RegistrationReceipt) error {
				args := r.Submission.Arguments.(ledger.ResourceRegistrationArguments)
				view, err := f.service.ResourceStatus(context.Background(), ResourceRegistrationLookup{Resource: args.Resource, ThroughSequence: ledger.ToEnd})
				if err != nil || view.Record.Version != r.Version || view.ReceiptSequence != r.Sequence || f.store.Durable() != r.Sequence {
					t.Errorf("ack preceded installed durable state: %+v %v", view, err)
				}
				actual, err := f.service.RegistrationCommandStatus(context.Background(), RegistrationCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
				if err != nil || !reflect.DeepEqual(actual, r) {
					t.Errorf("ack used predicted receipt: %+v %v", actual, err)
				}
				entered <- r
				<-release
				return lost
			},
		})
		a := registerPrivate(t, f.service, 100)
		r := registrationRequest(a, 300, 400, "lost-ack")
		armed.Store(true)
		go func() { _, err := f.service.SetResourceRegistration(ctx, a, r); result <- err }()
		select {
		case observed = <-entered:
		case err := <-result:
			t.Fatalf("post-commit cancellation returned before acknowledgment hook: %v", err)
		}
		once.Do(func() { close(release) })
		if err := <-result; !errors.Is(err, lost) {
			t.Fatalf("post-commit cancellation prevented completion: %v", err)
		}
		before := f.store.Durable()
		reopenScoped(t, f, DefaultLimits())
		got, err := f.service.SetResourceRegistration(context.Background(), a, r)
		if err != nil || !reflect.DeepEqual(got, observed) || f.store.Durable() != before {
			t.Fatalf("lost acknowledgement retry: %+v %v", got, err)
		}
	})
	t.Run("close-fence", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		entered, release, fenced := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		f := newServiceFixture(t, serviceDeps{afterRegistrationCommit: func(RegistrationReceipt) error { close(entered); <-release; return nil }, afterCloseFence: func() { close(fenced) }})
		a := registerPrivate(t, f.service, 100)
		r := registrationRequest(a, 300, 400, "close")
		result, closed := make(chan error, 1), make(chan error, 1)
		go func() { _, err := f.service.SetResourceRegistration(ctx, a, r); result <- err }()
		<-entered
		go func() { closed <- f.service.Close() }()
		<-fenced
		if _, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: r.Resource, ThroughSequence: ledger.ToEnd}); !errors.Is(err, ErrClosed) {
			t.Fatalf("query crossed Close fence: %v", err)
		}
		select {
		case err := <-closed:
			t.Fatalf("Close returned before active acknowledgement: %v", err)
		default:
		}
		once.Do(func() { close(release) })
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRegistrationDeclarationHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	b := registerPrivate(t, f.service, 200)
	work := scopedWorkContext(t, f.service, a, 110, 111)
	replacement := scopedWorkContext(t, f.service, a, 120, 121)
	scopedCreate(t, f.service, a, 500, 501, a.AgentID, b.AgentID)
	scopedCreate(t, f.service, a, 510, 511, b.AgentID)
	r := registrationRequest(a, 300, 400, "declarations")
	r.Audience = []uuid.UUID{b.AgentID, a.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetWorkDomainRegistration(ctx, a, WorkDomainRegistration{CommandID: privateID(301), WorkDomainID: privateID(401), Audience: r.Audience}); err != nil {
		t.Fatal(err)
	}
	task := ledger.DeclarationConsumer{Kind: "task", TaskID: privateID(500)}
	contextConsumer := ledger.DeclarationConsumer{Kind: "context", Binding: ledger.Binding(work)}
	var originals []DeclarationView
	var requests []DeclarationChange
	for i, subject := range []ledger.RegistrationRef{{Kind: "resource", ID: r.ResourceID, Version: 1}, {Kind: "work_domain", ID: privateID(401), Version: 1}} {
		for j, consumer := range []ledger.DeclarationConsumer{task, contextConsumer} {
			n := i*2 + j
			request := DeclarationChange{CommandID: privateID(310 + n), DeclarationID: privateID(410 + n), Subject: subject, Audience: []uuid.UUID{a.AgentID}, Consumers: []ledger.DeclarationConsumer{consumer}}
			receipt, err := f.service.SetDeclaration(ctx, a, request)
			if err != nil {
				t.Fatal(err)
			}
			view, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: request.DeclarationID, ThroughSequence: ledger.ToEnd})
			if err != nil || view.Record.Subject != subject || !reflect.DeepEqual(view.Record.Consumers, request.Consumers) || view.ReceiptSequence != receipt.Sequence {
				t.Fatalf("source/consumer shape: %+v %v", view, err)
			}
			requests, originals = append(requests, request), append(originals, view)
		}
	}
	before := f.store.Durable()
	base := requests[0]
	for _, row := range []struct {
		name   string
		change func(*DeclarationChange)
		want   error
	}{
		{"missing-source", func(r *DeclarationChange) { r.Subject.ID = privateID(999) }, ErrUnavailable},
		{"old-source", func(r *DeclarationChange) { r.Subject.Version = 2 }, ErrRevision},
		{"missing-task", func(r *DeclarationChange) {
			r.Consumers = []ledger.DeclarationConsumer{{Kind: "task", TaskID: privateID(999)}}
		}, ErrUnavailable},
		{"task-audience", func(r *DeclarationChange) {
			r.Consumers = []ledger.DeclarationConsumer{{Kind: "task", TaskID: privateID(510)}}
		}, ErrInvalid},
		{"missing-context", func(r *DeclarationChange) {
			r.Consumers = []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding{AgentID: a.AgentID, ContextID: privateID(999)}}}
		}, ErrNotBound},
		{"foreign-context", func(r *DeclarationChange) {
			r.Consumers = []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding{AgentID: a.AgentID, ContextID: b.ContextID}}}
		}, ErrNotBound},
		{"context-audience", func(r *DeclarationChange) {
			r.Consumers = []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding(b)}}
		}, ErrInvalid},
		{"duplicate-consumers", func(r *DeclarationChange) { r.Consumers = []ledger.DeclarationConsumer{task, task} }, ErrInvalid},
	} {
		request := base
		request.CommandID, request.DeclarationID = privateID(330), privateID(430)
		row.change(&request)
		if _, err := f.service.SetDeclaration(ctx, a, request); !errors.Is(err, row.want) || f.store.Durable() != before {
			t.Fatalf("%s: %v", row.name, err)
		}
	}
	// A source revision never rewrites existing pins, even when its new
	// audience no longer contains the declaration audience.
	r.CommandID, r.ExpectedVersion, r.Audience = privateID(340), 1, []uuid.UUID{b.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatal(err)
	}
	pinned, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: base.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || pinned.Record.Subject.Version != 1 || pinned.Record.Audience[0] != a.AgentID {
		t.Fatalf("source revision rewrote declaration: %+v %v", pinned, err)
	}
	exact, err := f.service.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: pinned.Record.Subject, ThroughSequence: ledger.ToEnd})
	if err != nil || exact.Resource == nil || !slices.Contains(exact.Resource.Record.Audience, a.AgentID) {
		t.Fatalf("pinned source borrowed current scope: %+v %v", exact, err)
	}
	bad := base
	bad.CommandID, bad.ExpectedVersion, bad.Subject.Version = privateID(341), 1, 2
	before = f.store.Durable()
	if _, err := f.service.SetDeclaration(ctx, a, bad); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("new declaration escaped source audience: %v", err)
	}
	bad.Subject = ledger.RegistrationRef{Kind: "work_domain", ID: privateID(401), Version: 1}
	if _, err := f.service.SetDeclaration(ctx, a, bad); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("declaration source retarget: %v", err)
	}
	r.CommandID, r.ExpectedVersion, r.Audience = privateID(342), 2, []uuid.UUID{a.AgentID, b.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatal(err)
	}
	mixed := base
	mixed.CommandID, mixed.ExpectedVersion, mixed.Subject.Version = privateID(343), 1, 3
	mixed.Consumers = []ledger.DeclarationConsumer{task, contextConsumer}
	receipt, err := f.service.SetDeclaration(ctx, a, mixed)
	if err != nil || mixed.Consumers[0].Kind != "task" {
		t.Fatalf("mixed consumers or caller mutation: %+v %v", receipt, err)
	}
	view, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: base.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || !reflect.DeepEqual(view.Record.Consumers, []ledger.DeclarationConsumer{contextConsumer, task}) {
		t.Fatalf("mixed replacement: %+v %v", view, err)
	}
	view.Record.Consumers[0].Binding = ledger.Binding(b)
	view.Record.Audience[0] = b.AgentID
	domain, err := f.service.WorkDomainStatus(ctx, RegistrationLookup{ID: privateID(401), ThroughSequence: ledger.ToEnd})
	if err != nil {
		t.Fatal(err)
	}
	domain.Record.Audience[0] = privateID(999)
	again, err := f.service.WorkDomainStatus(ctx, RegistrationLookup{ID: privateID(401), ThroughSequence: ledger.ToEnd})
	if err != nil || again.Record.Audience[0] != a.AgentID {
		t.Fatalf("domain view alias: %+v %v", again, err)
	}
	if _, err := f.service.AssignTask(ctx, a, TaskAssignment{CommandID: privateID(350), TaskID: task.TaskID, ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(351), Assignee: ledger.Binding(work)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReassignTask(ctx, a, TaskAssignment{CommandID: privateID(352), TaskID: task.TaskID, ExpectedVersion: 2, ExpectedAssignmentID: privateID(351), ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(353), Assignee: ledger.Binding(replacement)}}); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.DeclarationStatus(ctx, RegistrationLookup{ID: base.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || !reflect.DeepEqual(view.Record.Consumers, []ledger.DeclarationConsumer{contextConsumer, task}) || view.Record.Audience[0] != a.AgentID {
		t.Fatalf("reassignment/returned-value mutation retargeted declaration: %+v %v", view, err)
	}
	if _, err := f.service.TransitionTask(ctx, a, TaskMutation{CommandID: privateID(354), TaskID: task.TaskID, ExpectedVersion: 3, ExpectedAssignmentID: privateID(353), ExpectedGeneration: 2, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	mixed.CommandID, mixed.ExpectedVersion = privateID(355), 2
	if _, err := f.service.SetDeclaration(ctx, a, mixed); err != nil {
		t.Fatalf("terminal task was treated as missing: %v", err)
	}
	mixed.CommandID, mixed.ExpectedVersion, mixed.Consumers = privateID(356), 3, nil
	if _, err := f.service.SetDeclaration(ctx, a, mixed); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.DeclarationStatus(ctx, RegistrationLookup{ID: base.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || view.Record.Version != 4 || view.Record.Consumers == nil || len(view.Record.Consumers) != 0 {
		t.Fatalf("withdrawal not a real empty snapshot: %+v %v", view, err)
	}
	for _, old := range originals {
		got, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: old.Record.DeclarationID, ThroughSequence: old.ThroughSequence})
		if err != nil || !reflect.DeepEqual(got, old) {
			t.Fatalf("historical declaration rewritten: %+v %v", got, err)
		}
	}
	reopenScoped(t, f, DefaultLimits())
	got, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: base.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || !reflect.DeepEqual(got, view) {
		t.Fatalf("declaration restart changed current state: %+v %v", got, err)
	}
}

func TestRegistrationGrantsRemainIndependent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 100)
	b := registerPrivate(t, f.service, 200)
	scopedEnrol(t, f.service, a, scopedCapacity(), 210, a.AgentID, b.AgentID)
	scopedCreate(t, f.service, a, 500, 501, a.AgentID, b.AgentID)
	agent := scopedBound(t, f.service, a)
	outsider := scopedBound(t, f.service, b)
	address := ledger.Resource{Kind: "external", ResourceKey: "paid-route/" + privateID(450).String()}
	root := GrantSpec{GrantID: privateID(600), Grantee: a, Operations: []string{"grant.issue", "model.call"}, Resources: []ledger.Resource{address}, DataClass: "external", Recipients: []Binding{a}, Limits: scopedCapacity(), DelegationDepth: 1, Lifetime: "until_revoked"}
	scopedRoot(t, f.service, a, 0, 601, root)
	inspect := GrantSpec{GrantID: privateID(610), Grantee: a, Operations: []string{"task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(500)}}, DataClass: "task", Recipients: []Binding{a}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, a, 1, 611, inspect)
	// Actual delegated authority works before this address is registered.
	child := root
	child.GrantID, child.ParentGrantID, child.Operations, child.DelegationDepth = privateID(620), root.GrantID, []string{"model.call"}, 0
	if _, err := agent.IssueGrant(ctx, GrantIssue{CommandID: privateID(621), GrantID: root.GrantID, ExpectedAuthorityVersion: 2, Spec: child}); err != nil {
		t.Fatalf("registration became grant prerequisite: %v", err)
	}
	grantBefore, err := agent.GrantStatus(ctx, GrantLookup{GrantID: child.GrantID})
	if err != nil {
		t.Fatal(err)
	}
	bookBefore, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil {
		t.Fatal(err)
	}
	checkAccess := func() {
		t.Helper()
		if _, err := agent.TaskStatus(ctx, TaskLookup{TaskID: privateID(500), GrantID: inspect.GrantID}); err != nil {
			t.Fatalf("existing grant lost access: %v", err)
		}
		if _, err := outsider.TaskStatus(ctx, TaskLookup{TaskID: privateID(500), GrantID: inspect.GrantID}); !errors.Is(err, ErrDenied) {
			t.Fatalf("evidence audience granted access: %v", err)
		}
	}
	checkAccess()
	r := registrationRequest(a, 300, 400, address.ResourceKey)
	r.Audience = []uuid.UUID{a.AgentID, b.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatalf("paid-route registration required provider setup: %v", err)
	}
	declaration := DeclarationChange{CommandID: privateID(301), DeclarationID: privateID(401), Subject: ledger.RegistrationRef{Kind: "resource", ID: r.ResourceID, Version: 1}, Audience: []uuid.UUID{b.AgentID}, Consumers: []ledger.DeclarationConsumer{{Kind: "task", TaskID: privateID(500)}}}
	if _, err := f.service.SetDeclaration(ctx, a, declaration); err != nil {
		t.Fatal(err)
	}
	checkAccess()
	r.CommandID, r.ExpectedVersion, r.Audience = privateID(302), 1, []uuid.UUID{b.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, a, r); err != nil {
		t.Fatal(err)
	}
	declaration.CommandID, declaration.ExpectedVersion, declaration.Subject.Version, declaration.Consumers = privateID(303), 1, 2, nil
	if _, err := f.service.SetDeclaration(ctx, a, declaration); err != nil {
		t.Fatal(err)
	}
	checkAccess()
	grantAfter, err := agent.GrantStatus(ctx, GrantLookup{GrantID: child.GrantID})
	grantBefore.ThroughSequence = grantAfter.ThroughSequence
	if err != nil || !reflect.DeepEqual(grantBefore, grantAfter) {
		t.Fatalf("registration changed grant authority: %+v %v", grantAfter, err)
	}
	bookAfter, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	bookBefore.ThroughSequence = bookAfter.ThroughSequence
	if err != nil || !reflect.DeepEqual(bookBefore, bookAfter) {
		t.Fatalf("registration charged or changed capacity: %+v %v", bookAfter, err)
	}
	child.GrantID = privateID(630)
	if _, err := agent.IssueGrant(ctx, GrantIssue{CommandID: privateID(631), GrantID: root.GrantID, ExpectedAuthorityVersion: 3, Spec: child}); err != nil {
		t.Fatalf("narrowed registration revoked independent grant: %v", err)
	}
	reopenScoped(t, f, DefaultLimits())
}
