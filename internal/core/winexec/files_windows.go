package winexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type fileBinding struct {
	Path, Volume, ID string
	Handle           windows.Handle
	Reparse          bool
}

func openDirectory(path string, allowReparse bool) (b fileBinding, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return b, err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return b, nativeError("CreateFile.directory", err)
	}
	ok := false
	defer func() {
		if !ok {
			err = errors.Join(err, closeOwnedHandle("CloseHandle.directory", h))
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return b, nativeError("GetFileInformationByHandle", err)
	}
	reparse := info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || reparse && !allowReparse {
		return b, ErrInvalidRoot
	}
	name := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &name[0], uint32(len(name)), 0)
	if err != nil {
		return b, nativeError("GetFinalPathNameByHandle", err)
	}
	if n >= uint32(len(name)) {
		return b, ErrInvalidRoot
	}
	canonical := strings.TrimPrefix(windows.UTF16ToString(name[:n]), `\\?\`)
	if !canonicalDrivePath(canonical) {
		return b, ErrInvalidRoot
	}
	b = fileBinding{Path: canonical, Volume: fmt.Sprintf("%08x", info.VolumeSerialNumber), ID: fmt.Sprintf("%08x%08x", info.FileIndexHigh, info.FileIndexLow), Handle: h, Reparse: reparse}
	ok = true
	return b, nil
}
func checkLocalRoot(path string) error {
	if !filepath.IsAbs(path) || !canonicalDrivePath(filepath.Clean(path)) {
		return ErrInvalidRoot
	}
	volume := filepath.VolumeName(path) + `\`
	p, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return err
	}
	if windows.GetDriveType(p) != windows.DRIVE_FIXED {
		return ErrInvalidRoot
	}
	current := volume
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), volume), `\`)
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && i == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ErrInvalidRoot
		}
	}
	return nil
}
func readMarker(path string) (data []byte, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, nativeError("CreateFile.marker", err)
	}
	defer func() { err = errors.Join(err, closeOwnedHandle("CloseHandle.marker", h)) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow > markerLimit {
		return nil, ErrQuarantined
	}
	b := make([]byte, markerLimit+1)
	var n uint32
	if err := windows.ReadFile(h, b, &n, nil); err != nil {
		return nil, nativeError("ReadFile.marker", err)
	}
	if n > markerLimit {
		return nil, ErrQuarantined
	}
	return b[:n], nil
}
func stageInput(dir, owner, sid string, files []InputFile) ([]windows.Handle, error) {
	var pins []windows.Handle
	fail := func(err error) ([]windows.Handle, error) {
		for _, h := range pins {
			err = errors.Join(err, closeOwnedHandle("CloseHandle.input", h))
		}
		return nil, err
	}
	for _, file := range files {
		local, err := filepath.Localize(file.Path)
		if err != nil || !filepath.IsLocal(local) {
			return fail(ErrInvalid)
		}
		path := filepath.Join(dir, local)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fail(err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fail(err)
		}
		_, err = f.Write(file.Data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fail(err)
		}
	}
	if err := securePath(dir, owner, sid, "FRFX"); err != nil {
		return fail(err)
	}
	for _, file := range files {
		local, err := filepath.Localize(file.Path)
		if err != nil {
			return fail(err)
		}
		path, err := windows.UTF16PtrFromString(filepath.Join(dir, local))
		if err != nil {
			return fail(err)
		}
		h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return fail(nativeError("CreateFile.seal", err))
		}
		pins = append(pins, h)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil {
			return fail(err)
		}
		if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
			return fail(ErrInvalid)
		}
	}
	return pins, nil
}
func unlink(ctx context.Context, path string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// emptyDirectory pages entries and never follows a child-created reparse point.
// The caller pins and verifies the root's native identity before this walk.
func emptyDirectory(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			info, err := os.Lstat(child)
			if err != nil {
				return err
			}
			if info.IsDir() && (info.Mode()|entry.Type())&(os.ModeSymlink|os.ModeIrregular) == 0 {
				if err := emptyDirectory(ctx, child); err != nil {
					return err
				}
			}
			if err := unlink(ctx, child); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
func removeTree(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		if err := emptyDirectory(ctx, path); err != nil {
			return err
		}
	}
	return unlink(ctx, path)
}
