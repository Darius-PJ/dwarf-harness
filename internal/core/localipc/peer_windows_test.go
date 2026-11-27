package localipc

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"unsafe"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"golang.org/x/sys/windows"
)

func TestLocalIPCOwnerAuthentication(t *testing.T) {
	t.Parallel()
	owner, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	colony, host := uuid.NewV7(), uuid.NewV7()
	l, err := owner.Listen(colony, host)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, kind := range []string{"owner", "anonymous", "restricted"} {
		t.Run(kind, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				c, err := l.Accept()
				if err != nil {
					done <- err
					return
				}
				defer c.Close()
				raw, err := ReadFrame(c, true)
				if err != nil {
					done <- err
					return
				}
				message, err := launcher.Decode(raw, "")
				if err != nil {
					done <- err
					return
				}
				hello := message.(launcher.Hello)
				peer, authErr := c.Authenticate()
				var reply any = launcher.Refused{Kind: "refused", Code: launcher.Unauthorized}
				if authErr == nil {
					reply = launcher.Welcome{Kind: "welcome", Version: launcher.DraftVersion, Profile: "base", ConnectionID: hello.ConnectionID, HostID: host, ColonyID: colony, Principal: peer, State: "booting", LifecycleRevision: 1, MaxFrameBytes: launcher.MaxFrameBytes}
				}
				raw, err = launcher.Encode(reply, "")
				if err == nil {
					err = WriteFrame(c, raw)
				}
				done <- err
			}()
			var c *Conn
			switch kind {
			case "owner":
				c, err = owner.dialPipe(l.Endpoint(), windows.SECURITY_IDENTIFICATION)
			case "anonymous":
				c, err = owner.dialPipe(l.Endpoint(), windows.SECURITY_ANONYMOUS)
			case "restricted":
				c, err = restrictedDial(owner, l.Endpoint())
			}
			if kind == "restricted" && err != nil {
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatal("restricted token setup failed", err)
				}
				// A restricted token may be refused by the DACL before hello. Release
				// the real pending accept with an owner connection so it is reaped.
				control, controlErr := owner.dialPipe(l.Endpoint(), windows.SECURITY_IDENTIFICATION)
				if controlErr != nil {
					t.Fatal("owner control could not retire restricted accept", controlErr)
				}
				_ = control.Close()
				<-done
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			hello := launcher.Hello{Kind: "hello", Versions: []int64{launcher.DraftVersion}, Profile: "base", ColonyID: colony, ConnectionID: uuid.NewV7()}
			raw, err := launcher.Encode(hello, "")
			if err == nil {
				err = WriteFrame(c, raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err = ReadFrame(c, true)
			if kind == "restricted" && err != nil {
				<-done
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			message, err := launcher.Decode(raw, "")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "owner" {
				if _, ok := message.(launcher.Welcome); !ok {
					t.Fatalf("owner received %T", message)
				}
			} else {
				refusal, ok := message.(launcher.Refused)
				if !ok || refusal.Code != launcher.Unauthorized {
					t.Fatalf("%s received %#v", kind, message)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func restrictedDial(owner *Owner, path string) (*Conn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var original windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, &original); err != nil {
		return nil, err
	}
	defer original.Close()
	sid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return nil, err
	}
	restriction := windows.SIDAndAttributes{Sid: sid}
	var restricted windows.Token
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")
	ok, _, callErr := proc.Call(uintptr(original), 1, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&restriction)), uintptr(unsafe.Pointer(&restricted)))
	runtime.KeepAlive(sid)
	if ok == 0 {
		return nil, callErr
	}
	defer restricted.Close()
	var impersonation windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &impersonation); err != nil {
		return nil, err
	}
	defer impersonation.Close()
	if err := windows.SetThreadToken(nil, impersonation); err != nil {
		return nil, err
	}
	defer func() {
		if windows.RevertToSelf() != nil {
			os.Exit(1)
		}
	}()
	c, err := owner.dialPipe(path, windows.SECURITY_IDENTIFICATION)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, ErrUnauthorized
	}
	return c, err
}
