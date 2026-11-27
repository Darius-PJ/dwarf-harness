package localipc

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"golang.org/x/sys/windows"
)

func (o *Owner) address(colony, host uuid.UUID) (string, string, error) {
	return `\\.\pipe\LOCAL\dwarf-harness-` + colony.String() + "-" + host.String(), "pipe", nil
}

type pipeListener struct {
	mu             sync.Mutex
	owner          *Owner
	path           string
	pending, guard windows.Handle
	closed         bool
	accepts        sync.WaitGroup
	connections    map[*pipeStream]struct{}
	stopOnce       sync.Once
	stopErr        error
	closeOnce      sync.Once
	closeErr       error
}
type pipeStream struct {
	*os.File
	listener *pipeListener
	once     sync.Once
	err      error
}

func (s *pipeStream) Close() error {
	s.once.Do(func() {
		s.err = s.File.Close()
		s.listener.mu.Lock()
		delete(s.listener.connections, s)
		s.listener.mu.Unlock()
	})
	return s.err
}
func (o *Owner) newPipe(path string, first bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	access := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		access |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	h, err := windows.CreateNamedPipe(name, access, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, windows.PIPE_UNLIMITED_INSTANCES, 65536, 65536, 0, o.attributes())
	runtime.KeepAlive(o.descriptor)
	if err != nil {
		return 0, err
	}
	if err := o.security(h); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(h))
	}
	return h, nil
}
func (o *Owner) Listen(colony, host uuid.UUID) (Listener, error) {
	path, _, _ := o.address(colony, host)
	h, err := o.newPipe(path, true)
	if err != nil {
		return nil, err
	}
	var guard windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &guard, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(h))
	}
	return &pipeListener{owner: o, path: path, pending: h, guard: guard, connections: map[*pipeStream]struct{}{}}, nil
}
func (l *pipeListener) Endpoint() string { return l.path }
func (l *pipeListener) Accept() (*Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, os.ErrClosed
	}
	if l.pending == 0 {
		h, err := l.owner.newPipe(l.path, false)
		if err != nil {
			l.mu.Unlock()
			return nil, err
		}
		l.pending = h
	}
	h := l.pending
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		l.mu.Unlock()
		return nil, err
	}
	ov := windows.Overlapped{HEvent: event}
	var pin runtime.Pinner
	pin.Pin(&ov)
	l.accepts.Add(1)
	err = windows.ConnectNamedPipe(h, &ov)
	l.mu.Unlock()
	defer l.accepts.Done()
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		var transferred uint32
		for {
			err = windows.GetOverlappedResult(h, &ov, &transferred, true)
			if !errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
				break
			}
		}
	}
	pin.Unpin()
	eventErr := windows.CloseHandle(event)
	if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		err = nil
	}
	l.mu.Lock()
	l.pending = 0
	if l.closed || err != nil || eventErr != nil {
		if l.closed && err == nil {
			err = os.ErrClosed
		}
		l.mu.Unlock()
		return nil, errors.Join(err, eventErr, windows.CloseHandle(h))
	}
	f, err := pipeFile(h)
	if err != nil {
		l.mu.Unlock()
		return nil, err
	}
	stream := &pipeStream{File: f, listener: l}
	l.connections[stream] = struct{}{}
	l.mu.Unlock()
	return &Conn{stream: stream, peer: func() (p launcher.Principal, e error) { return l.owner.clientPeer(f) }}, nil
}
func pipeFile(h windows.Handle) (*os.File, error) {
	kind, err := windows.GetFileType(h)
	if err != nil || kind != windows.FILE_TYPE_PIPE {
		return nil, errors.Join(ErrUnsupported, windows.CloseHandle(h))
	}
	f := os.NewFile(uintptr(h), "owner-local-pipe")
	if err := f.SetReadDeadline(time.Time{}); err != nil {
		return nil, errors.Join(ErrUnsupported, f.Close())
	}
	return f, nil
}
func (l *pipeListener) StopAccept() error {
	l.stopOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		if l.pending != 0 {
			_ = windows.CancelIoEx(l.pending, nil)
		}
		l.mu.Unlock()
		l.accepts.Wait()
		l.mu.Lock()
		if l.pending != 0 {
			l.stopErr = windows.CloseHandle(l.pending)
			l.pending = 0
		}
		l.mu.Unlock()
	})
	return l.stopErr
}
func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() {
		l.closeErr = l.StopAccept()
		l.mu.Lock()
		conns := make([]*pipeStream, 0, len(l.connections))
		for c := range l.connections {
			conns = append(conns, c)
		}
		l.mu.Unlock()
		for _, c := range conns {
			l.closeErr = errors.Join(l.closeErr, c.Close())
		}
		l.closeErr = errors.Join(l.closeErr, windows.CloseHandle(l.guard))
	})
	return l.closeErr
}
func (o *Owner) Dial(d Discovery) (*Conn, error) {
	path, transport, _ := o.address(d.ColonyID, d.HostID)
	if d.Endpoint != path || d.Transport != transport {
		return nil, ErrPrivate
	}
	return o.dialPipe(path, windows.SECURITY_IDENTIFICATION)
}
func (o *Owner) dialPipe(path string, level uint32) (*Conn, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(Guard())
	var h windows.Handle
	for {
		h, err = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|level, 0)
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			break
		}
		if !time.Now().Before(deadline) {
			return nil, ErrUnavailable
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	f, err := pipeFile(h)
	if err != nil {
		return nil, err
	}
	principal, err := o.serverPeer(f)
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &Conn{stream: f, principal: principal, beforeChecked: true}, nil
}
