package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

type workspaceStore interface {
	appender.Appender
	PublishBody(context.Context, io.Reader) (ledger.Digest, int64, error)
	ReadBody(context.Context, ledger.Digest) ([]byte, error)
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
}

type serviceDeps struct {
	environ      func() []string
	newID        func() uuid.UUID
	beforeAppend func()
	removeAll    func(string) error
}

// Reconciliation reports best-effort filesystem repair. Pending entries explain
// unavailable workspace inputs, unverified repositories and unfinished cleanup.
// Missing lists open scratches whose directories are absent or anchors unavailable.
type Reconciliation struct {
	OrphansRemoved, RemovalsCompleted int
	Pending                           []string
	Missing                           []uuid.UUID
}

// Service owns one colony's workspace lifecycle. Its store remains host-owned.
// Run exactly one service over that store; Close drains operations and writers.
type Service struct {
	mu                     sync.Mutex
	ops                    sync.WaitGroup
	closed, poisoned       bool
	closeDone              chan struct{}
	store                  workspaceStore
	colony                 string
	deps                   serviceDeps
	git                    *gitRunner
	engine                 *captureEngine
	pool                   *reservationPool
	inputsLock             chan struct{}
	workspaces             map[uuid.UUID]workspaceState
	scratches              map[uuid.UUID]scratchState
	pairs                  map[scratchPair]uuid.UUID
	usedDirs, usedBranches map[string]bool
	pendingDirs            map[string]bool
	creating               map[scratchPair]chan struct{}
}

// Open validates the durable log before reconciling disposable state. Git may be
// unavailable without preventing replay. Pass the real ledgerstore.Store opened
// with ledger.Defined(); the service neither owns nor closes it.
func Open(ctx context.Context, store workspaceStore, colonyDir string, cfg Config) (*Service, Reconciliation, error) {
	return openService(ctx, store, colonyDir, cfg, serviceDeps{})
}

func openService(ctx context.Context, store workspaceStore, colonyDir string, cfg Config, deps serviceDeps) (*Service, Reconciliation, error) {
	var report Reconciliation
	if store == nil {
		return nil, report, ErrInvalid
	}
	colony, err := filepath.Abs(colonyDir)
	if err != nil {
		return nil, report, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	want, err := os.Stat(filepath.Join(colony, "log"))
	if err != nil {
		return nil, report, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	actual, err := os.Stat(store.LogDir())
	if err != nil || !os.SameFile(want, actual) {
		return nil, report, fmt.Errorf("%w: store belongs to a different colony directory", ErrInvalid)
	}
	pool, err := newReservationPool(cfg.Capture)
	if err != nil {
		return nil, report, err
	}
	if err := ctx.Err(); err != nil {
		return nil, report, err
	}
	if deps.newID == nil {
		deps.newID = uuid.NewV7
	}
	if deps.removeAll == nil {
		deps.removeAll = os.RemoveAll
	}
	s := &Service{store: store, colony: colony, deps: deps, pool: pool, closeDone: make(chan struct{}), inputsLock: make(chan struct{}, 1), workspaces: map[uuid.UUID]workspaceState{}, scratches: map[uuid.UUID]scratchState{}, pairs: map[scratchPair]uuid.UUID{}, usedDirs: map[string]bool{}, usedBranches: map[string]bool{}, pendingDirs: map[string]bool{}, creating: map[scratchPair]chan struct{}{}}
	s.git = newGitRunner(deps.environ)
	s.engine = newCaptureEngine(s.git, colony)
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: store.Durable(), Consume: workspaceConsumption(), AllTypes: true})
	if err != nil {
		return nil, report, err
	}
	defer reader.Close()
	for {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, report, err
		}
		drafts := make([]ledger.Draft, len(commit.Records))
		for i, record := range commit.Records {
			drafts[i] = record.Draft
		}
		if _, err := s.apply(ctx, drafts, commit.First, false); err != nil {
			return nil, report, err
		}
	}
	for _, id := range sortedIDs(s.workspaces) {
		w, err := s.loadInputs(ctx, s.workspaces[id])
		if err != nil {
			return nil, report, err
		}
		s.workspaces[id] = w
	}
	if err := prepareGitLayout(colony); err != nil {
		return nil, report, err
	}
	if err := os.MkdirAll(filepath.Join(colony, "scratch"), 0o700); err != nil {
		return nil, report, err
	}
	report = s.reconcile(ctx)
	return s, report, nil
}

