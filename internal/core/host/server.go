package host

import (
	"context"
	"errors"
	"io"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
)

type outbound struct {
	response  *launcher.Response
	operation string
	state     *launcher.HostState
	flushed   chan struct{}
}
type connection struct {
	host      *Host
	conn      *localipc.Conn
	mu        sync.Mutex // Decoder, outgoing ordinal and all writes have one owner gate.
	decoder   *launcher.Decoder
	id        uuid.UUID
	next      int64
	principal launcher.Principal
	welcomed  bool // guarded by host.mu
	ctx       context.Context
	cancel    context.CancelFunc
	output    chan outbound
	workers   sync.WaitGroup
}

func (h *Host) accept() {
	defer h.serving.Done()
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		c := &connection{host: h, conn: conn, decoder: launcher.NewServerDecoder(), next: 1, ctx: ctx, cancel: cancel, output: make(chan outbound, launcher.MaxResources)}
		h.mu.Lock()
		full := len(h.connections) >= launcher.MaxViews
		if h.closing {
			h.mu.Unlock()
			cancel()
			_ = conn.Close()
			return
		}
		if !full {
			h.connections[c] = struct{}{}
			h.serving.Add(1)
		}
		h.mu.Unlock()
		if full {
			// Bound even unauthenticated overflow to one handshake at a time.
			c.handshake(true)
			cancel()
			_ = conn.Close()
			continue
		}
		go func() {
			defer h.serving.Done()
			defer func() {
				cancel()
				_ = conn.Close()
				c.workers.Wait()
				h.mu.Lock()
				delete(h.connections, c)
				h.mu.Unlock()
			}()
			c.run()
		}()
	}
}
func (c *connection) refuse(code launcher.Code) {
	raw, err := launcher.Encode(launcher.Refused{Kind: "refused", Code: code}, "")
	if err == nil {
		_ = localipc.WriteFrame(c.conn, raw)
	}
}
func (c *connection) handshake(full bool) bool {
	if c.conn.BeforeHello() != nil {
		c.refuse(launcher.Unauthorized)
		return false
	}
	raw, err := localipc.ReadFrame(c.conn, true)
	if err != nil {
		c.refuse(launcher.ProtocolError)
		return false
	}
	message, decodeErr := c.decoder.Decode(raw)
	// The bytes have been read but no claimed value is acted on before the
	// impersonated token has been checked on Windows.
	principal, err := c.conn.Authenticate()
	if err != nil {
		c.refuse(launcher.Unauthorized)
		return false
	}
	if decodeErr != nil {
		if errors.Is(decodeErr, launcher.ErrIncompatible) {
			c.refuse(launcher.Incompatible)
		} else {
			c.refuse(launcher.ProtocolError)
		}
		return false
	}
	hello, ok := message.(launcher.Hello)
	if !ok {
		c.refuse(launcher.ProtocolError)
		return false
	}
	status := c.host.Status()
	if hello.ColonyID != status.ColonyID {
		c.refuse(launcher.WrongColony)
		return false
	}
	if full {
		c.refuse(launcher.Busy)
		return false
	}
	c.id, c.principal = hello.ConnectionID, principal
	c.host.mu.Lock()
	if c.host.closing {
		c.host.mu.Unlock()
		return false
	}
	status = c.host.status
	c.welcomed = true
	c.host.mu.Unlock()
	welcome := launcher.Welcome{Kind: "welcome", Version: launcher.DraftVersion, Profile: "base", ConnectionID: c.id, HostID: status.HostID, ColonyID: status.ColonyID, Principal: principal, State: status.State, LifecycleRevision: status.LifecycleRevision, MaxFrameBytes: launcher.MaxFrameBytes}
	raw, err = launcher.Encode(welcome, "")
	if err != nil || c.decoder.AcceptWelcome(welcome) != nil {
		return false
	}
	return localipc.WriteFrame(c.conn, raw) == nil
}
func (c *connection) run() {
	if !c.handshake(false) {
		return
	}
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		for {
			select {
			case <-c.ctx.Done():
				return
			case message := <-c.output:
				if message.flushed != nil {
					close(message.flushed)
					continue
				}
				if err := c.write(message); err != nil {
					c.cancel()
					_ = c.conn.Close()
					return
				}
			}
		}
	}()
	for {
		raw, err := localipc.ReadFrame(c.conn, false)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.protocolFailure()
			}
			return
		}
		c.mu.Lock()
		message, err := c.decoder.Decode(raw)
		c.mu.Unlock()
		var refusal *launcher.RequestRefusal
		if err != nil && !errors.As(err, &refusal) {
			c.protocolFailure()
			return
		}
		request, ok := message.(launcher.Request)
		if !ok {
			c.protocolFailure()
			return
		}
		if refusal != nil {
			c.respond(request, refusal.Code, nil)
			continue
		}
		c.dispatch(request)
	}
}
func (c *connection) protocolFailure() {
	status := c.host.Status()
	_ = c.write(outbound{state: &launcher.HostState{HostID: status.HostID, State: status.State, LifecycleRevision: status.LifecycleRevision, Code: launcher.ProtocolError}})
}
func (c *connection) enqueue(message outbound) {
	select {
	case <-c.ctx.Done():
		return
	default:
	}
	select {
	case c.output <- message:
	default:
		c.cancel()
		_ = c.conn.Close()
	}
}
func (c *connection) write(message outbound) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	envelope := launcher.Envelope{Version: launcher.DraftVersion, ConnectionID: c.id, Seq: c.next}
	var frame any
	if message.response != nil {
		envelope.Kind = "response"
		message.response.Envelope = envelope
		frame = *message.response
	} else {
		envelope.Kind = "host_state"
		message.state.Envelope = envelope
		frame = *message.state
	}
	raw, err := launcher.Encode(frame, message.operation)
	if err != nil {
		return err
	}
	if err := localipc.WriteFrame(c.conn, raw); err != nil {
		return err
	}
	c.next++
	if message.response != nil {
		return c.decoder.SentResponse(*message.response)
	}
	return nil
}
func (c *connection) respond(request launcher.Request, code launcher.Code, result any) {
	c.enqueue(outbound{response: &launcher.Response{RPCID: request.RPCID, Code: code, Last: true, Result: result}, operation: request.Operation})
}

type operationHandler func(*connection, launcher.Request)

// The draft codec's per-row scope/expected/idempotency metadata is authoritative.
// Dispatch remains keyed by operation, not a global mutation/binding switch.
var operations = map[string]operationHandler{
	"host.status": func(c *connection, r launcher.Request) { c.respond(r, launcher.OK, c.host.Status()) },
	"host.watch": func(c *connection, r launcher.Request) {
		c.workers.Add(1)
		go func() {
			defer c.workers.Done()
			status, err := c.host.watch(c.ctx, r.Arguments.(launcher.HostWatch).AfterRevision)
			if err == nil {
				c.respond(r, launcher.OK, status)
			}
		}()
	},
	"host.shutdown": func(c *connection, r launcher.Request) {
		code, status, start := c.host.shutdown(r)
		c.respond(r, code, status)
		if start {
			go c.host.drain()
		}
	},
}

func (c *connection) dispatch(request launcher.Request) {
	if _, ok := launcher.LookupOperation(request.Operation); !ok {
		c.respond(request, launcher.InvalidCommand, nil)
		return
	}
	if handler, ok := operations[request.Operation]; ok {
		handler(c, request)
		return
	}
	if c.host.Status().State != "ready" {
		c.respond(request, launcher.NotReady, nil)
		return
	}
	// Session operations acquire their real Core dispatcher in S3.
	c.respond(request, launcher.InvalidCommand, nil)
}
