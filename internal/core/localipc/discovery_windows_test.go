package localipc_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/host"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
	"golang.org/x/sys/windows"
)

func TestRunDirectoryLogonRenewal(t *testing.T) {
	t.Parallel()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	var logon string
	for _, group := range groups.AllGroups() {
		if group.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			logon = group.Sid.String()
		}
	}
	if logon == "" {
		t.Fatal("owner token has no logon SID")
	}
	previous := "S-1-5-5-0-1"
	if previous == logon {
		previous = "S-1-5-5-0-2"
	}
	descriptor := func(aces string) *windows.SECURITY_DESCRIPTOR {
		t.Helper()
		sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P" + aces)
		if err != nil {
			t.Fatal(err)
		}
		return sd
	}
	current := descriptor("(A;;GA;;;" + logon + ")")
	old := descriptor("(A;;GA;;;" + previous + ")")
	setDACL := func(handle windows.Handle, sd *windows.SECURITY_DESCRIPTOR) {
		t.Helper()
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
		runtime.KeepAlive(sd)
		if err != nil {
			t.Fatal(err)
		}
	}
	openSecurity := func(path string, access uint32) windows.Handle {
		t.Helper()
		if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
			path = path[4:]
		} else if strings.HasPrefix(path, `\\`) {
			path = `UNC\` + path[2:]
		}
		name, err := windows.NewNTUnicodeString(`\??\` + path)
		if err != nil {
			t.Fatal(err)
		}
		attributes := windows.OBJECT_ATTRIBUTES{
			Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
			ObjectName: name,
			Attributes: windows.OBJ_CASE_INSENSITIVE,
		}
		var h windows.Handle
		var status windows.IO_STATUS_BLOCK
		err = windows.NtCreateFile(&h, access, &attributes, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT, 0, 0)
		runtime.KeepAlive(name)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	security := func(path string) string {
		t.Helper()
		h := openSecurity(path, windows.READ_CONTROL)
		defer windows.CloseHandle(h)
		sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		return sd.String()
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "colony")
	store, err := ledgerstore.Create(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	colony := store.Colony()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "run")
	name, err := windows.UTF16PtrFromString(run)
	if err != nil {
		t.Fatal(err)
	}
	attributes := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: current}
	if err := windows.CreateDirectory(name, attributes); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(current)
	runHandle := openSecurity(run, windows.READ_CONTROL|windows.WRITE_DAC)
	t.Cleanup(func() { _ = windows.CloseHandle(runHandle) })
	t.Cleanup(func() { setDACL(runHandle, current) })
	pending := filepath.Join(run, "host.json.pending")
	pendingName, err := windows.UTF16PtrFromString(pending)
	if err != nil {
		t.Fatal(err)
	}
	pendingHandle, err := windows.CreateFile(pendingName, windows.READ_CONTROL|windows.WRITE_DAC, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	runtime.KeepAlive(current)
	if err != nil {
		t.Fatal(err)
	}
	setDACL(pendingHandle, old)
	if err := windows.CloseHandle(pendingHandle); err != nil {
		t.Fatal(err)
	}
	setDACL(runHandle, old)
	beforeRun, beforePending := security(run), security(pending)
	owner, err := localipc.CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ReadDiscovery(root, colony); err == nil {
		t.Fatal("old-session discovery was readable")
	}
	if control, err := host.Connect(root); err == nil {
		_ = control.Close()
		t.Fatal("read-only connect accepted old-session discovery")
	}
	if probe, err := host.Probe(root); err != nil || probe.Kind != "no_host" {
		t.Fatalf("read-only probe: %+v %v", probe, err)
	}
	if security(run) != beforeRun || security(pending) != beforePending {
		t.Fatal("read-only discovery, connect or probe repaired an old-session ACL")
	}
	candidate, failure := host.NewCandidate(root)
	if failure != nil {
		t.Fatal("candidate could not renew owner-only run directory and pending file", failure)
	}
	active := true
	t.Cleanup(func() {
		if active {
			_ = candidate.Abort()
		}
	})
	// ReadDiscovery verifies both native descriptors, not just the JSON hint.
	discovery, err := owner.ReadDiscovery(root, colony)
	if err != nil || discovery.HostID != candidate.Status().HostID {
		t.Fatal("renewed candidate discovery did not verify", err)
	}
	if after := security(run); !strings.Contains(after, ";"+logon+")") || strings.Contains(after, ";"+previous+")") {
		t.Fatal("run directory retained the previous logon SID")
	}
	control, err := host.Connect(root)
	if err != nil {
		t.Fatal("renewed candidate endpoint did not authenticate", err)
	}
	if control.Welcome.HostID != candidate.Status().HostID {
		t.Fatal("connected to another candidate")
	}
	_ = control.Close()
	if err := candidate.Abort(); err != nil {
		t.Fatal(err)
	}
	active = false
	// The same stale logon ACE is not repairable when access was broadened.
	setDACL(runHandle, descriptor("(A;;GA;;;"+previous+")(A;;GA;;;WD)"))
	before := security(run)
	candidate, failure = host.NewCandidate(root)
	if candidate != nil {
		_ = candidate.Abort()
		t.Fatal("candidate repaired a directory with an Everyone ACE")
	}
	if failure == nil || failure.Stage != "host endpoint" {
		t.Fatalf("broadened directory was not refused at the endpoint stage: %+v", failure)
	}
	if security(run) != before {
		t.Fatal("refused directory DACL changed")
	}
}
