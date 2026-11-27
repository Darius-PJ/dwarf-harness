package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
)

// Host owns the colony Claim and all Core resources independently of clients.
type Host struct {
	mu               sync.Mutex
	dir              string
	claim            *ledgerstore.Claim
	owner            *localipc.Owner
	listener         localipc.Listener
	status           launcher.HostStatusResult
	changed          chan struct{}
	connections      map[*connection]struct{}
	serving          sync.WaitGroup
	closing          bool
	composition      *Composition
	shutdownID       uuid.UUID
	shutdownExpected launcher.Expected
	done             chan struct{}
	result           error
}

// NewCandidate takes the writer lock before creating an endpoint or recovering.
func NewCandidate(dir string) (*Host, *Failure) {
	claim, err := ledgerstore.ClaimColony(dir)
	if err != nil {
		code := "startup_failed"
		if errors.Is(err, ledgerstore.ErrLocked) {
			code = "locked"
		}
		return nil, &Failure{Stage: "colony", Class: Describe(err), Code: code, cause: err}
	}
	fail := func(stage, code string, err error) (*Host, *Failure) {
		_ = claim.Close()
		return nil, &Failure{Stage: stage, Class: Describe(err), Code: code, cause: err}
	}
	owner, err := localipc.CurrentOwner()
	if err != nil {
		return fail("host endpoint", "endpoint_failed", err)
	}
	if err := owner.PrepareRun(dir); err != nil {
		return fail("host endpoint", "endpoint_failed", err)
	}
	previous := false
	if _, err := os.Lstat(filepath.Join(dir, "run", "host.json")); err == nil {
		previous = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail("host discovery", "discovery_failed", err)
	}
	id := uuid.NewV7()
	listener, err := owner.Listen(claim.Colony(), id)
	if err != nil {
		return fail("host endpoint", "endpoint_failed", err)
	}
	h := &Host{dir: dir, claim: claim, owner: owner, listener: listener, status: launcher.HostStatusResult{HostID: id, ColonyID: claim.Colony(), State: "booting", LifecycleRevision: 1}, changed: make(chan struct{}), connections: map[*connection]struct{}{}, done: make(chan struct{})}
	h.composition = NewComposition(dir, Configuration{}, nil)
	if previous {
		h.composition.Conditions = append(h.composition.Conditions, "previous_host_unclean")
	}
	h.serving.Add(1)
	go h.accept()
	if err := owner.Publish(dir, claim.Colony(), id, listener.Endpoint()); err != nil {
		_ = h.abort(false)
		return nil, &Failure{Stage: "host discovery", Class: Describe(err), Code: "discovery_failed", cause: err}
	}
	return h, nil
}
func (h *Host) Start(ctx context.Context, configuration Configuration) *Failure {
	h.mu.Lock()
	h.transitionLocked("recovering", 2, "")
	h.mu.Unlock()
	h.composition.configuration = configuration
	if failure := h.composition.Compose(ctx, h.claim); failure != nil {
		_ = h.Abort()
		return failure
	}
	summary := h.composition.Summary()
	h.mu.Lock()
	h.status.Startup = &summary
	h.transitionLocked("ready", 3, "")
	h.mu.Unlock()
	return nil
}
func (h *Host) Status() launcher.HostStatusResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}
func (h *Host) transitionLocked(state string, revision int64, code launcher.Code) {
	h.status.State = state
	h.status.LifecycleRevision = revision
	h.status.Code = code
	close(h.changed)
	h.changed = make(chan struct{})
	for c := range h.connections {
		if c.welcomed {
			c.enqueue(outbound{state: &launcher.HostState{HostID: h.status.HostID, State: state, LifecycleRevision: revision, Code: code}})
		}
	}
}
func (h *Host) watch(ctx context.Context, after int64) (launcher.HostStatusResult, error) {
	for {
		h.mu.Lock()
		status, changed := h.status, h.changed
		h.mu.Unlock()
		if status.LifecycleRevision != after || status.Code != "" {
			return status, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return launcher.HostStatusResult{}, ctx.Err()
		}
	}
}
func (h *Host) Wait() error { <-h.done; return h.result }

// Abort is startup failure cleanup, never a stopped-success announcement.
func (h *Host) Abort() error { return h.abort(true) }
func (h *Host) abort(remove bool) error {
	err := h.composition.Close()
	err = errors.Join(err, h.closeEndpoint(remove), h.claim.Close())
	return err
}
func (h *Host) closeEndpoint(remove bool) error {
	h.mu.Lock()
	h.closing = true
	for c := range h.connections {
		c.cancel()
		_ = c.conn.Close()
	}
	h.mu.Unlock()
	err := h.listener.Close()
	h.serving.Wait()
	if remove {
		err = errors.Join(err, h.owner.RemoveDiscovery(h.dir, h.status.ColonyID, h.status.HostID))
	}
	return err
}
