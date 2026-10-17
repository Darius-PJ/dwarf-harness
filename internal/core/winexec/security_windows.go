package winexec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ownerSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", nativeError("GetTokenUser", err)
	}
	return u.User.Sid.String(), nil
}

func securityDescriptor(owner, sid, rights string) (*windows.SECURITY_DESCRIPTOR, error) {
	sddl := "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + owner + ")"
	if sid != "" {
		sddl += "(A;OICI;" + rights + ";;;" + sid + ")"
	}
	return windows.SecurityDescriptorFromString(sddl)
}
func securePath(path, owner, sid, rights string) error {
	sd, err := securityDescriptor(owner, sid, rights)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(sd)
	return nativeError("SetNamedSecurityInfo.DACL", err)
}
func lowerLabel(handle windows.Handle) error {
	sd, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if err != nil {
		return err
	}
	acl, _, err := sd.SACL()
	if err != nil {
		return err
	}
	err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, acl)
	runtime.KeepAlive(sd)
	if err != nil {
		return nativeError("SetSecurityInfo.label", err)
	}
	readback, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		return nativeError("GetSecurityInfo.label", err)
	}
	label, _, err := readback.SACL()
	if err != nil {
		return err
	}
	if label == nil {
		return ErrUnsupported
	}
	found := false
	for i := range uint32(label.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(label, i, &ace); err != nil {
			return nativeError("GetAce.label", err)
		}
		if ace.Header.AceType == 0x11 && ace.Mask&1 != 0 && (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String() == "S-1-16-4096" {
			found = true
		}
	}
	runtime.KeepAlive(readback)
	if !found {
		return fmt.Errorf("%w: low no-write-up label not established", ErrUnsupported)
	}
	return nil
}
func hasProfileWrite(handle windows.Handle, sid string) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nativeError("GetSecurityInfo.profile", err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return ErrUnsupported
	}
	found := false
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return nativeError("GetAce.profile", err)
		}
		if ace.Header.AceType == 0 && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 && ace.Mask&windows.FILE_WRITE_DATA != 0 && (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String() == sid {
			found = true
		}
	}
	runtime.KeepAlive(sd)
	if !found {
		return fmt.Errorf("%w: profile lacks direct fresh-SID data grant", ErrUnsupported)
	}
	return nil
}
func verifyToken(process windows.Handle, sid string) (err error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return nativeError("OpenProcessToken", err)
	}
	defer func() { err = errors.Join(err, closeOwnedHandle("CloseHandle.token", windows.Handle(token))) }()
	for _, class := range []uint32{29, 30, 31} {
		b, err := tokenInformation(token, class)
		if err != nil {
			return err
		}
		switch class {
		case 29:
			if len(b) != 4 || binary.LittleEndian.Uint32(b) == 0 {
				return fmt.Errorf("%w: not an AppContainer", ErrUnsupported)
			}
		case 30:
			if len(b) < 4 || binary.LittleEndian.Uint32(b) != 0 {
				return fmt.Errorf("%w: unexpected capabilities", ErrUnsupported)
			}
		case 31:
			if len(b) < int(unsafe.Sizeof(uintptr(0))) {
				return ErrUnsupported
			}
			actual := *(**windows.SID)(unsafe.Pointer(&b[0]))
			if actual == nil || actual.String() != sid {
				return fmt.Errorf("%w: package SID mismatch", ErrUnsupported)
			}
			runtime.KeepAlive(b)
		}
	}
	return nil
}
