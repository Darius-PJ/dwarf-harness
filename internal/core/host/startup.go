package host

import (
	"errors"
	"os"
	"path/filepath"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
)

// ConnectionFailure exposes only a fixed cause code.
type ConnectionFailure struct{ Code string }

func (e *ConnectionFailure) Error() string { return "host: " + e.Code }

// Control is a serial lifecycle client. Closing it only detaches this view.
// It does not own Core, a Claim, or any child-process lifetime.
type Control struct {
	conn     *localipc.Conn
	decoder  *launcher.Decoder
	Welcome  launcher.Welcome
	latest   launcher.HostStatusResult
	seq, rpc int64
}

func Connect(dir string) (*Control, error) {
	colony, err := ledgerstore.ReadColony(dir)
	if err != nil {
		return nil, err
	}
	owner, err := localipc.CurrentOwner()
	if err != nil {
		return nil, &ConnectionFailure{"unauthorized"}
	}
	discovery, err := owner.ReadDiscovery(dir, colony)
	if err != nil {
		return nil, &ConnectionFailure{"no_host"}
	}
	conn, err := owner.Dial(discovery)
	if err != nil {
		code := "no_host"
		if errors.Is(err, localipc.ErrUnauthorized) || errors.Is(err, localipc.ErrPrivate) {
			code = "unauthorized"
		}
		if errors.Is(err, localipc.ErrUnavailable) {
			code = "host_timeout"
		}
		return nil, &ConnectionFailure{code}
	}
	fail := func(code string) (*Control, error) { _ = conn.Close(); return nil, &ConnectionFailure{code} }
	hello := launcher.Hello{Kind: "hello", Versions: []int64{launcher.DraftVersion}, Profile: "base", ColonyID: colony, ConnectionID: uuid.NewV7()}
	decoder, err := launcher.NewClientDecoder(hello)
	if err != nil {
		return fail("protocol_error")
	}
	raw, err := launcher.Encode(hello, "")
	if err != nil {
		return fail("protocol_error")
	}
	if localipc.WriteFrame(conn, raw) != nil {
		return fail("host_timeout")
	}
	raw, err = localipc.ReadFrame(conn, true)
	if err != nil {
		return fail("host_timeout")
	}
	message, err := decoder.Decode(raw)
	if err != nil {
		return fail("protocol_error")
	}
	if refused, ok := message.(launcher.Refused); ok {
		return fail(string(refused.Code))
	}
	welcome, ok := message.(launcher.Welcome)
	if !ok || welcome.HostID != discovery.HostID || welcome.ColonyID != colony {
		return fail("wrong_colony")
	}
	status := launcher.HostStatusResult{HostID: welcome.HostID, ColonyID: colony, State: welcome.State, LifecycleRevision: welcome.LifecycleRevision}
	return &Control{conn: conn, decoder: decoder, Welcome: welcome, latest: status, seq: 1, rpc: 1}, nil
}
func (c *Control) Close() error                      { return c.conn.Close() }
func (c *Control) Latest() launcher.HostStatusResult { return c.latest }
func (c *Control) Call(operation string, arguments any, command uuid.UUID, expected *launcher.Expected, observe func(launcher.HostStatusResult)) (launcher.Response, error) {
	request := launcher.Request{Envelope: launcher.Envelope{Version: launcher.DraftVersion, ConnectionID: c.Welcome.ConnectionID, Seq: c.seq, Kind: "request"}, RPCID: c.rpc, Operation: operation, CommandID: command, Expected: expected, Arguments: arguments}
	if err := c.decoder.ExpectResponse(request); err != nil {
		return launcher.Response{}, err
	}
	raw, err := launcher.Encode(request, "")
	if err != nil {
		return launcher.Response{}, err
	}
	if err := localipc.WriteFrame(c.conn, raw); err != nil {
		return launcher.Response{}, err
	}
	c.seq++
	c.rpc++
	for {
		raw, err := localipc.ReadFrame(c.conn, false)
		if err != nil {
			return launcher.Response{}, err
		}
		message, err := c.decoder.Decode(raw)
		if err != nil {
			return launcher.Response{}, err
		}
		switch p := message.(type) {
		case launcher.HostState:
			if p.HostID != c.Welcome.HostID {
				return launcher.Response{}, launcher.ErrProtocol
			}
			c.latest.State = p.State
			c.latest.LifecycleRevision = p.LifecycleRevision
			c.latest.Code = p.Code
			if observe != nil {
				observe(c.latest)
			}
		case launcher.Response:
			if p.RPCID != request.RPCID {
				return launcher.Response{}, launcher.ErrProtocol
			}
			if status, ok := p.Result.(launcher.HostStatusResult); ok {
				if status.HostID != c.Welcome.HostID || status.ColonyID != c.Welcome.ColonyID {
					return launcher.Response{}, launcher.ErrProtocol
				}
				if status.LifecycleRevision >= c.latest.LifecycleRevision {
					c.latest = status
				}
				if observe != nil {
					observe(status)
				}
			}
			return p, nil
		default:
			return launcher.Response{}, launcher.ErrProtocol
		}
	}
}
func (c *Control) AwaitReady(observe func(launcher.HostStatusResult)) (launcher.HostStatusResult, error) {
	if observe != nil {
		observe(c.latest)
	}
	response, err := c.Call("host.status", launcher.HostStatus{}, uuid.UUID{}, nil, observe)
	if err != nil {
		return c.latest, err
	}
	if response.Code != launcher.OK {
		return c.latest, &ConnectionFailure{string(response.Code)}
	}
	for c.latest.State == "booting" || c.latest.State == "recovering" {
		response, err = c.Call("host.watch", launcher.HostWatch{AfterRevision: c.latest.LifecycleRevision}, uuid.UUID{}, nil, observe)
		if err != nil {
			return c.latest, err
		}
		if response.Code != launcher.OK {
			return c.latest, &ConnectionFailure{string(response.Code)}
		}
	}
	if c.latest.State != "ready" {
		return c.latest, &ConnectionFailure{"not_ready"}
	}
	return c.latest, nil
}
func (c *Control) Shutdown(observe func(launcher.HostStatusResult)) error {
	response, err := c.Call("host.shutdown", launcher.HostShutdown{}, uuid.NewV7(), &launcher.Expected{Kind: "host", ID: c.Welcome.HostID, Revision: 3}, observe)
	if err != nil {
		return err
	}
	if response.Code != launcher.OK {
		return &ConnectionFailure{string(response.Code)}
	}
	for {
		if c.latest.Code == launcher.ClosureUnconfirmed {
			return &ConnectionFailure{"closure_unconfirmed"}
		}
		if c.latest.State == "stopped" {
			return nil
		}
		response, err = c.Call("host.watch", launcher.HostWatch{AfterRevision: c.latest.LifecycleRevision}, uuid.UUID{}, nil, observe)
		if err != nil {
			if c.latest.Code == launcher.ClosureUnconfirmed {
				return &ConnectionFailure{"closure_unconfirmed"}
			}
			if c.latest.State == "stopped" {
				return nil
			}
			return &ConnectionFailure{"host_connection_lost"}
		}
		if response.Code != launcher.OK {
			return &ConnectionFailure{string(response.Code)}
		}
	}
}

