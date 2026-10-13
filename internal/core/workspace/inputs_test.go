package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestInspectImportsHostInputs(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	common := filepath.Join(f.root, ".git")
	home := ""
	for _, s := range f.env {
		if strings.HasPrefix(s, "HOME=") {
			home = strings.TrimPrefix(s, "HOME=")
		}
	}
	files := map[string][]byte{filepath.Join(common, "info", "exclude"): []byte("local\n"), filepath.Join(common, "info", "attributes"): []byte("*.bin -text\n"), filepath.Join(home, "git", "ignore"): []byte("global\n"), filepath.Join(home, "git", "attributes"): []byte("*.txt text\n")}
	for path, b := range files {
		writeTestFile(t, path, b)
	}
	gitCommand(t, f.env, f.root, nil, "config", "core.autocrlf", "true")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		got  HostFile
		path string
	}{{p.LocalExcludes, filepath.Join(common, "info", "exclude")}, {p.LocalAttributes, filepath.Join(common, "info", "attributes")}, {p.GlobalExcludes, filepath.Join(home, "git", "ignore")}, {p.GlobalAttributes, filepath.Join(home, "git", "attributes")}} {
		if !tc.got.Present || !bytes.Equal(tc.got.Bytes, files[tc.path]) {
			t.Fatalf("wrong import %s: %#v", tc.path, tc.got)
		}
	}
	if p.Normalization.AutoCRLF != "true" || p.GitVersion != f.git.version || p.ObjectFormat != "sha1" {
		t.Fatalf("wrong settings: %#v", p)
	}
	for path := range files {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	p, err = f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	if p.LocalExcludes.Present || p.LocalAttributes.Present || p.GlobalExcludes.Present || p.GlobalAttributes.Present {
		t.Fatalf("missing inputs treated as present: %#v", p)
	}
}

