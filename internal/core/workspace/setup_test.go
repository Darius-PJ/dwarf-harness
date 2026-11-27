package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestSetupWorkspaceRegistration(t *testing.T) {
	t.Parallel()
	f := newUnregisteredService(t)
	digest, err := CaptureInputsDigest(f.p)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.svc.Register(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	f.reopen(t, serviceConfig())
	list, err := f.svc.ListWorkspaces()
	if err != nil || len(list) != 1 || list[0] != w || list[0].InputsSHA256 != digest {
		t.Fatalf("registered inputs: %v %v", list, err)
	}
	list[0].Revision = 99
	list, err = f.svc.ListWorkspaces()
	if err != nil || list[0].Revision != 1 {
		t.Fatal("listing changed registration")
	}
	bad := f.p
	bad.LocalExcludes = HostFile{Bytes: []byte("not present")}
	if _, err := CaptureInputsDigest(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid proposal: %v", err)
	}
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ListWorkspaces(); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed listing: %v", err)
	}
}
