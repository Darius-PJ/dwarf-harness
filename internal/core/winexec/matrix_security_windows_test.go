package winexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"golang.org/x/sys/windows"
)

func TestSeparateRunsCannotReadPeerInput(t *testing.T) {
	t.Parallel()
	var contexts [2]context.Context
	var cancels [2]context.CancelFunc
	for i := range contexts {
		contexts[i], cancels[i] = context.WithCancel(context.Background())
	}
	type admitted struct {
		index   int
		dir     string
		sid     string
		process windows.Handle
		err     error
	}
	ready := make(chan admitted, 2)
	releases := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseOnce [2]sync.Once
	unblock := func(i int) { releaseOnce[i].Do(func() { close(releases[i]) }) }
	var handlesMu sync.Mutex
	var handles [2]windows.Handle
	closeHandles := func() bool {
		handlesMu.Lock()
		defer handlesMu.Unlock()
		ok := true
		for i, handle := range handles {
			if handle != 0 {
				if err := windows.CloseHandle(handle); err != nil {
					t.Errorf("close peer %d observer handle: %v", i, err)
					ok = false
				}
				handles[i] = 0
			}
		}
		return ok
	}
	var results [2]chan runResult
	var done [2]chan struct{}
	// Defers run before testExecutor's cleanups, including on an early failure.
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		for i := range releases {
			unblock(i)
		}
		closeHandles()
		for _, joined := range done {
			if joined != nil {
				<-joined
			}
		}
	}()
	checkTargets := func(dir, nonce string) error {
		for path, want := range map[string]string{
			filepath.Join(dir, "input", "nonce"):     nonce,
			filepath.Join(dir, "work", "peer-nonce"): "private-work-" + nonce,
		} {
			data, err := os.ReadFile(path)
			if err != nil || string(data) != want {
				return fmt.Errorf("peer target changed: path=%q contents=%q error=%v", path, data, err)
			}
		}
		return nil
	}
	var unchanged [2]error
	var sharedLimits Limits
	for i := range results {
		e := testExecutor(t)
		nonce := fmt.Sprintf("authorized-peer-%d", i)
		ctx := contexts[i]
		request := testRequest(t, "peer", nonce)
		sharedLimits = request.Limits
		request.Files = append(request.Files, InputFile{Path: "nonce", Data: []byte(nonce)})
		e.observe = func(phase string, r *nativeRun) {
			switch phase {
			case "suspended_verified":
				peer := admitted{index: i, dir: r.dir, sid: r.sid.String()}
				handlesMu.Lock()
				// Cancellation precedes handle cleanup, so a late callback
				// cannot create a duplicate after cleanup has closed them.
				if ctx.Err() == nil {
					self := windows.CurrentProcess()
					peer.err = windows.DuplicateHandle(self, r.pi.Process, self, &peer.process, windows.SYNCHRONIZE, false, 0)
					handles[i] = peer.process
				}
				handlesMu.Unlock()
				ready <- peer
				select {
				case <-releases[i]:
				case <-ctx.Done():
				}
			case "tree_exited":
				unchanged[i] = checkTargets(r.dir, nonce)
			}
		}
		results[i] = make(chan runResult, 1)
		done[i] = make(chan struct{})
		go func() {
			r, err := e.Run(ctx, request)
			results[i] <- runResult{r, err}
			close(done[i])
		}()
	}
	var peers [2]admitted
	for range peers {
		select {
		case peer := <-ready:
			peers[peer.index] = peer
			if peer.err != nil {
				t.Fatalf("peer %d admission: %v", peer.index, peer.err)
			}
		case <-done[0]:
			t.Fatalf("peer A completed before both admissions: %+v", <-results[0])
		case <-done[1]:
			t.Fatalf("peer B completed before both admissions: %+v", <-results[1])
		case <-testAdmissionWait(t, sharedLimits, sharedLimits.Wall+sharedLimits.StopGrace):
			t.Fatal("both real runs did not reach the suspended boundary")
		}
	}
	if peers[0].sid == peers[1].sid || peers[0].dir == peers[1].dir {
		t.Fatalf("concurrent runs share authority: %+v", peers)
	}
	for i, peer := range peers {
		other := peers[1-i]
		data, err := json.Marshal([2]string{filepath.Join(other.dir, "input", "nonce"), filepath.Join(other.dir, "work", "peer-nonce")})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(peer.dir, "work", "peer-targets.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(peer.dir, "work", "peer-nonce"), []byte(fmt.Sprintf("private-work-authorized-peer-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for i, peer := range peers {
		t.Logf("releasing peer %d from suspended boundary", i)
		unblock(i)
		want := fmt.Sprintf("authorized-peer-%d", i)
		for {
			data, err := os.ReadFile(filepath.Join(peer.dir, "work", "peer-probed"))
			if err == nil && string(data) == want {
				t.Logf("observed peer %d probe marker: %q", i, data)
				break
			}
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("peer %d probe marker: %v", i, err)
			}
			select {
			case <-done[0]:
				t.Fatalf("peer A completed before peer %d probe marker: %+v", i, <-results[0])
			case <-done[1]:
				t.Fatalf("peer B completed before peer %d probe marker: %+v", i, <-results[1])
			case <-tick.C:
			}
		}
	}
	for i, peer := range peers {
		state, err := windows.WaitForSingleObject(peer.process, 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("peer %d root did not remain live after both probes: state=%d error=%v", i, state, err)
		}
		if err := checkTargets(peer.dir, fmt.Sprintf("authorized-peer-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if !closeHandles() {
		t.Fatal("peer observer handles did not close before payload release")
	}
	for i, peer := range peers {
		t.Logf("releasing peer %d after both live-target proofs", i)
		if err := os.WriteFile(filepath.Join(peer.dir, "work", "peer-release"), []byte("release-nonce"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i, result := range results {
		got := awaitResult(t, result)
		assertReusable(t, got.receipt, got.err)
		if unchanged[i] != nil {
			t.Fatal(unchanged[i])
		}
		var probe peerProbe
		if err := json.Unmarshal(got.receipt.Stdout, &probe); err != nil {
			t.Fatalf("peer evidence: %q: %v", got.receipt.Stdout, err)
		}
		if probe.OwnNonce != fmt.Sprintf("authorized-peer-%d", i) {
			t.Fatalf("own input was not usable: %+v", probe)
		}
		for j := range probe.ReadCodes {
			if probe.ReadCodes[j] != uint32(windows.ERROR_ACCESS_DENIED) || probe.WriteCodes[j] != uint32(windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("disclosed peer target was not protected: %+v", probe)
			}
		}
		t.Logf("separate SID=%s; actual disclosed peer input/work read and write native codes=%v/%v; own nonce succeeded", got.receipt.ProfileSID, probe.ReadCodes, probe.WriteCodes)
	}
}

func TestOnlyExplicitStandardHandlesInherited(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	path := testProtectedFile(t, t.TempDir(), "handle-canary", "private-handle-nonce")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &sa, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
	var canary windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &canary); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := false
	var handleCount int
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "suspended_created" {
			return
		}
		observed = true
		handleCount, observationErr = testInspectInheritedHandles(r, canary)
		if observationErr != nil {
			cancel()
		}
	}
	request := testRequest(t, "inherited-handles")
	request.Stdin = []byte("handle-positive-stdin")
	r, err := e.Run(ctx, request)
	if !observed || observationErr != nil {
		t.Fatalf("native suspended handle snapshot: observed=%t error=%v run=%v", observed, observationErr, err)
	}
	assertReusable(t, r, err)
	if string(r.Stdout) != "explicit-handles-only\n" || string(r.Stderr) != "handle-positive-stderr\n" {
		t.Fatalf("standard stream exchange did not complete: stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private-handle-nonce" {
		t.Fatalf("Core canary changed through an inherited handle: %q %v", data, err)
	}
	t.Logf("native suspended child snapshot=%d entries; inheritable Core canary checked by volume/file ID; pipe ends by native role/owner PID; all three explicit standard handles usable", handleCount)
}

func TestProtectedTargetsDenied(t *testing.T) {
	t.Parallel()
	testProtectedTargets(t, false)
}

func TestDescendantsCannotBreakAway(t *testing.T) {
	t.Parallel()
	testProtectedTargets(t, true)
}

func testProtectedTargets(t *testing.T, descendants bool) {
	t.Helper()
	ctx := context.Background()
	e := testExecutor(t)
	git := testPrivateGit(t)
	colony := t.TempDir()
	store, err := ledgerstore.Create(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	const bodyNonce = "published-body-native-denial-nonce"
	digest, _, err := store.PublishBody(ctx, strings.NewReader(bodyNonce))
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.ReadBody(ctx, digest)
	if err != nil || string(body) != bodyNonce {
		t.Fatalf("host positive body read: %q %v", body, err)
	}
	payload, err := (ledger.Command{CommandID: uuid.NewV7(), SubmissionSHA256: digest}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	command := ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorAgent, Payload: payload}
	commit, err := store.Submit(ctx, command, nil)
	if err != nil {
		t.Fatal(err)
	}
	position := store.Durable()
	paths := []string{
		filepath.Join(git.root, "base.txt"),
		filepath.Join(git.root, ".git", "HEAD"),
		filepath.Join(git.root, ".git", "config"),
		filepath.Join(git.root, ".git", "refs", "heads", "noncanonical"),
		filepath.Join(git.root, ".git", "refs", "heads", "main"),
		filepath.Join(git.root, ".git", "objects", git.main[:2], git.main[2:]),
		filepath.Join(store.LogDir(), ledger.SegmentName(1)),
		filepath.Join(colony, "colony.json"),
		filepath.Join(colony, "bodies", digest.String()[:2], digest.String()),
	}
	// Closing removes sharing-mode interference: these attempts must fail at
	// the native access boundary, not because the writer happens to be open.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	directories := []string{git.root, filepath.Join(git.root, ".git"), filepath.Join(git.root, ".git", "objects"), store.LogDir(), filepath.Join(colony, "bodies")}
	for _, name := range []string{"private-peer", "credentials-canary", "sealed-result"} {
		dir := t.TempDir()
		paths = append(paths, testProtectedFile(t, dir, name, "host-only-"+name))
		directories = append(directories, dir)
	}
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, testProtectedFile(t, localAppData, "dwarf-harness-canary-"+uuid.NewV7().String(), "host-localappdata-nonce"))
	before := make(map[string][]byte, len(paths))
	for _, path := range paths {
		if err := securePath(path, e.owner, "", ""); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("host positive target read %q: %v", path, err)
		}
		before[path] = data
	}
	for _, dir := range directories {
		if err := securePath(dir, e.owner, "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadDir(dir); err != nil {
			t.Fatalf("host positive directory enumeration %q: %v", dir, err)
		}
	}
	pipe, noDelivery := testOwnerPipe(t, e.owner)
	manifest, err := json.Marshal(protectedTargets{Files: paths, Directories: directories})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t, "protected", pipe)
	request.Files = append(request.Files, InputFile{Path: "nonce", Data: []byte("authorized-targets-nonce")}, InputFile{Path: "targets.json", Data: manifest})
	var r Receipt
	var probes []protectedProbe
	if descendants {
		r, probes, err = testRunDescendants(t, e, request, pipe)
	} else {
		r, err = e.Run(ctx, request)
		var probe protectedProbe
		if decodeErr := json.Unmarshal(r.Stdout, &probe); decodeErr != nil {
			t.Fatalf("native denial evidence: %q %v; run=%v", r.Stdout, decodeErr, err)
		}
		probes = []protectedProbe{probe}
	}
	assertReusable(t, r, err)
	noDelivery()
	for _, probe := range probes {
		if probe.OwnNonce != "authorized-targets-nonce" || len(probe.Files) != len(paths) || len(probe.Directories) != len(directories) {
			t.Fatalf("positive input or target coverage missing: %+v", probe)
		}
		for i, code := range probe.Directories {
			if code != uint32(windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("real directory not access-denied: path=%q native=%d", directories[i], code)
			}
		}
		for i, path := range paths {
			if probe.Files[i].ReadCode != uint32(windows.ERROR_ACCESS_DENIED) || probe.Files[i].WriteCode != uint32(windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("real target not access-denied: path=%q result=%+v", path, probe.Files[i])
			}
		}
		switch probe.PipeCode {
		case uint32(windows.ERROR_ACCESS_DENIED), uint32(windows.ERROR_FILE_NOT_FOUND), uint32(windows.ERROR_PATH_NOT_FOUND):
		default:
			t.Fatalf("owner pipe was not access-denied or hidden by the AppContainer namespace: %d", probe.PipeCode)
		}
	}
	for _, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before[path]) {
			t.Fatalf("protected bytes changed: %q error=%v", path, err)
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	main, err := git.command(checkCtx, "rev-parse", "refs/heads/main")
	if err != nil || main != git.main {
		t.Fatalf("real canonical main changed: %q %v", main, err)
	}
	reopened, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Durable() != position {
		t.Fatalf("real durable position changed: got=%d want=%d", reopened.Durable(), position)
	}
	body, err = reopened.ReadBody(ctx, digest)
	if err != nil || string(body) != bodyNonce {
		t.Fatalf("published body changed: %q %v", body, err)
	}
	replay, err := reopened.Submit(ctx, command, nil)
	if err != nil || !replay.Duplicate || replay.Committed != commit.Committed || reopened.Durable() != position {
		t.Fatalf("real committed command lost its acknowledged position: %+v %v", replay, err)
	}
	t.Logf("Git=%s main=%s; native denials for actual Git/log/metadata/body, private directories and owner pipe=%+v, with no delivery; durable position preserved=%d", git.version, main, probes, position)
}

func TestLinksAndInputAliasesCannotExpandRoots(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	external := t.TempDir()
	canary := testProtectedFile(t, external, "canary", "external-link-owner")
	before := make(map[string]string)
	for _, path := range []string{external, canary} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = sd.String()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var paths []string
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "suspended_verified" {
			return
		}
		observationErr = func() error {
			source, err := os.Stat(canary)
			if err != nil {
				return err
			}
			for _, root := range []string{filepath.Join(r.dir, "input"), filepath.Join(r.dir, "work"), r.profile.Path} {
				junction := filepath.Join(root, "via-junction")
				if err := testMakeJunction(junction, external); err != nil {
					return err
				}
				info, err := os.Lstat(junction)
				if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
					return fmt.Errorf("junction is not a native reparse point: %v", err)
				}
				hardlink := filepath.Join(root, "via-hardlink")
				if err := os.Link(canary, hardlink); err != nil {
					return err
				}
				linked, err := os.Stat(hardlink)
				if err != nil || !os.SameFile(source, linked) {
					return fmt.Errorf("hard link does not identify the real canary: %v", err)
				}
				paths = append(paths, filepath.Join(junction, "canary"), hardlink)
			}
			data, err := json.Marshal(paths)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(r.dir, "work", "links.json"), data, 0o600)
		}()
		if observationErr != nil {
			cancel()
		}
	}
	request := testRequest(t, "links")
	request.Files = append(request.Files, InputFile{Path: "nonce", Data: []byte("authorized-link-nonce")})
	r, err := e.Run(ctx, request)
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	assertReusable(t, r, err)
	var proof []targetDenial
	if err := json.Unmarshal(r.Stdout, &proof); err != nil {
		t.Fatalf("link denial evidence: %q %v", r.Stdout, err)
	}
	if len(paths) != 6 || len(proof) != len(paths) {
		t.Fatalf("private input/work/profile aliases were not all exercised: paths=%v proof=%+v", paths, proof)
	}
	for i, result := range proof {
		if result.ReadCode != uint32(windows.ERROR_ACCESS_DENIED) || result.WriteCode != uint32(windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("alias expanded authority: path=%q result=%+v", paths[i], result)
		}
	}
	data, err := os.ReadFile(canary)
	if err != nil || string(data) != "external-link-owner" {
		t.Fatalf("cleanup followed a private alias into the external object: %q %v", data, err)
	}
	for path, want := range before {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
		if err != nil || sd.String() != want {
			t.Fatalf("external alias target permissions changed: %q: %v", path, err)
		}
	}
	t.Logf("real junction/hard-link denial codes in input/work/native profile=%+v; native file identity and external bytes/DACL/label preserved after cleanup", proof)
}
