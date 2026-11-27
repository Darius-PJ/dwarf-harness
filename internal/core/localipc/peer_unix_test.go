//go:build linux || darwin

package localipc

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "--foreign-peer" {
		c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: os.Args[2], Net: "unix"})
		if err != nil {
			os.Exit(0)
		}
		defer c.Close()
		hello := launcher.Hello{Kind: "hello", Versions: []int64{launcher.DraftVersion}, Profile: "base", ColonyID: uuid.MustParse(os.Args[3]), ConnectionID: uuid.NewV7()}
		raw, err := launcher.Encode(hello, "")
		if err != nil {
			os.Exit(2)
		}
		conn := &Conn{stream: c}
		if WriteFrame(conn, raw) != nil {
			os.Exit(0)
		}
		raw, err = ReadFrame(conn, true)
		if err != nil {
			os.Exit(0)
		}
		message, err := launcher.Decode(raw, "")
		if err != nil {
			os.Exit(2)
		}
		if _, ok := message.(launcher.Welcome); ok {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLocalIPCOwnerAuthentication(t *testing.T) {
	t.Parallel()
	owner, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	colony, host := uuid.NewV7(), uuid.NewV7()
	listener, err := owner.Listen(colony, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.Remove(filepath.Dir(listener.Endpoint())) })
	endpoint := listener.Endpoint()
	for _, row := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Dir(filepath.Dir(endpoint)), 0700}, {filepath.Dir(endpoint), 0700}, {endpoint, 0600},
	} {
		info, err := os.Lstat(row.path)
		if err != nil || info.Mode().Perm() != row.mode {
			t.Fatalf("private endpoint mode: %v %v", info, err)
		}
	}
	serve := func() chan error {
		done := make(chan error, 1)
		go func() {
			c, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			defer c.Close()
			if c.BeforeHello() != nil {
				raw, _ := launcher.Encode(launcher.Refused{Kind: "refused", Code: launcher.Unauthorized}, "")
				done <- WriteFrame(c, raw)
				return
			}
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
			peer, err := c.Authenticate()
			if err != nil {
				done <- err
				return
			}
			raw, err = launcher.Encode(launcher.Welcome{Kind: "welcome", Version: launcher.DraftVersion, Profile: "base", ConnectionID: hello.ConnectionID, HostID: host, ColonyID: colony, Principal: peer, State: "booting", LifecycleRevision: 1, MaxFrameBytes: launcher.MaxFrameBytes}, "")
			if err == nil {
				err = WriteFrame(c, raw)
			}
			done <- err
		}()
		return done
	}
	t.Run("owner", func(t *testing.T) {
		done := serve()
		c, err := owner.Dial(Discovery{ColonyID: colony, HostID: host, Endpoint: endpoint, Transport: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		raw, err := launcher.Encode(launcher.Hello{Kind: "hello", Versions: []int64{launcher.DraftVersion}, Profile: "base", ColonyID: colony, ConnectionID: uuid.NewV7()}, "")
		if err == nil {
			err = WriteFrame(c, raw)
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err = ReadFrame(c, true)
		if err != nil {
			t.Fatal(err)
		}
		message, err := launcher.Decode(raw, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := message.(launcher.Welcome); !ok {
			t.Fatalf("owner received %T", message)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("foreign_uid", func(t *testing.T) {
		if os.Geteuid() == 65534 || exec.Command("sudo", "-n", "-u", "nobody", "true").Run() != nil {
			t.Skip("foreign UID unavailable: noninteractive sudo is not permitted")
		}
		dir, err := os.MkdirTemp("/tmp", "dh-peer-test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		helper := filepath.Join(dir, "peer-test")
		target, err := os.OpenFile(helper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(target, source)
		if err := errors.Join(copyErr, source.Close(), target.Close()); err != nil {
			t.Fatal(err)
		}
		done := serve()
		err = exec.Command("sudo", "-n", "-u", "nobody", helper, "--foreign-peer", endpoint, colony.String(), host.String()).Run()
		_ = listener.Close()
		<-done
		if err != nil {
			t.Fatal("foreign UID received a welcome or helper failed", err)
		}
	})
	_ = listener.Close()
	t.Run("broad_directory", func(t *testing.T) {
		directory := filepath.Dir(endpoint)
		if err := os.Chmod(directory, 0770); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(directory, 0700)
		if other, err := owner.Listen(colony, uuid.NewV7()); !errors.Is(err, ErrPrivate) {
			if other != nil {
				other.Close()
			}
			t.Fatalf("broad directory accepted: %v", err)
		}
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0770 {
			t.Fatal("unsafe directory was repaired", err)
		}
	})
	t.Run("symlink_directory", func(t *testing.T) {
		other := uuid.NewV7()
		address, _, err := owner.address(other, host)
		if err != nil {
			t.Fatal(err)
		}
		directory := filepath.Dir(address)
		target := t.TempDir()
		if err := os.Symlink(target, directory); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(directory)
		if l, err := owner.Listen(other, host); !errors.Is(err, ErrPrivate) {
			if l != nil {
				l.Close()
			}
			t.Fatalf("symlink accepted: %v", err)
		}
		info, err := os.Lstat(directory)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("symlink was repaired", err)
		}
	})
}
