package workspace

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/law"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func activationWorkspaceID(last byte) uuid.UUID {
	return uuid.UUID{0x01, 0x99, 0xc0, 0, 0, 0, 0x70, 0, 0x80, 0, 0, 0, 0, 0, 0, last}
}

func activationTestArtifacts(t *testing.T) (ActivationTarget, map[ledger.Digest][]byte) {
	t.Helper()
	read := func(parts ...string) []byte {
		t.Helper()
		path := filepath.Join(append([]string{"..", "..", "..", "contract", "law", "testdata"}, parts...)...)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	field := read("law", "v1", "minimal.json")
	policy := read("validation-policy", "v1", "policy.json")
	// C2 treats local bodies as identities, not as JSON or executable checks.
	check, environment := []byte{0xff, 0, 'c'}, []byte{}
	artifacts := make(map[ledger.Digest][]byte)
	add := func(body []byte) ledger.Digest {
		d := ledger.Digest(sha256.Sum256(body))
		artifacts[d] = body
		return d
	}
	target := ActivationTarget{FieldLawSHA256: add(field), ValidationPolicyHash: add(policy)}
	target.WorkspacePolicies = []ActivationWorkspacePolicy{
		{activationWorkspaceID(1), add(check), add(environment)},
		{activationWorkspaceID(2), add(check), add(environment)},
	}
	return target, artifacts
}

func TestActivationTargetValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*ActivationTarget)
	}{
		{"zero_law", func(p *ActivationTarget) { p.FieldLawSHA256 = ledger.Digest{} }},
		{"zero_policy", func(p *ActivationTarget) { p.ValidationPolicyHash = ledger.Digest{} }},
		{"zero_check", func(p *ActivationTarget) { p.WorkspacePolicies[0].CheckPolicyHash = ledger.Digest{} }},
		{"zero_environment", func(p *ActivationTarget) { p.WorkspacePolicies[0].EnvironmentPolicyHash = ledger.Digest{} }},
		{"zero_workspace", func(p *ActivationTarget) { p.WorkspacePolicies[0].WorkspaceID = uuid.UUID{} }},
		{"wrong_uuid_version", func(p *ActivationTarget) { p.WorkspacePolicies[0].WorkspaceID[6] = 0x40 }},
		{"wrong_uuid_variant", func(p *ActivationTarget) { p.WorkspacePolicies[0].WorkspaceID[8] = 0 }},
		{"unsorted", func(p *ActivationTarget) { slices.Reverse(p.WorkspacePolicies) }},
		{"duplicate", func(p *ActivationTarget) { p.WorkspacePolicies[1].WorkspaceID = p.WorkspacePolicies[0].WorkspaceID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, _ := activationTestArtifacts(t)
			tc.change(&target)
			if err := validateActivationTarget(target); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid target accepted: %v", err)
			}
		})
	}
}

