package validation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
)

func New(c Config) (*Runner, error) {
	if !supported() {
		return nil, ErrUnsupported
	}
	if c.Workspace == nil || c.Store == nil || c.Capacity.MaxFiles < 1 || c.Capacity.Validation == nil || c.Capacity.Validation.Validate() != nil || !filepath.IsAbs(c.RunsRoot) {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(c.RunsRoot)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, ErrInvalid
	}
	root, err := filepath.EvalSymlinks(c.RunsRoot)
	if err != nil {
		return nil, err
	}
	return &Runner{workspace: c.Workspace, store: c.Store, runsRoot: root, maxFiles: c.Capacity.MaxFiles, limits: c.Capacity.Validation.PerJob}, nil
}

func validID(id uuid.UUID) bool { return id[6]>>4 == 7 && id[8]&0xc0 == 0x80 }

func (r *Runner) preflight(a Attempt) error {
	q, c, e := a.Queued, a.Check, a.Environment
	if r == nil || !validID(q.ValidationID) || !validID(q.AttemptID) || !validID(q.RunID) || q.BindingSHA256 == (ledger.Digest{}) || q.Subject != a.Subject.Descriptor() || c.ExecutableID != e.ExecutableID || c.EnvironmentID != e.EnvironmentID {
		return ErrInvalid
	}
	check, err := c.CheckPreimage()
	if err != nil {
		return err
	}
	env, err := e.EnvironmentPreimage()
	if err != nil {
		return err
	}
	inventory, err := c.InventoryPreimage()
	if err != nil {
		return err
	}
	if sha256.Sum256(check) != q.CheckSHA256 || sha256.Sum256(env) != q.EnvironmentSHA256 || sha256.Sum256(inventory) != q.InventorySHA256 || q.AdapterSHA256 != AdapterDigest(q.RunnerSHA256) {
		return ErrInvalid
	}
	if c.OutputBytes > r.limits.OutputBytes || c.Processes > r.limits.Processes || c.Processes > int64(^uint32(0)) || e.GOMAXPROCS > r.limits.CPUUnits || e.MemoryReservationBytes > r.limits.MemoryBytes || len(c.DefiningFiles)+1 > r.maxFiles {
		return ErrInvalid
	}
	program, err := os.Executable()
	if err != nil {
		return err
	}
	sum, _, err := fileDigest(context.Background(), program, 1<<63-1)
	if err != nil || sum != q.RunnerSHA256 {
		return ErrInvalid
	}
	return nil
}

