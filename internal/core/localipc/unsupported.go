//go:build !windows && !linux && !darwin

package localipc

import (
	"os"
	"uuid"
)

type Owner struct{}

func CurrentOwner() (*Owner, error)                                   { return nil, ErrUnsupported }
func (o *Owner) privateDir(string, bool) (os.FileInfo, error)         { return nil, ErrUnsupported }
func (o *Owner) openPrivate(string, bool) (*os.File, error)           { return nil, ErrUnsupported }
func (o *Owner) address(uuid.UUID, uuid.UUID) (string, string, error) { return "", "", ErrUnsupported }
func (o *Owner) Listen(uuid.UUID, uuid.UUID) (Listener, error)        { return nil, ErrUnsupported }
func (o *Owner) Dial(Discovery) (*Conn, error)                        { return nil, ErrUnsupported }
func replaceDiscovery(string, string) error                           { return ErrUnsupported }
func Spawn([]string) (*os.File, bool, error)                          { return nil, false, ErrUnsupported }
func StartupChannel(string) (*os.File, error)                         { return nil, ErrUnsupported }
