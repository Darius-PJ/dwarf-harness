//go:build linux || darwin

package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"golang.org/x/sys/unix"
)

type Owner struct{ uid uint32 }

func CurrentOwner() (*Owner, error) { return &Owner{uint32(os.Geteuid())}, nil }

func (o *Owner) checkInfo(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != o.uid || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || info.Mode().Perm()&0077 != 0 {
		return ErrPrivate
	}
	if !directory && !info.Mode().IsRegular() {
		return ErrPrivate
	}
	return nil
}
func (o *Owner) privateDir(path string, create bool) (os.FileInfo, error) {
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := o.checkInfo(before, true); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	after, statErr := f.Stat()
	err = errors.Join(statErr, f.Close())
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, ErrPrivate
	}
	return after, nil
}
func (o *Owner) openPrivate(path string, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if create {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil {
		err = o.checkInfo(info, false)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
func replaceDiscovery(from, to string) error { return os.Rename(from, to) }
func (o *Owner) address(colony, host uuid.UUID) (string, string, error) {
	base, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return "", "", err
	}
	address := filepath.Join(base, "dh-"+strconv.FormatUint(uint64(o.uid), 10), colony.String(), "s")
	if len(address) >= socketPathLimit {
		return "", "", ErrUnsupported
	}
	return address, "unix", nil
}

type socketListener struct {
	listener *net.UnixListener
	owner    *Owner
	path     string
	identity os.FileInfo
}

func (o *Owner) Listen(colony, host uuid.UUID) (Listener, error) {
	path, _, err := o.address(colony, host)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if _, err := o.privateDir(dir, true); err != nil {
			return nil, err
		}
	}
	if info, err := os.Lstat(path); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != o.uid || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
			return nil, ErrPrivate
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	fail := func(err error) (Listener, error) { _ = listener.Close(); return nil, err }
	if err := os.Chmod(path, 0600); err != nil {
		return fail(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != o.uid || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return fail(ErrPrivate)
	}
	return &socketListener{listener, o, path, info}, nil
}
func (l *socketListener) Endpoint() string { return l.path }
func (l *socketListener) Accept() (*Conn, error) {
	c, err := l.listener.AcceptUnix()
	if err != nil {
		return nil, err
	}
	principal, err := l.owner.peer(c)
	return &Conn{stream: c, principal: principal, before: err, beforeChecked: true}, nil
}
func (l *socketListener) StopAccept() error {
	err := l.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
func (l *socketListener) Close() error {
	err := l.StopAccept()
	info, statErr := os.Lstat(l.path)
	if statErr == nil && os.SameFile(l.identity, info) {
		err = errors.Join(err, os.Remove(l.path))
	}
	return err
}
func (o *Owner) Dial(d Discovery) (*Conn, error) {
	path, transport, err := o.address(d.ColonyID, d.HostID)
	if err != nil {
		return nil, err
	}
	if d.Endpoint != path || d.Transport != transport {
		return nil, ErrPrivate
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if _, err := o.privateDir(dir, false); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), Guard())
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	c := raw.(*net.UnixConn)
	principal, err := o.peer(c)
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return &Conn{stream: c, principal: principal, beforeChecked: true}, nil
}
func (o *Owner) peer(c *net.UnixConn) (launcher.Principal, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return launcher.Principal{}, ErrUnauthorized
	}
	var uid uint32
	var queryErr error
	err = raw.Control(func(fd uintptr) { uid, queryErr = peerUID(int(fd)) })
	if err != nil || queryErr != nil || uid != o.uid {
		return launcher.Principal{}, ErrUnauthorized
	}
	return launcher.Principal{Kind: "unix_uid", UID: &uid}, nil
}