func workspaceConsumption() ledger.Consumption {
	return ledger.Consumption{ledger.TypeWorkspaceRegistered: {1}, ledger.TypeCaptureInputsPinned: {1}, ledger.TypeScratchCreated: {1}, ledger.TypeScratchClosed: {1}, ledger.TypeBodyPin: {1}, ledger.TypeBodyUnpin: {1}}
}

func validID(id uuid.UUID) bool   { return id[6]>>4 == 7 && id[8]&0xc0 == 0x80 }
func shortID(id uuid.UUID) string { text := id.String(); return text[len(text)-8:] }

func (s *Service) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.poisoned {
		return ErrPoisoned
	}
	s.ops.Add(1)
	return nil
}

// Close prevents new calls and waits for existing calls and BeginWrite holders.
// It is idempotent and does not close the host's ledger store.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.ops.Wait()
	close(s.closeDone)
	return nil
}

// append holds mu through validation and acknowledgement. apply's undo journal
// keeps failed commits invisible without cloning projections that grow forever.
func (s *Service) append(ctx context.Context, drafts []ledger.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poisoned {
		return ErrPoisoned
	}
	first := s.store.Durable() + 1
	undo, err := s.apply(ctx, drafts, first, true)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	committed, err := s.store.Append(ctx, drafts)
	if err != nil {
		undo()
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
			return fmt.Errorf("%w: %w", ErrPoisoned, err)
		}
		return err
	}
	// Other Core producers may have appended between Durable and Append.
	for i, draft := range drafts {
		if draft.Type == ledger.TypeCaptureInputsPinned {
			p, _ := ledger.DecodeCaptureInputsPinned(draft.Payload)
			w := s.workspaces[p.WorkspaceID]
			w.inputsSeq = committed.First + uint64(i)
			s.workspaces[p.WorkspaceID] = w
		}
	}
	return nil
}