// Run completes one attempt synchronously. It neither appends records nor
// retries checks. Cancellation is joined by the attempt's native Owner.
func (r *Runner) Run(ctx context.Context, a Attempt, n Notices) (Result, error) {
	if !supported() {
		return Result{}, ErrUnsupported
	}
	q := a.Queued
	s := &resultState{safe: true, bodies: map[ledger.Digest][]byte{}, finish: ledger.ValidationAttemptFinished{
		ValidationID: q.ValidationID, AttemptID: q.AttemptID, RunID: q.RunID, Outcome: "infra_failure", Reason: "environment_unavailable", ClosureProof: "not_started", Canary: ledger.ValidationCanaryResult{State: "not_run", TemplateSHA256: a.Check.Canary.TemplateSHA256}, RequiredResults: []ledger.ValidationRequiredResult{}, Artifacts: []ledger.ValidationArtifact{}, InputIntegrity: "unavailable",
	}}
	result := Result{state: s}
	if ctx == nil {
		return result, ErrInvalid
	}
	if err := r.preflight(a); err != nil {
		return result, err
	}
	args, err := goArguments(a.Check)
	if err != nil {
		return result, err
	}
	template, err := r.store.ReadBody(ctx, a.Check.Canary.TemplateSHA256)
	if err != nil || !bytes.Equal(template, CanaryTemplate()) || sha256.Sum256(template) != a.Check.Canary.TemplateSHA256 {
		return result, fmt.Errorf("validation: environment_unavailable")
	}
	dependencies, err := r.verifyEnvironment(ctx, a.Environment, a.DependencyRoot)
	if err != nil {
		return result, fmt.Errorf("validation: environment_unavailable: %w", err)
	}
	if err := ctx.Err(); err != nil {
		s.finish.Outcome, s.finish.Reason = "incomplete", "cancelled"
		return result, err
	}
	id := q.RunID.String()
	root := filepath.Join(r.runsRoot, id[len(id)-8:])
	if err := os.Mkdir(root, 0700); err != nil {
		return result, err
	}
	s.root = root
	s.rootInfo, err = os.Lstat(root)
	if err != nil {
		s.safe = false
		return result, err
	}
	export, err := r.workspace.ExportValidationSubject(ctx, a.Subject, root, r.limits.MaterializedBytes)
	if err != nil {
		s.finish.Reason = "subject_unavailable"
		return result, err
	}
	canary, binding, nonceHash, err := makeCanary(q, a.Check)
	if err != nil {
		return result, err
	}
	pins, overlay, err := r.overlay(ctx, export.Directory(), a.Check, canary)
	if err != nil {
		s.finish.Reason = "definition_unavailable"
		return result, err
	}
	env, err := r.prepareEnvironment(ctx, root, export.Directory(), a, dependencies)
	if err != nil {
		return result, err
	}
	if err := r.materializationBudget(ctx, root); err != nil {
		return result, err
	}
	_, integrity := verifyFiles(ctx, export.Directory(), pins, r.limits.MaterializedBytes)
	s.finish.InputIntegrity = integrity
	if integrity != "verified" {
		return result, fmt.Errorf("validation: integrity_unavailable")
	}
	manifest, err := json.Marshal(struct {
		Schema           int                `json:"schema"`
		Argv             []string           `json:"argv"`
		WorkingDirectory string             `json:"working_directory"`
		Environment      []concreteVariable `json:"environment"`
	}{1, append([]string{env.program}, args...), filepath.Join(export.Directory(), filepath.FromSlash(a.Check.WorkingDirectory)), env.variables})
	if err != nil {
		return result, err
	}
	// Reserve metadata before collection: integrity has the same bounded path
	// set as overlay, with at most 19 decimal size digits per file.
	metadata := int64(len(manifest) + len(overlay) + 19*len(pins))
	if metadata >= a.Check.OutputBytes {
		return result, ledger.ErrTooLarge
	}
	owner, _, err := gitprocess.Open(ctx, filepath.Join(root, "processes"), gitprocess.Options{RequireTree: true, MaxConcurrent: 1, MaxDescendants: int(a.Check.Processes)})
	if err != nil {
		return result, err
	}
	closeOwner := func() error {
		err := owner.Close()
		if err != nil {
			s.safe = false
		}
		return err
	}
	inputs := Inputs{Started: ledger.ValidationAttemptStarted{ValidationID: q.ValidationID, AttemptID: q.AttemptID, RunID: q.RunID, MaterializedManifestSHA256: sha256.Sum256(export.Manifest()), OverlaySHA256: sha256.Sum256(overlay), CanarySHA256: sha256.Sum256(canary), NonceSHA256: nonceHash, ConcreteEnvironmentSHA256: env.digest}, MaterializedManifest: export.Manifest(), OverlayManifest: overlay}
	if n.Prepared != nil {
		if err := n.Prepared(ctx, cloneInputs(inputs)); err != nil {
			return result, errors.Join(err, closeOwner())
		}
	}
	s.inputs = &inputs
	s.finish.Canary.CanarySHA256, s.finish.Canary.NonceSHA256 = inputs.Started.CanarySHA256, nonceHash
	// Prepared may take time. Re-read pins after it, immediately before launch.
	_, integrity = verifyFiles(ctx, export.Directory(), pins, r.limits.MaterializedBytes)
	if integrity != "verified" {
		s.finish.InputIntegrity = integrity
		return result, errors.Join(fmt.Errorf("validation: integrity_unavailable"), closeOwner())
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Check.WallMS)*time.Millisecond)
	defer cancel()
	reducer := newGoReducer(a.Check, binding)
	collection := &collection{limit: a.Check.OutputBytes - metadata, cancel: cancel, reducer: reducer}
	var pid int
	native, runErr := owner.Run(runCtx, gitprocess.Spec{Program: env.program, Args: args, Env: env.values, Dir: filepath.Join(export.Directory(), filepath.FromSlash(a.Check.WorkingDirectory)), Stdout: collectionWriter{collection, true}, Stderr: collectionWriter{collection, false}, Started: func(p int) {
		pid = p
		if n.Started != nil {
			n.Started(p)
		}
	}})
	closeErr := closeOwner()
	collection.mu.Lock()
	collection.sealed = true
	collection.mu.Unlock()
	s.safe = closeErr == nil && !errors.Is(runErr, gitprocess.ErrUnconfirmed) && (pid == 0 || native.RootExited && native.TreeClosed && native.IOJoined && native.Proof == "live")
	if pid == 0 {
		s.finish.Reason = "launch_failed"
		if !s.safe {
			s.finish.Outcome, s.finish.Reason = "incomplete", "recovery_pending"
		}
		return result, errors.Join(runErr, closeErr)
	}
	f := &s.finish
	f.Started, f.RootPID, f.ProcessRunID = true, int64(pid), native.RunID
	f.RootExited, f.TreeClosed, f.IOJoined = native.RootExited, native.TreeClosed, native.IOJoined
	f.ClosureProof = "unconfirmed"
	if s.safe {
		f.ClosureProof = native.Proof
	}
	if native.RootExited && native.ExitCode >= 0 {
		exit := int64(native.ExitCode)
		f.ExitCode = &exit
		f.ExitKnown = true
	}
	reducer.finish()
	f.InputIntegrity = "unavailable"
	if s.safe {
		actual, state := verifyFiles(context.WithoutCancel(ctx), export.Directory(), pins, r.limits.MaterializedBytes)
		f.InputIntegrity = state
		postTemplate, templateErr := r.store.ReadBody(context.WithoutCancel(ctx), a.Check.Canary.TemplateSHA256)
		if templateErr != nil || !bytes.Equal(postTemplate, template) {
			f.InputIntegrity = "changed"
		}
		integrityBody, encodeErr := manifestBytes(actual)
		if encodeErr == nil {
			s.artifact("integrity", integrityBody, state != "missing" && state != "unavailable")
		} else {
			f.InputIntegrity = "unavailable"
		}
	}
	stopReason := ""
	if ctx.Err() != nil {
		stopReason = "cancelled"
	} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		stopReason = "timeout"
	}
	reconcileGo(reducer, f, stopReason, collection.overflow)
	f.Canary.CanarySHA256, f.Canary.NonceSHA256 = inputs.Started.CanarySHA256, nonceHash
	if runErr != nil && !errors.Is(runErr, gitprocess.ErrExit) && stopReason == "" && s.safe {
		f.Outcome, f.Reason = "incomplete", "invalid_output"
	}
	s.artifact("events", collection.stdout.Bytes(), native.IOJoined && !collection.overflow)
	s.artifact("stderr", collection.stderr.Bytes(), native.IOJoined && !collection.overflow)
	s.artifact("manifest", manifest, true)
	slices.SortFunc(f.Artifacts, func(a, b ledger.ValidationArtifact) int { return strings.Compare(a.Kind, b.Kind) })
	return result, closeErr
}

