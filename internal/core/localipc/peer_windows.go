package localipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"golang.org/x/sys/windows"
)

type Owner struct {
	user, logon string
	descriptor  *windows.SECURITY_DESCRIPTOR
}

func tokenBytes(token windows.Token, class uint32) ([]byte, error) {
	var size uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size == 0 {
		return nil, ErrUnauthorized
	}
	data := make([]byte, size)
	if err := windows.GetTokenInformation(token, class, &data[0], size, &size); err != nil {
		return nil, ErrUnauthorized
	}
	return data, nil
}
func tokenIdentity(token windows.Token, impersonated bool) (user, logon, authentication string, err error) {
	denied := func() (string, string, string, error) { return "", "", "", ErrUnauthorized }
	restricted, e := token.IsRestricted()
	if e != nil || restricted {
		return denied()
	}
	data, e := tokenBytes(token, 29) // TokenIsAppContainer, absent from pinned x/sys constants.
	if e != nil || len(data) < 4 || *(*uint32)(unsafe.Pointer(&data[0])) != 0 {
		return denied()
	}
	if impersonated {
		data, e = tokenBytes(token, windows.TokenImpersonationLevel)
		if e != nil || len(data) < 4 || *(*uint32)(unsafe.Pointer(&data[0])) < windows.SecurityIdentification {
			return denied()
		}
	}
	u, e := token.GetTokenUser()
	if e != nil {
		return denied()
	}
	user = u.User.Sid.String()
	data, e = tokenBytes(token, windows.TokenLogonSid)
	if e != nil || len(data) < int(unsafe.Sizeof(windows.Tokengroups{})) {
		return denied()
	}
	groups := (*windows.Tokengroups)(unsafe.Pointer(&data[0]))
	if groups.GroupCount != 1 || groups.Groups[0].Attributes&windows.SE_GROUP_LOGON_ID != windows.SE_GROUP_LOGON_ID {
		return denied()
	}
	logon = groups.Groups[0].Sid.String()
	runtime.KeepAlive(data)
	data, e = tokenBytes(token, windows.TokenStatistics)
	if e != nil || len(data) < 16 {
		return denied()
	}
	stats := (*struct{ TokenID, AuthenticationID windows.LUID })(unsafe.Pointer(&data[0]))
	authentication = fmt.Sprintf("%08x%08x", uint32(stats.AuthenticationID.HighPart), stats.AuthenticationID.LowPart)
	return user, logon, authentication, nil
}
func CurrentOwner() (*Owner, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, ErrUnauthorized
	}
	defer token.Close()
	user, logon, _, err := tokenIdentity(token, false)
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user + "D:P(A;;GA;;;" + logon + ")")
	if err != nil {
		return nil, ErrPrivate
	}
	return &Owner{user, logon, sd}, nil
}
func (o *Owner) checkToken(token windows.Token, impersonated bool) (launcher.Principal, error) {
	user, logon, auth, err := tokenIdentity(token, impersonated)
	if err != nil || user != o.user || logon != o.logon {
		return launcher.Principal{}, ErrUnauthorized
	}
	return launcher.Principal{Kind: "windows_logon", UserSID: user, AuthenticationID: auth}, nil
}

var impersonatePipe = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

func (o *Owner) clientPeer(f *os.File) (principal launcher.Principal, err error) {
	raw, e := f.SyscallConn()
	if e != nil {
		return principal, ErrUnauthorized
	}
	err = raw.Control(func(fd uintptr) {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		ok, _, _ := impersonatePipe.Call(fd)
		if ok == 0 {
			e = ErrUnauthorized
			return
		}
		defer func() {
			if windows.RevertToSelf() != nil {
				os.Exit(1)
			}
		}()
		var token windows.Token
		if windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token) != nil {
			e = ErrUnauthorized
			return
		}
		defer token.Close()
		principal, e = o.checkToken(token, true)
	})
	if err != nil || e != nil {
		return launcher.Principal{}, ErrUnauthorized
	}
	return principal, nil
}
func (o *Owner) serverPeer(f *os.File) (principal launcher.Principal, err error) {
	raw, e := f.SyscallConn()
	if e != nil {
		return principal, ErrUnauthorized
	}
	err = raw.Control(func(fd uintptr) {
		var pid uint32
		if windows.GetNamedPipeServerProcessId(windows.Handle(fd), &pid) != nil {
			e = ErrUnauthorized
			return
		}
		process, openErr := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if openErr != nil {
			e = ErrUnauthorized
			return
		}
		defer windows.CloseHandle(process)
		var token windows.Token
		if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
			e = ErrUnauthorized
			return
		}
		defer token.Close()
		principal, e = o.checkToken(token, false)
	})
	if err != nil || e != nil {
		return launcher.Principal{}, ErrUnauthorized
	}
	return principal, nil
}

