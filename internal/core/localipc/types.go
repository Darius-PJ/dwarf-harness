package localipc

import (
	"bytes"
	"errors"
	"io"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
)

var (
	ErrUnauthorized = errors.New("localipc: unauthorized")
	ErrUnavailable  = errors.New("localipc: unavailable")
	ErrUnsupported  = errors.New("localipc: unsupported")
	ErrPrivate      = errors.New("localipc: private endpoint required")
)

// guardOverride is set only by tests of expiry, never by ordinary fixtures.
var guardOverride func() time.Duration

func Guard() time.Duration {
	if guardOverride != nil {
		return guardOverride()
	}
	return 5 * time.Second
}

type stream interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// Conn owns one native connection. Unix peers are checked at accept; Windows
// peers are checked after reading hello, before any reply or Core effect.
type Conn struct {
	stream
	peer          func() (launcher.Principal, error)
	before        error
	principal     launcher.Principal
	beforeChecked bool
}

func (c *Conn) BeforeHello() error { return c.before }
func (c *Conn) Authenticate() (launcher.Principal, error) {
	if c.beforeChecked {
		return c.principal, c.before
	}
	return c.peer()
}

type Listener interface {
	Accept() (*Conn, error)
	// StopAccept retires pending accepts without closing connected streams.
	StopAccept() error
	Close() error
	Endpoint() string
}

// ReadFrame leaves idle connections alone, but bounds an incomplete frame from
// its first byte. A handshake uses the full guard, including its first byte.
func ReadFrame(c *Conn, handshake bool) ([]byte, error) {
	deadline := time.Time{}
	if handshake {
		deadline = time.Now().Add(Guard())
	}
	if err := c.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	var first [1]byte
	if _, err := io.ReadFull(c, first[:]); err != nil {
		return nil, err
	}
	if !handshake {
		if err := c.SetReadDeadline(time.Now().Add(Guard())); err != nil {
			return nil, err
		}
	}
	return launcher.ReadFrame(io.MultiReader(bytes.NewReader(first[:]), c))
}
func WriteFrame(c *Conn, raw []byte) error {
	if err := c.SetWriteDeadline(time.Now().Add(Guard())); err != nil {
		return err
	}
	return launcher.WriteFrame(c, raw)
}

// Discovery is an advisory address, never a writer-lock or readiness proof.
type Discovery struct {
	Format           string    `json:"format"`
	Version          int       `json:"version"`
	ColonyID         uuid.UUID `json:"colony_id"`
	HostID           uuid.UUID `json:"host_id"`
	Transport        string    `json:"transport"`
	Endpoint         string    `json:"endpoint"`
	ProtocolVersions []int64   `json:"protocol_versions"`
}