func (s *Service) apply(ctx context.Context, drafts []ledger.Draft, first uint64, loading bool) (undo func(), err error) {
	var journal []func()
	undo = func() {
		for i := len(journal) - 1; i >= 0; i-- {
			journal[i]()
		}
	}
	defer func() {
		if err != nil {
			undo()
		}
	}()
	for i, draft := range drafts {
		seq := first + uint64(i)
		bad := func(detail string) error { return &InconsistentError{Seq: seq, Detail: detail} }
		switch draft.Type {
		case ledger.TypeWorkspaceRegistered:
			p, decodeErr := ledger.DecodeWorkspaceRegistered(draft.Payload)
			if decodeErr != nil {
				return undo, bad(decodeErr.Error())
			}
			if draft.Actor != ledger.ActorOwner || len(draft.BodyRefs) != 0 {
				return undo, bad("registration actor or body_refs")
			}
			if i != 0 || len(drafts) != 3 || drafts[1].Type != ledger.TypeCaptureInputsPinned || drafts[2].Type != ledger.TypeBodyPin {
				return undo, bad("registration requires registered, inputs and pin as one three-record commit")
			}
			pin, decodeErr := ledger.DecodeCaptureInputsPinned(drafts[1].Payload)
			if decodeErr != nil || pin.WorkspaceID != p.WorkspaceID || pin.Revision != 1 {
				return undo, bad("registration inputs do not name this workspace at revision 1")
			}
			if _, exists := s.workspaces[p.WorkspaceID]; exists {
				return undo, bad("workspace ID reused")
			}
			for _, w := range s.workspaces {
				if w.CommonDir == p.CommonDir {
					return undo, bad("common_dir already registered")
				}
				if shortID(w.ID) == shortID(p.WorkspaceID) {
					return undo, bad("workspace directory suffix reused")
				}
			}
			id := p.WorkspaceID
			s.workspaces[id] = workspaceState{Workspace: Workspace{ID: id, CommonDir: p.CommonDir, ObjectFormat: p.ObjectFormat}}
			journal = append(journal, func() { delete(s.workspaces, id) })
		case ledger.TypeCaptureInputsPinned:
			p, decodeErr := ledger.DecodeCaptureInputsPinned(draft.Payload)
			if decodeErr != nil {
				return undo, bad(decodeErr.Error())
			}
			old, ok := s.workspaces[p.WorkspaceID]
			if !ok {
				return undo, bad("inputs name an unregistered workspace")
			}
			if draft.Actor != ledger.ActorOwner || !slices.Contains(draft.BodyRefs, p.CaptureInputsSHA256) {
				return undo, bad("inputs require owner and document in body_refs")
			}
			if old.Revision == 0 {
				if p.Revision != 1 || i != 1 || len(drafts) != 3 || drafts[0].Type != ledger.TypeWorkspaceRegistered {
					return undo, bad("revision 1 only belongs to registration index 1")
				}
				registered, e := ledger.DecodeWorkspaceRegistered(drafts[0].Payload)
				if e != nil || registered.WorkspaceID != p.WorkspaceID {
					return undo, bad("revision 1 workspace differs from registration")
				}
			} else {
				if p.Revision <= old.Revision || p.Revision-old.Revision != 1 || i != 0 || len(drafts) < 3 || drafts[2].Type != ledger.TypeBodyUnpin {
					return undo, bad("update requires next revision at index 0 and previous unpin at index 2")
				}
				unpin, e := ledger.DecodeBodyUnpin(drafts[2].Payload)
				if e != nil || unpin.PinID != old.PinID || len(drafts[2].BodyRefs) != 0 {
					return undo, bad("update does not release previous inputs pin")
				}
			}
			if i+1 >= len(drafts) || drafts[i+1].Type != ledger.TypeBodyPin {
				return undo, bad("inputs pin missing from its commit index")
			}
			pin, e := ledger.DecodeBodyPin(drafts[i+1].Payload)
			if e != nil || pin.PinID != p.PinID || !slices.Equal(drafts[i+1].BodyRefs, draft.BodyRefs) {
				return undo, bad("inputs pin ID or body_refs differ")
			}
			w := old
			w.Revision, w.InputsSHA256, w.PinID = p.Revision, p.CaptureInputsSHA256, p.PinID
			w.refs, w.inputsSeq = slices.Clone(draft.BodyRefs), seq
			if loading {
				w, err = s.loadInputs(ctx, w)
				if err != nil {
					return undo, err
				}
				if w.inputsError != "" {
					return undo, bad(w.inputsDetail)
				}
			}
			id := p.WorkspaceID
			s.workspaces[id] = w
			journal = append(journal, func() { s.workspaces[id] = old })
		case ledger.TypeScratchCreated:
			p, decodeErr := ledger.DecodeScratchCreated(draft.Payload)
			if decodeErr != nil {
				return undo, bad(decodeErr.Error())
			}
			if draft.Actor != ledger.ActorCore || draft.ContextID != p.ContextID || len(draft.BodyRefs) != 0 {
				return undo, bad("scratch creation actor, context or body_refs")
			}
			w, ok := s.workspaces[p.WorkspaceID]
			if !ok {
				return undo, bad("scratch names an unregistered workspace")
			}
			if _, ok := s.scratches[p.ScratchID]; ok {
				return undo, bad("scratch ID reused")
			}
			pair := scratchPair{p.ContextID, p.WorkspaceID}
			if _, ok := s.pairs[pair]; ok {
				return undo, bad("pair already has an open scratch")
			}
			if s.usedDirs[p.Dir] || s.usedBranches[p.Branch] {
				return undo, bad("scratch directory or branch reused")
			}
			if p.Branch != "refs/heads/dwarf-harness/"+p.Dir {
				return undo, bad("scratch branch does not match directory")
			}
			if !validOID(p.BaseCommit, oidLength(w.ObjectFormat)) || !validOID(p.AnchorTree, oidLength(w.ObjectFormat)) {
				return undo, bad("scratch Git IDs do not match workspace object format")
			}
			id := p.ScratchID
			s.scratches[id] = scratchState{Scratch: Scratch{ID: id, WorkspaceID: p.WorkspaceID, ContextID: p.ContextID, Dir: filepath.Join(s.colony, "scratch", p.Dir), Branch: p.Branch, BaseCommit: p.BaseCommit, AnchorTree: p.AnchorTree}}
			s.pairs[pair], s.usedDirs[p.Dir], s.usedBranches[p.Branch] = id, true, true
			journal = append(journal, func() {
				delete(s.scratches, id)
				delete(s.pairs, pair)
				delete(s.usedDirs, p.Dir)
				delete(s.usedBranches, p.Branch)
			})
		case ledger.TypeScratchClosed:
			p, decodeErr := ledger.DecodeScratchClosed(draft.Payload)
			if decodeErr != nil {
				return undo, bad(decodeErr.Error())
			}
			old, ok := s.scratches[p.ScratchID]
			if !ok || old.closed {
				return undo, bad("closure names an unknown or closed scratch")
			}
			if p.ContextID != old.ContextID || len(draft.BodyRefs) != 0 || (draft.Actor != ledger.ActorOwner && (draft.Actor != ledger.ActorAgent || draft.ContextID != p.ContextID)) {
				return undo, bad("closure actor, context or body_refs")
			}
			if loading && p.Reason != "abandoned" {
				return undo, bad("producer cannot write an undefined closure reason")
			}
			closed := old
			closed.closed = true
			id := p.ScratchID
			pair := scratchPair{old.ContextID, old.WorkspaceID}
			s.scratches[id] = closed
			delete(s.pairs, pair)
			journal = append(journal, func() { s.scratches[id] = old; s.pairs[pair] = id })
		}
	}
	return undo, nil
}