func TestActivationArtifactSet(t *testing.T) {
	t.Parallel()
	t.Run("exact_shared_union", func(t *testing.T) {
		target, artifacts := activationTestArtifacts(t)
		target.WorkspacePolicies[1].CheckPolicyHash = target.FieldLawSHA256
		target.WorkspacePolicies[1].EnvironmentPolicyHash = target.ValidationPolicyHash
		refs, err := validateActivationArtifacts(target, artifacts)
		if err != nil {
			t.Fatal(err)
		}
		want := []ledger.Digest{target.FieldLawSHA256, target.ValidationPolicyHash, target.WorkspacePolicies[0].CheckPolicyHash, target.WorkspacePolicies[0].EnvironmentPolicyHash}
		slices.SortFunc(want, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
		if !slices.Equal(refs, want) {
			t.Fatalf("retention set = %v; want exact shared union %v", refs, want)
		}
		field, err := law.Load(artifacts[target.FieldLawSHA256], law.StandardV1())
		if err != nil || ledger.Digest(field.SHA256) != target.FieldLawSHA256 {
			t.Fatalf("law bytes lost identity at ledger boundary: %v", err)
		}
		policy, err := law.LoadValidationPolicy(artifacts[target.ValidationPolicyHash], law.StandardV1())
		if err != nil || policy.SHA256 != law.Digest(target.ValidationPolicyHash) {
			t.Fatalf("policy bytes lost identity at law boundary: %v", err)
		}
	})
	t.Run("global_only", func(t *testing.T) {
		target, artifacts := activationTestArtifacts(t)
		delete(artifacts, target.WorkspacePolicies[0].CheckPolicyHash)
		delete(artifacts, target.WorkspacePolicies[0].EnvironmentPolicyHash)
		target.WorkspacePolicies = nil
		refs, err := validateActivationArtifacts(target, artifacts)
		if err != nil || len(refs) != 2 || !slices.Contains(refs, target.FieldLawSHA256) || !slices.Contains(refs, target.ValidationPolicyHash) {
			t.Fatalf("global-only target: %v, %v", refs, err)
		}
		delete(artifacts, target.FieldLawSHA256)
		if _, err := validateActivationArtifacts(target, artifacts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing global artifact accepted without workspaces: %v", err)
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*ActivationTarget, map[ledger.Digest][]byte)
	}{
		{"missing_law", func(p *ActivationTarget, a map[ledger.Digest][]byte) { delete(a, p.FieldLawSHA256) }},
		{"missing_policy", func(p *ActivationTarget, a map[ledger.Digest][]byte) { delete(a, p.ValidationPolicyHash) }},
		{"missing_local", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			delete(a, p.WorkspacePolicies[0].CheckPolicyHash)
		}},
		{"extra_body", func(_ *ActivationTarget, a map[ledger.Digest][]byte) {
			a[sha256.Sum256([]byte("extra"))] = []byte("extra")
		}},
		{"wrong_key_same_count", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			delete(a, p.WorkspacePolicies[0].CheckPolicyHash)
			a[sha256.Sum256([]byte("extra"))] = []byte("extra")
		}},
		{"mismatched_bytes", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			a[p.WorkspacePolicies[0].CheckPolicyHash] = []byte("different")
		}},
		{"law_exact_bytes", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			a[p.FieldLawSHA256] = append(a[p.FieldLawSHA256], '\n')
		}},
		{"policy_exact_bytes", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			a[p.ValidationPolicyHash] = append(a[p.ValidationPolicyHash], '\n')
		}},
		{"invalid_law", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			delete(a, p.FieldLawSHA256)
			body := []byte(`{"format":"dwarf-harness/undercurrent-law","schema_version":1}`)
			p.FieldLawSHA256 = sha256.Sum256(body)
			a[p.FieldLawSHA256] = body
		}},
		{"invalid_policy", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			delete(a, p.ValidationPolicyHash)
			body := []byte(`{"format":"dwarf-harness/validation-policy","schema_version":1}`)
			p.ValidationPolicyHash = sha256.Sum256(body)
			a[p.ValidationPolicyHash] = body
		}},
		{"global_digest_alias", func(p *ActivationTarget, a map[ledger.Digest][]byte) {
			delete(a, p.ValidationPolicyHash)
			p.ValidationPolicyHash = p.FieldLawSHA256
		}},
		{"invalid_target", func(p *ActivationTarget, _ map[ledger.Digest][]byte) { slices.Reverse(p.WorkspacePolicies) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, artifacts := activationTestArtifacts(t)
			tc.change(&target, artifacts)
			if refs, err := validateActivationArtifacts(target, artifacts); !errors.Is(err, ErrInvalid) || refs != nil {
				t.Fatalf("invalid artifacts produced usable retention set %v: %v", refs, err)
			}
		})
	}
}