type ProbeResult struct {
	Kind   string // not_colony, no_host, host, or non_host_holder
	HostID uuid.UUID
	State  string
}

// Probe starts no process, takes no Claim and performs no recovery/publication.
// It checks an existing lock through durable's one lock implementation, holding
// it only long enough to observe availability. An absent lock is never created.
func Probe(dir string) (ProbeResult, error) {
	if _, err := ledgerstore.ReadColony(dir); err != nil {
		if errors.Is(err, ledgerstore.ErrNotColony) || errors.Is(err, os.ErrNotExist) {
			return ProbeResult{Kind: "not_colony"}, nil
		}
		return ProbeResult{}, err
	}
	if c, err := Connect(dir); err == nil {
		status := c.Latest()
		closeErr := c.Close()
		return ProbeResult{Kind: "host", HostID: status.HostID, State: status.State}, closeErr
	} else {
		var failure *ConnectionFailure
		if errors.As(err, &failure) && failure.Code != "no_host" && failure.Code != "host_timeout" {
			return ProbeResult{}, err
		}
	}
	path := filepath.Join(dir, "lock")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return ProbeResult{Kind: "no_host"}, nil
	} else if err != nil {
		return ProbeResult{}, err
	}
	lock, err := durable.LockFile(path)
	if errors.Is(err, durable.ErrLocked) {
		return ProbeResult{Kind: "non_host_holder"}, nil
	}
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Kind: "no_host"}, lock.Close()
}