func sortedIDs[T any](m map[uuid.UUID]T) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}

func (s *Service) reconcile(ctx context.Context) Reconciliation {
	var report Reconciliation
	pending := func(path string, err error) {
		report.Pending = append(report.Pending, fmt.Sprintf("%s: %v", path, err))
	}
	// Temporary indices are disposable, including those for an interrupted
	// registration. Never follow a reparse point while removing them.
	workspaceDirs, err := os.ReadDir(filepath.Join(s.colony, "workspaces"))
	if err != nil {
		pending("workspaces", err)
	}
	for _, entry := range workspaceDirs {
		if !entry.IsDir() || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			continue
		}
		tmp := filepath.Join(s.colony, "workspaces", entry.Name(), "tmp")
		if err := removeScratchDirectory(tmp, s.deps.removeAll); err != nil {
			pending(tmp, err)
		}
	}
	usable := map[uuid.UUID]bool{}
	anchors := map[uuid.UUID]map[string]string{}
	for _, id := range sortedIDs(s.workspaces) {
		w := s.workspaces[id]
		if w.inputsError != "" {
			pending(id.String(), &UnavailableError{Reason: w.inputsError, Detail: w.inputsDetail})
		}
		if s.git.unavailable != nil {
			pending(id.String(), s.git.unavailable)
			continue
		}
		// Cleanup needs a verified repository, not materializable input bodies.
		check := w
		check.inputsError = ""
		if err := s.workspaceReady(ctx, check); err != nil {
			pending(id.String(), err)
			continue
		}
		refs, err := s.anchorRefs(ctx, w)
		if err != nil {
			pending(id.String(), err)
			continue
		}
		usable[id], anchors[id] = true, refs
	}
	known := map[string]bool{}
	for _, id := range sortedIDs(s.scratches) {
		scratch := s.scratches[id]
		known[filepath.Base(scratch.Dir)] = true
		w := s.workspaces[scratch.WorkspaceID]
		if !usable[w.ID] {
			continue
		}
		if scratch.closed {
			_, statErr := os.Lstat(scratch.Dir)
			if err := s.git.removeWorktree(ctx, s.colony, w.CommonDir, scratch.worktree(), s.deps.removeAll); err != nil {
				pending(scratch.Dir, err)
			} else {
				delete(anchors[w.ID], scratch.worktree().AnchorRef)
				if statErr == nil {
					report.RemovalsCompleted++
				}
			}
			continue
		}
		missing := false
		ref := scratch.worktree().AnchorRef
		if anchors[w.ID][ref] != scratch.BaseCommit {
			if anchors[w.ID][ref] == "" {
				if err := s.git.createRef(ctx, s.colony, w.CommonDir, ref, scratch.BaseCommit); err != nil {
					pending(ref, &UnavailableError{Reason: "anchor_missing", Detail: err.Error()})
					missing = true
				}
			} else {
				pending(ref, &UnavailableError{Reason: "anchor_missing", Detail: "anchor differs from recorded base"})
				missing = true
			}
		}
		info, err := os.Lstat(scratch.Dir)
		if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			missing = true
		}
		if missing {
			report.Missing = append(report.Missing, id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.colony, "scratch"))
	if err != nil {
		pending("scratch", err)
	}
	protectedSuffixes := map[string]bool{}
	for _, entry := range entries {
		if known[entry.Name()] {
			continue
		}
		path := filepath.Join(s.colony, "scratch", entry.Name())
		branch := "refs/heads/dwarf-harness/" + entry.Name()
		w, verified := s.orphanWorkspace(path, usable)
		var matches []string
		for ref := range anchors[w.ID] {
			id, err := uuid.Parse(strings.TrimPrefix(ref, "refs/dwarf-harness/anchors/"))
			if err == nil && validID(id) && shortID(id) == entry.Name() {
				matches = append(matches, ref)
			}
		}
		if !verified || len(matches) != 1 {
			protectedSuffixes[entry.Name()] = true
			// Ambiguity must not be erased by the later stray-anchor sweep.
			for _, ref := range matches {
				delete(anchors[w.ID], ref)
			}
			if err := removeScratchDirectory(path, s.deps.removeAll); err != nil {
				pending(path, err)
			} else {
				report.OrphansRemoved++
				if verified {
					if _, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "worktree", "prune", "--expire=now"); err != nil {
						pending(path, err)
					}
				}
			}
			pending(branch, errors.New("orphan workspace or base is unverified; branch retained"))
			continue
		}
		ref := matches[0]
		wt := scratchWorktree{Dir: path, Branch: branch, BaseCommit: anchors[w.ID][ref], AnchorRef: ref}
		if err := s.git.removeWorktree(ctx, s.colony, w.CommonDir, wt, s.deps.removeAll); err != nil {
			pending(path, err)
			continue
		}
		delete(anchors[w.ID], ref)
		report.OrphansRemoved++
		if current, err := s.git.readRef(ctx, s.colony, w.CommonDir, branch, oidLength(w.ObjectFormat)); err != nil {
			pending(branch, err)
		} else if current != "" {
			pending(branch, errors.New("orphan branch moved from its anchor; retained"))
		}
	}
	for _, id := range sortedIDs(s.workspaces) {
		w := s.workspaces[id]
		for ref, oid := range anchors[id] {
			anchorID, err := uuid.Parse(strings.TrimPrefix(ref, "refs/dwarf-harness/anchors/"))
			if err == nil && protectedSuffixes[shortID(anchorID)] {
				continue
			}
			if err == nil && validID(anchorID) {
				if _, statErr := os.Lstat(filepath.Join(s.colony, "scratch", shortID(anchorID))); !os.IsNotExist(statErr) {
					continue
				}
			}
			if err == nil {
				if scratch, ok := s.scratches[anchorID]; ok && !scratch.closed {
					continue
				}
			}
			if _, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, oid); err != nil {
				pending(ref, err)
			}
		}
		if usable[id] {
			// No C2 product operation owns a sealed capture yet. A retained
			// capture without a record is an interrupted, unexposed operation.
			refs, err := s.listRefs(ctx, w, "refs/dwarf-harness/captures/")
			if err != nil {
				pending(w.CommonDir, err)
				continue
			}
			for ref, oid := range refs {
				if _, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, oid); err != nil {
					pending(ref, err)
				}
			}
		}
	}
	slices.Sort(report.Pending)
	return report
}