func TestInspectRefuses(t *testing.T) {
	t.Parallel()
	t.Run("not repository", func(t *testing.T) {
		t.Parallel()
		g := newGitRunner(func() []string { return testEnv(t) })
		_, err := g.inspect(context.Background(), t.TempDir())
		if !errors.Is(err, ErrNotGitRepository) {
			t.Fatal(err)
		}
	})
	t.Run("no main", func(t *testing.T) {
		t.Parallel()
		f := newGitFixture(t, "sha1")
		gitCommand(t, f.env, f.root, nil, "branch", "-D", "main")
		_, err := f.git.inspect(context.Background(), f.root)
		if !errors.Is(err, ErrNoCanonical) {
			t.Fatal(err)
		}
	})
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "main checkout", true: "linked main checkout"}[linked], func(t *testing.T) {
			t.Parallel()
			f := newGitFixture(t, "sha1")
			if linked {
				gitCommand(t, f.env, f.root, nil, "worktree", "add", filepath.Join(t.TempDir(), "linked"), "main")
			} else {
				gitCommand(t, f.env, f.root, nil, "checkout", "main")
			}
			_, err := f.git.inspect(context.Background(), f.root)
			if !errors.Is(err, ErrCanonicalCheckedOut) {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("oversize", func(t *testing.T) {
		t.Parallel()
		f := newGitFixture(t, "sha1")
		writeTestFile(t, filepath.Join(f.root, ".git", "info", "exclude"), bytes.Repeat([]byte("x"), (1<<20)+1))
		_, err := f.git.inspect(context.Background(), f.root)
		if !errors.Is(err, ErrInputsTooLarge) {
			t.Fatal(err)
		}
	})
	t.Run("bare", func(t *testing.T) {
		t.Parallel()
		f := newGitFixture(t, "sha1")
		bare := filepath.Join(t.TempDir(), "bare.git")
		gitCommand(t, f.env, f.root, nil, "clone", "--bare", f.root, bare)
		p, err := f.git.inspect(context.Background(), bare)
		if err != nil {
			t.Fatal(err)
		}
		if p.CommonDir != filepath.ToSlash(bare) {
			t.Fatalf("wrong common dir: %q", p.CommonDir)
		}
		if runtime.GOOS == "windows" && strings.Contains(p.CommonDir, `\`) {
			t.Fatalf("Git's common-dir bytes were rewritten: %q", p.CommonDir)
		}
	})
}

func TestInputsDigestCoversEveryInput(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.LocalExcludes = HostFile{}
	p.GlobalExcludes = HostFile{}
	p.LocalAttributes = HostFile{}
	p.GlobalAttributes = HostFile{}
	p.SystemAttributes = HostFile{}
	base, digest, err := encodeCaptureInputs(p, "capture-rules/1")
	if err != nil {
		t.Fatal(err)
	}
	again, againDigest, err := encodeCaptureInputs(p, "capture-rules/1")
	if err != nil || !bytes.Equal(base, again) || digest != againDigest || digest != sha256.Sum256(base) {
		t.Fatalf("nondeterministic inputs: %v", err)
	}
	var changes []Proposal
	for i := range reflect.TypeFor[Normalization]().NumField() {
		q := p
		field := reflect.ValueOf(&q.Normalization).Elem().Field(i)
		if field.String() == "false" {
			field.SetString("true")
		} else {
			field.SetString("false")
		}
		if reflect.TypeFor[Normalization]().Field(i).Name == "EOL" {
			field.SetString("lf")
		}
		changes = append(changes, q)
	}
	q := p
	q.GitVersion = "2.42.0"
	changes = append(changes, q)
	q = p
	q.ObjectFormat = "sha256"
	changes = append(changes, q)
	for _, name := range []string{"LocalExcludes", "GlobalExcludes", "LocalAttributes", "GlobalAttributes", "SystemAttributes"} {
		for _, b := range [][]byte{nil, []byte("x\n")} {
			q := p
			reflect.ValueOf(&q).Elem().FieldByName(name).Set(reflect.ValueOf(HostFile{Present: true, Bytes: b}))
			changes = append(changes, q)
		}
	}
	seen := map[[32]byte]bool{digest: true}
	for i, q := range changes {
		b, d, err := encodeCaptureInputs(q, "capture-rules/1")
		if err != nil || bytes.Equal(base, b) || seen[d] {
			t.Fatalf("change %d missing from identity: %v", i, err)
		}
		seen[d] = true
	}
	_, d, err := encodeCaptureInputs(p, "capture-rules/2")
	if err != nil || d == digest {
		t.Fatalf("rules omitted: %v", err)
	}
	decoded, err := decodeCaptureInputs(base)
	if err != nil {
		t.Fatal(err)
	}
	round, err := decoded.encode()
	if err != nil || !bytes.Equal(round, base) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestDecodeCaptureInputsRefuses(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := encodeCaptureInputs(p, "capture-rules/1")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1),
		bytes.Replace(b, []byte(`"version":1`), []byte(`"version":null`), 1),
		bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(b, []byte(`"version":1,`), nil, 1),
		append(append([]byte(nil), b...), '\n'),
		bytes.Replace(b, []byte(`"core.eol":"native"`), []byte(`"core.eol":"invalid"`), 1),
		bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"unknown":0`), 1),
	} {
		if _, err := decodeCaptureInputs(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q: %v", bad, err)
		}
	}
}

func TestTamperedMaterializationIsRewritten(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha256")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.LocalExcludes = HostFile{Present: true, Bytes: []byte("hidden\n")}
	p.Normalization.AutoCRLF = "true"
	dir := filepath.Join(f.colony, "workspaces", "inputs", "1")
	work := t.TempDir()
	index := filepath.Join(t.TempDir(), "index")
	writeTestFile(t, filepath.Join(work, "visible"), []byte("text\r\n"))
	writeTestFile(t, filepath.Join(work, "hidden"), []byte("private\n"))
	for range 2 {
		if err := materializeInputs(f.colony, dir, p); err != nil {
			t.Fatal(err)
		}
		env := captureEnvironment(f.env, f.colony, dir, work, index, p.CommonDir)
		got := gitCommand(t, env, work, nil, "ls-files", "-z", "-o", "--exclude-standard")
		if string(got) != "visible\x00" {
			t.Fatalf("tampered exclusions used: %q", got)
		}
		gitCommand(t, env, work, []byte("visible\x00"), "update-index", "-z", "--add", "--stdin")
		oid := strings.TrimSpace(string(gitCommand(t, env, work, nil, "rev-parse", "--verify", "--end-of-options", ":visible")))
		if blob := gitCommand(t, env, work, nil, "cat-file", "blob", oid); string(blob) != "text\n" {
			t.Fatalf("tampered normalization used: %q", blob)
		}
		if !bytes.Equal(readTestFile(t, filepath.Join(dir, "git", "info", "exclude")), p.LocalExcludes.Bytes) {
			t.Fatal("excludes not restored")
		}
		if err := os.Remove(index); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(dir, "git", "info", "exclude"), []byte("visible\n"))
		config := readTestFile(t, filepath.Join(dir, "git", "config"))
		writeTestFile(t, filepath.Join(dir, "git", "config"), bytes.Replace(config, []byte("autocrlf = true"), []byte("autocrlf = false"), 1))
	}
}