func TestActivationArtifactLimits(t *testing.T) {
	t.Parallel()
	target, artifacts := activationTestArtifacts(t)
	body := artifacts[target.FieldLawSHA256]
	for _, size := range []int{law.StandardV1().Bytes, law.StandardV1().Bytes + 1} {
		delete(artifacts, target.FieldLawSHA256)
		padded := append(slices.Clone(body), bytes.Repeat([]byte(" "), size-len(body))...)
		target.FieldLawSHA256 = sha256.Sum256(padded)
		artifacts[target.FieldLawSHA256] = padded
		refs, err := validateActivationArtifacts(target, artifacts)
		if size == law.StandardV1().Bytes {
			if err != nil || !slices.Contains(refs, target.FieldLawSHA256) {
				t.Fatalf("StandardV1 boundary body lost exact identity: %v, %v", refs, err)
			}
		} else if !errors.Is(err, ErrInvalid) || refs != nil {
			t.Fatalf("oversized field law accepted: %v, %v", refs, err)
		}
	}
}

func TestActivationAffectedWorkspaces(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*ActivationTarget)
		want   []uuid.UUID
	}{
		{"unchanged", func(*ActivationTarget) {}, nil},
		{"checks_only", func(p *ActivationTarget) { p.WorkspacePolicies[0].CheckPolicyHash = ledger.Digest{3} }, []uuid.UUID{activationWorkspaceID(1)}},
		{"environment_only", func(p *ActivationTarget) { p.WorkspacePolicies[1].EnvironmentPolicyHash = ledger.Digest{4} }, []uuid.UUID{activationWorkspaceID(2)}},
		{"removed", func(p *ActivationTarget) { p.WorkspacePolicies = p.WorkspacePolicies[1:] }, []uuid.UUID{activationWorkspaceID(1)}},
		{"all_removed", func(p *ActivationTarget) { p.WorkspacePolicies = nil }, []uuid.UUID{activationWorkspaceID(1), activationWorkspaceID(2)}},
		{"added", func(p *ActivationTarget) {
			p.WorkspacePolicies = append(p.WorkspacePolicies, ActivationWorkspacePolicy{activationWorkspaceID(3), ledger.Digest{3}, ledger.Digest{4}})
		}, []uuid.UUID{activationWorkspaceID(3)}},
		{"removed_and_added", func(p *ActivationTarget) {
			p.WorkspacePolicies[0] = p.WorkspacePolicies[1]
			p.WorkspacePolicies[1].WorkspaceID = activationWorkspaceID(3)
		}, []uuid.UUID{activationWorkspaceID(1), activationWorkspaceID(3)}},
		{"law_changed", func(p *ActivationTarget) { p.FieldLawSHA256 = ledger.Digest{5} }, []uuid.UUID{activationWorkspaceID(1), activationWorkspaceID(2)}},
		{"policy_changed_union", func(p *ActivationTarget) {
			p.ValidationPolicyHash = ledger.Digest{6}
			p.WorkspacePolicies[0] = p.WorkspacePolicies[1]
			p.WorkspacePolicies[1].WorkspaceID = activationWorkspaceID(3)
		}, []uuid.UUID{activationWorkspaceID(1), activationWorkspaceID(2), activationWorkspaceID(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior, _ := activationTestArtifacts(t)
			next := prior
			next.WorkspacePolicies = slices.Clone(prior.WorkspacePolicies)
			tc.change(&next)
			if got := activationAffectedWorkspaces(&prior, next); !slices.Equal(got, tc.want) {
				t.Fatalf("affected = %v; want %v", got, tc.want)
			}
		})
	}
	t.Run("bootstrap", func(t *testing.T) {
		target, _ := activationTestArtifacts(t)
		target.WorkspacePolicies = target.WorkspacePolicies[1:]
		if got := activationAffectedWorkspaces(nil, target); !slices.Equal(got, []uuid.UUID{activationWorkspaceID(2)}) {
			t.Fatalf("bootstrap must include only target workspaces: %v", got)
		}
		target.WorkspacePolicies = nil
		if got := activationAffectedWorkspaces(nil, target); len(got) != 0 {
			t.Fatalf("empty bootstrap acquired scope: %v", got)
		}
	})
}
