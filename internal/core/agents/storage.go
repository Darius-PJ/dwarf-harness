package agents

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

const (
	maxObjectBytes    = 1 << 20
	maxOperationBytes = 4 << 20
)

// ErrTooLarge reports bytes beyond the private-storage producer bounds.
var ErrTooLarge = errors.New("agents: private bytes exceed limit")

// ErrIntegrity reports an existing object that does not match its address.
// Errors contain paths and digests, never private content.
var ErrIntegrity = errors.New("agents: private object integrity failure")

func hashObject(data []byte) (ledger.Digest, error) {
	if len(data) > maxObjectBytes {
		return ledger.Digest{}, ErrTooLarge
	}
	return ledger.Digest(sha256.Sum256(data)), nil
}

// publishObject publishes exact bounded bytes in this scope only. The caller
// serializes publication in the scope, including the verify-existing/rename
// interval: durable.Rename replaces its target and is not a no-clobber primitive.
func (s privateScope) publishObject(data []byte) (digest ledger.Digest, err error) {
	if len(data) > maxObjectBytes {
		return ledger.Digest{}, ErrTooLarge
	}
	if err := s.ensureObjects(); err != nil {
		return ledger.Digest{}, err
	}
	temp, digest, err := writePrivateTemp(s.objects(), data)
	if err != nil {
		return ledger.Digest{}, err
	}
	defer func() { err = errors.Join(err, removePrivateTemp(temp)) }()
	target := filepath.Join(s.objects(), digest.String())
	info, statErr := regularEntry(target)
	switch {
	case statErr == nil:
		if info.Size() != int64(len(data)) {
			return ledger.Digest{}, fmt.Errorf("%w: size at %s", ErrIntegrity, target)
		}
		actual, _, err := hashPrivateFile(target)
		if err != nil {
			return ledger.Digest{}, err
		}
		if actual != digest {
			return ledger.Digest{}, fmt.Errorf("%w: digest at %s", ErrIntegrity, target)
		}
		// A prior failed directory flush may have left this verified object.
		return digest, durable.SyncDir(s.objects())
	case !errors.Is(statErr, os.ErrNotExist):
		return ledger.Digest{}, statErr
	default:
		// On a flush failure, leave the published object unreferenced. Never
		// remove a target; only this invocation's temporary file is ours.
		return digest, durable.Rename(temp, target)
	}
}

// writePrivateTemp hashes while writing, flushes data, then closes the file.
// A successful return transfers cleanup of the temporary path to the caller.
func writePrivateTemp(dir string, data []byte) (string, ledger.Digest, error) {
	if len(data) > maxObjectBytes {
		return "", ledger.Digest{}, ErrTooLarge
	}
	f, err := os.CreateTemp(dir, ".private-*")
	if err != nil {
		return "", ledger.Digest{}, err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), bytes.NewReader(data))
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", ledger.Digest{}, errors.Join(err, removePrivateTemp(f.Name()))
	}
	var digest ledger.Digest
	copy(digest[:], h.Sum(nil))
	return f.Name(), digest, nil
}

func removePrivateTemp(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// hashPrivateFile bounds both work and allocation when an on-disk file has been
// changed externally. Callers must first check its owned ancestors and entry.
func hashPrivateFile(path string) (ledger.Digest, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return ledger.Digest{}, 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxObjectBytes+1))
	err = errors.Join(err, f.Close())
	if n > maxObjectBytes {
		err = errors.Join(err, ErrTooLarge)
	}
	if err != nil {
		return ledger.Digest{}, n, err
	}
	var digest ledger.Digest
	copy(digest[:], h.Sum(nil))
	return digest, n, nil
}