func TestPinnedGitVersionNamesBothVersions(t *testing.T) {
	t.Parallel()
	g := &gitRunner{version: "2.55.0.windows.3"}
	err := g.checkInputsVersion("2.42.0")
	var unavailable *UnavailableError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || unavailable.Reason != "inputs_stale" || !strings.Contains(unavailable.Detail, "2.42.0") || !strings.Contains(unavailable.Detail, g.version) {
		t.Fatalf("got %v", err)
	}
}

func TestInspectGlobalExcludePaths(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	home := environmentValue(f.env, "HOME")
	var env []string
	for _, s := range f.env {
		if !strings.HasPrefix(s, "XDG_CONFIG_HOME=") {
			env = append(env, s)
		}
	}
	f.env = env
	f.git.environ = func() []string { return f.env }
	want := []byte("from-default\n")
	writeTestFile(t, filepath.Join(home, ".config", "git", "ignore"), want)
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil || !p.GlobalExcludes.Present || !bytes.Equal(p.GlobalExcludes.Bytes, want) {
		t.Fatalf("HOME fallback: %#v, %v", p.GlobalExcludes, err)
	}
	want = []byte("from-config\n")
	writeTestFile(t, filepath.Join(home, "private-ignore"), want)
	gitCommand(t, f.env, f.root, nil, "config", "core.excludesFile", "~/private-ignore")
	p, err = f.git.inspect(context.Background(), f.root)
	if err != nil || !p.GlobalExcludes.Present || !bytes.Equal(p.GlobalExcludes.Bytes, want) {
		t.Fatalf("expanded configured path: %#v, %v", p.GlobalExcludes, err)
	}
}

func TestMaterializedAttributesPrecedence(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.SystemAttributes = HostFile{Present: true, Bytes: []byte("*.txt text eol=crlf")}
	p.GlobalAttributes = HostFile{Present: true, Bytes: []byte("*.txt eol=lf\n")}
	p.LocalAttributes = HostFile{Present: true, Bytes: []byte("special.txt -text\n")}
	dir := filepath.Join(f.colony, "workspaces", "inputs", "1")
	if err := materializeInputs(f.colony, dir, p); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	env := captureEnvironment(f.env, f.colony, dir, work, filepath.Join(t.TempDir(), "index"), p.CommonDir)
	b := gitCommand(t, env, work, []byte("ordinary.txt\x00special.txt\x00"), "check-attr", "-z", "--stdin", "text", "eol")
	want := "ordinary.txt\x00text\x00set\x00ordinary.txt\x00eol\x00lf\x00special.txt\x00text\x00unset\x00special.txt\x00eol\x00lf\x00"
	if string(b) != want {
		t.Fatalf("attribute precedence: %q", b)
	}
}

func TestCaptureInputsKeepHistoricalGitFloor(t *testing.T) {
	t.Parallel()
	// These format-v1 bytes were valid before integration raised the runtime
	// floor. Replaying them must remain possible without running that Git.
	body := []byte(`{"format":"dwarf-harness/capture-inputs","version":1,"rules":"capture-rules/1","git_version":"2.42.0","object_format":"sha1","core.autocrlf":"false","core.eol":"native","core.filemode":"true","core.ignorecase":"false","core.longpaths":"false","core.precomposeunicode":"false","core.symlinks":"true"}`)
	document, err := decodeCaptureInputs(body)
	if err != nil {
		t.Fatalf("historical input document rejected: %v", err)
	}
	encoded, err := document.encode()
	if err != nil || !bytes.Equal(encoded, body) {
		t.Fatalf("historical input bytes changed: %s %v", encoded, err)
	}
	if _, err := parseGitVersion([]byte("git version " + document.GitVersion)); err == nil {
		t.Fatal("historical document lowered the runtime Git floor")
	}
}