func cloneInputs(in Inputs) Inputs {
	in.MaterializedManifest = bytes.Clone(in.MaterializedManifest)
	in.OverlayManifest = bytes.Clone(in.OverlayManifest)
	return in
}

func (s *resultState) artifact(kind string, b []byte, complete bool) {
	h := sha256.Sum256(b)
	s.bodies[h] = bytes.Clone(b)
	s.finish.Artifacts = append(s.finish.Artifacts, ledger.ValidationArtifact{Kind: kind, SHA256: h, Bytes: int64(len(b)), Complete: complete})
}

func (r *Runner) materializationBudget(ctx context.Context, root string) error {
	var total int64
	files := 0
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ErrInvalid
		}
		if info.IsDir() {
			return nil
		}
		files++
		if !info.Mode().IsRegular() || files > r.maxFiles || info.Size() > r.limits.MaterializedBytes-total {
			return ledger.ErrTooLarge
		}
		total += info.Size()
		return nil
	})
}

type collection struct {
	mu               sync.Mutex
	stdout, stderr   bytes.Buffer
	limit, used      int64
	overflow, sealed bool
	cancel           context.CancelFunc
	reducer          *goReducer
}

type collectionWriter struct {
	c      *collection
	stdout bool
}

func (w collectionWriter) Write(p []byte) (int, error) {
	c := w.c
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if c.sealed {
		return n, nil
	}
	keep := min(int64(n), c.limit-c.used)
	if keep < int64(n) {
		c.overflow = true
		c.cancel()
	}
	if w.stdout {
		_, _ = c.stdout.Write(p[:keep])
		c.reducer.write(p[:keep])
	} else {
		_, _ = c.stderr.Write(p[:keep])
	}
	c.used += keep
	return n, nil
}
