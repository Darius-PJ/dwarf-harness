package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
	"uuid"
)

// A conflict never carries Git's conflicted result tree as a usable candidate.
type mergeResult struct {
	Tree  string
	Cause string
	Paths []string
}

func (e *captureEngine) prepareIntegration(ctx context.Context, s captureScratch) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := e.git.checkInputsVersion(s.Inputs.GitVersion); err != nil {
		return nil, err
	}
	if err := materializeInputs(e.colony, s.InputsDir, s.Inputs); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.TmpDir, 0o700); err != nil {
		return nil, err
	}
	return integrationEnvironment(e.git.environ(), e.colony, s.InputsDir, s.Inputs.CommonDir), nil
}

func (e *captureEngine) mergeTrees(ctx context.Context, s captureScratch, base, predecessor, submission string) (mergeResult, error) {
	var zero mergeResult
	n := oidLength(s.Inputs.ObjectFormat)
	if !validOID(base, n) || !validOID(predecessor, n) || !validOID(submission, n) {
		return zero, fmt.Errorf("%w: invalid integration trees", ErrInvalid)
	}
	env, err := e.prepareIntegration(ctx, s)
	if err != nil {
		return zero, err
	}
	out, exit, err := e.git.run(ctx, s.TmpDir, env, nil, []int{0, 1}, "-c", "core.bare=true", "merge-tree", "--write-tree", "-z", "--name-only", "--no-messages", "--merge-base="+base, "--end-of-options", predecessor, submission)
	if err != nil {
		return zero, err
	}
	tree, paths, err := parseMergeTree(out, exit, n)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrGit, err)
	}
	if exit == 1 {
		sort.Strings(paths)
		return mergeResult{Cause: "merge", Paths: paths}, nil
	}
	out, _, err = e.git.repository(ctx, e.colony, s.Inputs.CommonDir, nil, []int{0}, "ls-tree", "-r", "-z", "--name-only", "--full-tree", "--end-of-options", tree)
	if err != nil {
		return zero, err
	}
	paths, err = parseNULList(out)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrGit, err)
	}
	if first, second := firstCaseCollision(paths); first != "" {
		return mergeResult{Cause: "case_collision", Paths: []string{first, second}}, nil
	}
	return mergeResult{Tree: tree}, nil
}

func (e *captureEngine) integrationCommit(ctx context.Context, s captureScratch, tree, parent string, requestID, generationID uuid.UUID) (string, error) {
	n := oidLength(s.Inputs.ObjectFormat)
	if !validOID(tree, n) || !validOID(parent, n) || !validID(requestID) || !validID(generationID) {
		return "", fmt.Errorf("%w: invalid integration provenance", ErrInvalid)
	}
	message := fmt.Sprintf("dwarf-harness integration\n\nrequest %s\ngeneration %s", requestID, generationID)
	return e.deterministicCommit(ctx, s, tree, parent, generationID, message)
}

func (e *captureEngine) deterministicCommit(ctx context.Context, s captureScratch, tree, parent string, id uuid.UUID, message string) (string, error) {
	n := oidLength(s.Inputs.ObjectFormat)
	if !validOID(tree, n) || !validOID(parent, n) || !validID(id) {
		return "", fmt.Errorf("%w: invalid commit provenance", ErrInvalid)
	}
	env, err := e.prepareIntegration(ctx, s)
	if err != nil {
		return "", err
	}
	// RFC 9562: UUIDv7's leading 48 bits are Unix milliseconds.
	var milliseconds uint64
	for _, b := range id[:6] {
		milliseconds = milliseconds<<8 | uint64(b)
	}
	date := fmt.Sprintf("%d +0000", milliseconds/1000)
	env = append(env, "GIT_AUTHOR_NAME=DwarfHarness Core", "GIT_AUTHOR_EMAIL=core@dwarf-harness.invalid", "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME=DwarfHarness Core", "GIT_COMMITTER_EMAIL=core@dwarf-harness.invalid", "GIT_COMMITTER_DATE="+date)
	out, _, err := e.git.run(ctx, s.TmpDir, env, nil, []int{0}, "-c", "core.bare=true", "commit-tree", "-p", parent, "-m", message, "--end-of-options", tree)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSuffix(string(out), "\n")
	if !validOID(commit, n) {
		return "", fmt.Errorf("%w: invalid integration commit", ErrGit)
	}
	return commit, nil
}

func (g *gitRunner) createGenerationRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.createCommitRef(ctx, colony, p, "generations", id, commit)
}

func (g *gitRunner) deleteGenerationRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.deleteCommitRef(ctx, colony, p, "generations", id, commit)
}

func commitRef(p Proposal, namespace string, id uuid.UUID, commit string) (string, error) {
	if (namespace != "generations" && namespace != "scratch-commits") || !validID(id) || !validOID(commit, oidLength(p.ObjectFormat)) {
		return "", fmt.Errorf("%w: invalid commit retention ref", ErrInvalid)
	}
	return "refs/dwarf-harness/" + namespace + "/" + id.String(), nil
}

func (g *gitRunner) createCommitRef(ctx context.Context, colony string, p Proposal, namespace string, id uuid.UUID, commit string) error {
	ref, err := commitRef(p, namespace, id, commit)
	if err != nil {
		return err
	}
	return g.createRef(ctx, colony, p.CommonDir, ref, commit)
}

func (g *gitRunner) deleteCommitRef(ctx context.Context, colony string, p Proposal, namespace string, id uuid.UUID, commit string) error {
	ref, err := commitRef(p, namespace, id, commit)
	if err != nil {
		return err
	}
	_, _, err = g.repository(ctx, colony, p.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, commit)
	return err
}

const maxConflictPaths = 10_000

// conflictReport returns bytes and their digest; its caller publishes and pins.
func conflictReport(cause string, paths []string) ([]byte, [32]byte, error) {
	if cause != "merge" && cause != "case_collision" {
		return nil, [32]byte{}, fmt.Errorf("%w: invalid conflict cause", ErrInvalid)
	}
	for _, path := range paths {
		if !utf8.ValidString(path) {
			return nil, [32]byte{}, fmt.Errorf("%w: non-UTF-8 conflict path", ErrInvalid)
		}
	}
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)
	unique := ordered[:0]
	for _, path := range ordered {
		if len(unique) == 0 || unique[len(unique)-1] != path {
			unique = append(unique, path)
		}
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	header := struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Cause   string `json:"cause"`
		Paths   int    `json:"paths"`
	}{"dwarf-harness/conflict-report", 1, cause, len(unique)}
	if err := encoder.Encode(header); err != nil {
		return nil, [32]byte{}, err
	}
	for _, path := range unique[:min(len(unique), maxConflictPaths)] {
		if err := encoder.Encode(struct {
			Path string `json:"path"`
		}{path}); err != nil {
			return nil, [32]byte{}, err
		}
	}
	return body.Bytes(), sha256.Sum256(body.Bytes()), nil
}