func (o *Owner) security(h windows.Handle) error {
	sid, err := o.privateAccessSID(h)
	if err != nil || sid != o.logon {
		return ErrPrivate
	}
	return nil
}

// privateAccessSID checks the entire owner-only shape without requiring that
// its one ACE belongs to this sign-in session.
func (o *Owner) privateAccessSID(h windows.Handle) (string, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", ErrPrivate
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || owner.String() != o.user {
		return "", ErrPrivate
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return "", ErrPrivate
	}
	acl, defaulted, err := sd.DACL()
	if err != nil || defaulted || acl == nil || acl.AceCount != 1 {
		return "", ErrPrivate
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	if windows.GetAce(acl, 0, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
		return "", ErrPrivate
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	name := sid.String()
	runtime.KeepAlive(sd)
	return name, nil
}
func (o *Owner) attributes() *windows.SecurityAttributes {
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: o.descriptor}
}

// renewLogon is used only by Claim-held directory preparation and pending-file
// publication. NtCreateFile avoids Win32's implicit SYNCHRONIZE request, which
// the owner's READ_CONTROL/WRITE_DAC rights cannot satisfy under the old DACL.
// FILE_READ_ATTRIBUTES is needed to reject reparse points before any repair.
func (o *Owner) renewLogon(path string, directory bool) (err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		path = path[4:]
	} else if strings.HasPrefix(path, `\\`) {
		path = `UNC\` + path[2:]
	}
	name, err := windows.NewNTUnicodeString(`\??\` + path)
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		ObjectName: name,
		Attributes: windows.OBJ_CASE_INSENSITIVE,
	}
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES, &attributes, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	runtime.KeepAlive(name)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			return status.Errno()
		}
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return ErrPrivate
	}
	previous, err := o.privateAccessSID(h)
	if err != nil {
		return err
	}
	if previous == o.logon {
		return nil
	}
	sid, err := windows.StringToSid(previous)
	if err != nil || !sid.IsWellKnown(windows.WinLogonIdsSid) {
		return ErrPrivate
	}
	acl, _, err := o.descriptor.DACL()
	if err != nil {
		return ErrPrivate
	}
	err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(o.descriptor)
	if err != nil {
		return err
	}
	return o.security(h)
}

func (o *Owner) preparePending(path string) (*os.File, error) {
	if err := o.renewLogon(path, false); err != nil {
		return nil, err
	}
	return o.openPrivate(path, false)
}

func (o *Owner) privateDir(path string, create bool) (os.FileInfo, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	if create {
		err = windows.CreateDirectory(name, o.attributes())
		runtime.KeepAlive(o.descriptor)
		if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, err
		}
		// Only PrepareRun requests creation here, after ClaimColony succeeds.
		if err := o.renewLogon(path, true); err != nil {
			return nil, err
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrPrivate
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	after, statErr := f.Stat()
	err = errors.Join(statErr, o.security(h), rejectReparse(h), f.Close())
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, ErrPrivate
	}
	return after, nil
}
func rejectReparse(h windows.Handle) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrPrivate
	}
	return nil
}
func (o *Owner) openPrivate(path string, create bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL)
	disposition := uint32(windows.OPEN_EXISTING)
	if create {
		access = windows.GENERIC_WRITE | windows.READ_CONTROL
		disposition = windows.CREATE_NEW
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, o.attributes(), disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(o.descriptor)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = ErrPrivate
	}
	err = errors.Join(err, rejectReparse(h), o.security(h))
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
func replaceDiscovery(from, to string) error {
	a, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING)
}
