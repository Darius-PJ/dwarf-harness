package winexec

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const pipeBufferSize = 32 * 1024

type pipeGroup struct {
	mu            sync.Mutex
	limit         uint64
	out, errOut   []byte
	input         []byte
	overflow      bool
	frozen        bool
	done          int
	complete      [3]bool
	failure       error
	event         chan struct{}
	stop          chan struct{}
	stopOnce      sync.Once
	parent, child [3]windows.Handle
}

func newPipes(owner, sid, id string, limit uint64) (*pipeGroup, error) {
	p := &pipeGroup{limit: limit, event: make(chan struct{}, 1), stop: make(chan struct{})}
	fail := func(err error) (*pipeGroup, error) {
		return nil, errors.Join(err, p.closeUnstarted())
	}
	sd, err := securityDescriptor(owner, sid, "FA")
	if err != nil {
		return nil, err
	}
	for i := range p.parent {
		name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\LOCAL\dwarf-harness-%s-%d`, id, i))
		if err != nil {
			return fail(err)
		}
		access := uint32(windows.PIPE_ACCESS_INBOUND)
		clientAccess := uint32(windows.GENERIC_WRITE)
		if i == 0 {
			access = windows.PIPE_ACCESS_OUTBOUND
			clientAccess = windows.GENERIC_READ
		}
		serverSA := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
		h, err := windows.CreateNamedPipe(name, access|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, pipeBufferSize, pipeBufferSize, 0, &serverSA)
		runtime.KeepAlive(sd)
		if err != nil {
			return fail(nativeError("CreateNamedPipe", err))
		}
		p.parent[i] = h
		clientSA := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
		child, err := windows.CreateFile(name, clientAccess, 0, &clientSA, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return fail(nativeError("CreateFile.pipe", err))
		}
		p.child[i] = child
		// Opening our sole client first makes ConnectNamedPipe synchronous. No
		// pending connect is allowed to outlive this stack-owned OVERLAPPED.
		event, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			return fail(nativeError("CreateEvent.connect", err))
		}
		ov := windows.Overlapped{HEvent: event}
		var pin runtime.Pinner
		pin.Pin(&ov)
		err = windows.ConnectNamedPipe(h, &ov)
		if errors.Is(err, windows.ERROR_IO_PENDING) {
			// With the client already open this is an unexpected native contract.
			// Transfer the pinned operation to its retiring owner before returning.
			p.parent[i] = 0
			go func() {
				p.cancel()
				_, _ = p.await(h, &ov)
				_ = windows.CloseHandle(h)
				_ = windows.CloseHandle(event)
				pin.Unpin()
			}()
			return fail(fmt.Errorf("%w: unexpected pending pipe connection", ErrQuarantined))
		}
		pin.Unpin()
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		closeErr := closeOwnedHandle("CloseHandle.connect-event", event)
		if closeErr != nil {
			return fail(errors.Join(nativeError("ConnectNamedPipe", err), closeErr))
		}
		if err != nil {
			return fail(nativeError("ConnectNamedPipe", err))
		}
	}
	return p, nil
}
func (p *pipeGroup) closeChildren() error {
	var err error
	for i, h := range p.child {
		if h != 0 {
			err = errors.Join(err, closeOwnedHandle("CloseHandle.child-pipe", h))
			p.child[i] = 0
		}
	}
	return err
}
func (p *pipeGroup) closeUnstarted() error {
	err := p.closeChildren()
	for i, h := range p.parent {
		if h != 0 {
			err = errors.Join(err, closeOwnedHandle("CloseHandle.parent-pipe", h))
			p.parent[i] = 0
		}
	}
	return err
}
func (p *pipeGroup) cancel() {
	p.stopOnce.Do(func() { close(p.stop) })
	p.mu.Lock()
	p.input = nil
	p.mu.Unlock()
}
func (p *pipeGroup) signal() {
	select {
	case p.event <- struct{}{}:
	default:
	}
}
func (p *pipeGroup) start(input []byte) {
	p.input = input
	for i := range p.parent {
		go p.pump(i)
	}
}
func (p *pipeGroup) finish(index int, complete bool, err error) {
	p.mu.Lock()
	if index == 0 {
		p.input = nil
	}
	p.done++
	p.complete[index] = complete
	p.failure = errors.Join(p.failure, err)
	p.mu.Unlock()
	p.signal()
}
func (p *pipeGroup) noteFailure(err error) {
	p.mu.Lock()
	p.failure = errors.Join(p.failure, err)
	p.mu.Unlock()
	p.signal()
}
func (p *pipeGroup) append(index int, data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return true
	}
	used := uint64(len(p.out)) + uint64(len(p.errOut))
	remaining := p.limit - used
	if uint64(len(data)) > remaining {
		data = data[:int(remaining)]
		p.overflow = true
	}
	if index == 1 {
		p.out = append(p.out, data...)
	} else {
		p.errOut = append(p.errOut, data...)
	}
	p.signal()
	return p.overflow
}
func (p *pipeGroup) snapshot() (done int, sealed, overflow bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done, p.complete[0] && p.complete[1] && p.complete[2], p.overflow
}
func (p *pipeGroup) takeFailure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.failure
	p.failure = nil
	return err
}
func (p *pipeGroup) output() ([]byte, []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.out...), append([]byte(nil), p.errOut...)
}
func (p *pipeGroup) freezeOutput() ([]byte, []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frozen = true
	// Transfer ownership so a retiring operation cannot retain the receipt's
	// variable-size storage. After frozen, append cannot touch these arrays.
	out, errOut := p.out, p.errOut
	p.out, p.errOut = nil, nil
	return out, errOut
}
func (p *pipeGroup) pump(index int) {
	h := p.parent[index]
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		p.finish(index, false, errors.Join(nativeError("CreateEvent.pipe", err), closeOwnedHandle("CloseHandle.parent-pipe", h)))
		return
	}
	buffer := make([]byte, pipeBufferSize)
	ov := windows.Overlapped{HEvent: event}
	// x/sys's race wrappers require a nonnil count even for overlapped IO.
	// Retain it with the operation; only GetOverlappedResult supplies proof.
	var transferred uint32
	var pin runtime.Pinner
	pin.Pin(&buffer[0])
	pin.Pin(&ov)
	pin.Pin(&transferred)
	complete := false
	defer func() {
		pin.Unpin()
		err = errors.Join(err, closeOwnedHandle("CloseHandle.parent-pipe", h), closeOwnedHandle("CloseHandle.pipe-event", event))
		p.finish(index, complete, err)
	}()
	position := 0
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		length := len(buffer)
		if index == 0 {
			p.mu.Lock()
			if position >= len(p.input) {
				p.mu.Unlock()
				complete = true
				return
			}
			length = copy(buffer, p.input[position:])
			p.mu.Unlock()
		}
		ov = windows.Overlapped{HEvent: event}
		if err = windows.ResetEvent(event); err != nil {
			err = nativeError("ResetEvent.pipe", err)
			return
		}
		var n uint32
		transferred = 0
		if index == 0 {
			err = windows.WriteFile(h, buffer[:length], &transferred, &ov)
		} else {
			err = windows.ReadFile(h, buffer, &transferred, &ov)
		}
		if err == nil {
			err = windows.GetOverlappedResult(h, &ov, &n, false)
		}
		if errors.Is(err, windows.ERROR_IO_PENDING) || errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
			n, err = p.await(h, &ov)
		}
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_HANDLE_EOF) {
			// This completes channel ownership, not a claim that stdin was
			// consumed. An actual early child close is ordinary broken-pipe EOF.
			complete = true
			err = nil
			return
		}
		if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
			select {
			case <-p.stop:
				err = nil
			default:
				err = nativeError("overlapped stdio", err)
			}
			return
		}
		if err != nil {
			err = nativeError("overlapped stdio", err)
			return
		}
		if index == 0 {
			if n == 0 {
				err = fmt.Errorf("%w: zero-length stdin write", ErrUnsupported)
				return
			}
			position += int(n)
		} else {
			if n == 0 {
				complete = true
				return
			}
			if p.append(index, buffer[:n]) {
				return
			}
		}
	}
}

// await returns only after observed native completion. A failed wait/query is
// not completion: the caller retains its pinned OVERLAPPED, event and fixed
// buffer while Run can independently exhaust StopGrace and quarantine.
func (p *pipeGroup) await(handle windows.Handle, ov *windows.Overlapped) (uint32, error) {
	canceled, reported := false, false
	for {
		select {
		case <-p.stop:
			if !canceled {
				canceled = true
				if err := windows.CancelIoEx(handle, ov); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
					p.noteFailure(nativeError("CancelIoEx.pipe", err))
				}
			}
		default:
		}
		state, waitErr := windows.WaitForSingleObject(ov.HEvent, 10)
		if waitErr == nil && state == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		var n uint32
		resultErr := windows.GetOverlappedResult(handle, ov, &n, false)
		if resultErr == nil {
			return n, nil
		}
		if waitErr == nil && state == windows.WAIT_OBJECT_0 && !errors.Is(resultErr, windows.ERROR_IO_INCOMPLETE) {
			return n, resultErr
		}
		if !reported {
			reported = true
			p.noteFailure(errors.Join(nativeError("WaitForSingleObject.pipe", waitErr), nativeError("GetOverlappedResult.pipe", resultErr)))
			p.cancel()
		}
		// A broken event or contradictory native state cannot become a hot
		// loop, nor permission to free an operation still owned by Windows.
		<-time.After(10 * time.Millisecond)
	}
}
