package host

import (
	"errors"
	"fmt"
	"io"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

func (h *Host) shutdown(request launcher.Request) (launcher.Code, launcher.HostStatusResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.CommandID == h.shutdownID && h.shutdownID != (uuid.UUID{}) {
		if request.Expected == nil || *request.Expected != h.shutdownExpected {
			return launcher.CommandMismatch, h.status, false
		}
		return launcher.OK, h.status, false
	}
	if request.Expected == nil || request.Expected.Kind != "host" || request.Expected.ID != h.status.HostID || request.Expected.Revision != 3 || h.status.State != "ready" {
		return launcher.StaleRevision, h.status, false
	}
	h.shutdownID = request.CommandID
	h.shutdownExpected = *request.Expected
	h.transitionLocked("quiescing", 4, "")
	return launcher.OK, h.status, true
}

// ShutdownSignal enters the same incarnation/revision gate as a wire control.
func (h *Host) ShutdownSignal() {
	status := h.Status()
	_, _, start := h.shutdown(launcher.Request{CommandID: uuid.NewV7(), Expected: &launcher.Expected{Kind: "host", ID: status.HostID, Revision: 3}})
	if start {
		go h.drain()
	}
}
func (h *Host) drain() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	err := errors.Join(h.listener.StopAccept(), h.composition.Close())
	h.mu.Lock()
	if err != nil {
		h.transitionLocked("quiescing", 4, launcher.ClosureUnconfirmed)
	} else {
		h.transitionLocked("stopped", 5, "")
	}
	waits := make([]struct {
		c    *connection
		done chan struct{}
	}, 0, len(h.connections))
	for c := range h.connections {
		if !c.welcomed {
			continue
		}
		done := make(chan struct{})
		c.enqueue(outbound{flushed: done})
		waits = append(waits, struct {
			c    *connection
			done chan struct{}
		}{c, done})
	}
	h.mu.Unlock()
	for _, wait := range waits {
		select {
		case <-wait.done:
		case <-wait.c.ctx.Done():
		}
	}
	// A completed native write is not proof that the peer read the final
	// frame. Keep the read side serving until peers detach, or one shared
	// transport guard expires; this also lets an in-flight watch finish.
	guard := time.NewTimer(localipc.Guard())
linger:
	for _, wait := range waits {
		select {
		case <-wait.c.ctx.Done():
		case <-guard.C:
			break linger
		}
	}
	guard.Stop()
	// A closure failure retains the discovery hint for previous_host_unclean.
	// Endpoint cleanup cannot turn a reported terminal result into another
	// exit outcome. A retained hint is still advisory to the next Claim.
	_ = h.closeEndpoint(err == nil)
	h.result = err
	close(h.done)
}

// Shutdown is the owner command entry, shared by the CLI and subprocess tests.
// Exit success requires the authenticated current host to report stopped.
func Shutdown(dir string, output io.Writer) int {
	refuse := func(err error) int {
		code := "host_connection_lost"
		var failure *ConnectionFailure
		if errors.As(err, &failure) {
			code = failure.Code
		}
		_ = terminal.WriteHostCause(output, code)
		return 1
	}
	control, err := Connect(dir)
	if err != nil {
		return refuse(err)
	}
	defer control.Close()
	var revision int64
	var code launcher.Code
	observe := func(status launcher.HostStatusResult) {
		if status.LifecycleRevision > revision || status.Code != "" && status.Code != code {
			fmt.Fprintf(output, "[terminal] host %s %s\n", status.HostID, status.State)
			revision, code = status.LifecycleRevision, status.Code
		}
	}
	if err := control.Shutdown(observe); err != nil {
		return refuse(err)
	}
	return 0
}
