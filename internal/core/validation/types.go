package validation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"sync"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

var ErrUnsupported = errors.New("validation: unsupported platform")
var ErrInvalid = errors.New("validation: invalid attempt")

// Config supplies host-owned services and explicit admission maxima. The caller
// reserves aggregate capacity before Run and retains it through terminal commit.
// New does not create directories. RunsRoot must be an existing real directory.
type Config struct {
	Workspace *workspace.Service
	Store     *ledgerstore.Store
	RunsRoot  string
	Capacity  workspace.CapacityProfile
}

type Runner struct {
	workspace *workspace.Service
	store     *ledgerstore.Store
	runsRoot  string
	maxFiles  int
	limits    workspace.ValidationResources
}

// Attempt carries already-authorized immutable input. DependencyRoot is the
// host's read-only cache seed; its full contents must match Environment's pin.
// Subject is an opaque workspace-resolved handle, not a caller-selected tree.
type Attempt struct {
	Queued         ledger.ValidationAttemptQueued
	Subject        workspace.ValidationSubject
	Check          ledger.CheckSpec
	Environment    ledger.EnvironmentSpec
	DependencyRoot string
}

// Notices never receive a verdict. Prepared publishes bodies and durably records
// launch intent before any untrusted child. Its error prevents launch. Started is
// a synchronous, prompt PID notice with gitprocess.Spec.Started's restrictions.
type Notices struct {
	Prepared func(context.Context, Inputs) error
	Started  func(int)
}

// Inputs is a detached launch-intent value and the exact manifest body bytes.
type Inputs struct {
	Started              ledger.ValidationAttemptStarted
	MaterializedManifest []byte
	OverlayManifest      []byte
}

// Result cannot be constructed with an outcome by a caller. Finish and Bodies
// return detached facts, never a capability to promote. Release is called only
// after the consumer's durable terminal bookkeeping; it refuses unsafe closure.
type Result struct{ state *resultState }

type resultState struct {
	finish     ledger.ValidationAttemptFinished
	inputs     *Inputs
	bodies     map[ledger.Digest][]byte
	root       string
	rootInfo   os.FileInfo
	safe       bool
	once       sync.Once
	releaseErr error
}

func (r Result) Finish() ledger.ValidationAttemptFinished {
	if r.state == nil {
		return ledger.ValidationAttemptFinished{}
	}
	f := r.state.finish
	f.RequiredResults = slices.Clone(f.RequiredResults)
	f.Artifacts = slices.Clone(f.Artifacts)
	if f.ExitCode != nil {
		code := *f.ExitCode
		f.ExitCode = &code
	}
	return f
}

func (r Result) Inputs() (Inputs, bool) {
	if r.state == nil || r.state.inputs == nil {
		return Inputs{}, false
	}
	v := *r.state.inputs
	v.MaterializedManifest = bytes.Clone(v.MaterializedManifest)
	v.OverlayManifest = bytes.Clone(v.OverlayManifest)
	return v, true
}

func (r Result) Bodies() map[ledger.Digest][]byte {
	out := map[ledger.Digest][]byte{}
	if r.state != nil {
		for digest, b := range r.state.bodies {
			out[digest] = bytes.Clone(b)
		}
	}
	return out
}

func (r Result) Release() error {
	if r.state == nil {
		return ErrInvalid
	}
	s := r.state
	if !s.safe {
		return errors.New("validation: recovery_pending")
	}
	s.once.Do(func() {
		if s.root != "" {
			now, err := os.Lstat(s.root)
			if err != nil || s.rootInfo == nil || !os.SameFile(now, s.rootInfo) || !now.IsDir() || now.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				s.releaseErr = ErrInvalid
				return
			}
		}
		if s.root != "" {
			s.releaseErr = os.RemoveAll(s.root)
		}
	})
	return s.releaseErr
}