func (s *Service) anchorRefs(ctx context.Context, w workspaceState) (map[string]string, error) {
	return s.listRefs(ctx, w, "refs/dwarf-harness/anchors/")
}

func (s *Service) listRefs(ctx context.Context, w workspaceState, prefix string) (map[string]string, error) {
	out, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "for-each-ref", "--format=%(refname)%00%(objectname)%00", "--end-of-options", prefix)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	// for-each-ref appends LF after the format. Field boundaries remain NUL;
	// Git ref names cannot contain LF, so strip only that record separator.
	for len(out) != 0 {
		name, rest, ok := strings.Cut(string(out), "\x00")
		if !ok {
			return nil, fmt.Errorf("%w: malformed ref listing", ErrGit)
		}
		oid, rest, ok := strings.Cut(rest, "\x00")
		if !ok || !strings.HasPrefix(rest, "\n") || !strings.HasPrefix(name, prefix) || !validOID(oid, oidLength(w.ObjectFormat)) {
			return nil, fmt.Errorf("%w: malformed ref listing", ErrGit)
		}
		refs[name] = oid
		out = []byte(rest[1:])
	}
	return refs, nil
}

// orphanWorkspace reads only a regular .git file in a real directory. The
// administrative path must identify a registered, verified common directory.
func (s *Service) orphanWorkspace(dir string, usable map[uuid.UUID]bool) (workspaceState, bool) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return workspaceState{}, false
	}
	gitfile := filepath.Join(dir, ".git")
	info, err = os.Lstat(gitfile)
	if err != nil || !info.Mode().IsRegular() {
		return workspaceState{}, false
	}
	b, err := os.ReadFile(gitfile)
	if err != nil {
		return workspaceState{}, false
	}
	admin, ok := strings.CutPrefix(strings.TrimSuffix(string(b), "\n"), "gitdir: ")
	if !ok || admin == "" || strings.ContainsAny(admin, "\x00\r\n") {
		return workspaceState{}, false
	}
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(dir, admin)
	}
	parent := filepath.Dir(filepath.Clean(admin))
	if filepath.Base(parent) != "worktrees" {
		return workspaceState{}, false
	}
	common, err := os.Stat(filepath.Dir(parent))
	if err != nil {
		return workspaceState{}, false
	}
	for _, id := range sortedIDs(s.workspaces) {
		if !usable[id] {
			continue
		}
		w := s.workspaces[id]
		candidate, err := os.Stat(w.CommonDir)
		if err == nil && os.SameFile(common, candidate) {
			return w, true
		}
	}
	return workspaceState{}, false
}
