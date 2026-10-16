package workspace

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/law"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ActivationWorkspacePolicy binds one workspace to opaque local artifacts.
// Their identity does not establish an available validation gate in C2.
type ActivationWorkspacePolicy struct {
	WorkspaceID                            uuid.UUID
	CheckPolicyHash, EnvironmentPolicyHash ledger.Digest
}

// ActivationTarget is the full configured policy map, not a per-workspace
// patch. WorkspacePolicies must be distinct and ascending by UUID bytes.
type ActivationTarget struct {
	FieldLawSHA256, ValidationPolicyHash ledger.Digest
	WorkspacePolicies                    []ActivationWorkspacePolicy
}

func validateActivationTarget(target ActivationTarget) error {
	if target.FieldLawSHA256 == (ledger.Digest{}) || target.ValidationPolicyHash == (ledger.Digest{}) {
		return fmt.Errorf("%w: global artifact digest is zero", ErrInvalid)
	}
	for i, policy := range target.WorkspacePolicies {
		switch {
		case !validID(policy.WorkspaceID):
			return fmt.Errorf("%w: workspace_policies element %d is not a UUIDv7", ErrInvalid, i)
		case policy.CheckPolicyHash == (ledger.Digest{}) || policy.EnvironmentPolicyHash == (ledger.Digest{}):
			return fmt.Errorf("%w: workspace_policies element %d artifact digest is zero", ErrInvalid, i)
		case i > 0 && bytes.Compare(target.WorkspacePolicies[i-1].WorkspaceID[:], policy.WorkspaceID[:]) >= 0:
			return fmt.Errorf("%w: workspace_policies element %d is not strictly ascending", ErrInvalid, i)
		}
	}
	return nil
}

// validateActivationArtifacts returns the exact sorted retention set. Global
// artifacts are loaded at admission; local artifacts have identity only. It
// neither publishes bytes nor borrows caller storage in its returned set.
func validateActivationArtifacts(target ActivationTarget, artifacts map[ledger.Digest][]byte) ([]ledger.Digest, error) {
	if err := validateActivationTarget(target); err != nil {
		return nil, err
	}
	refs := []ledger.Digest{target.FieldLawSHA256, target.ValidationPolicyHash}
	for _, policy := range target.WorkspacePolicies {
		refs = append(refs, policy.CheckPolicyHash, policy.EnvironmentPolicyHash)
	}
	slices.SortFunc(refs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
	refs = slices.Compact(refs)
	if len(artifacts) != len(refs) {
		return nil, fmt.Errorf("%w: artifacts must be the exact target digest set", ErrInvalid)
	}
	for _, digest := range refs {
		body, present := artifacts[digest]
		if !present {
			return nil, fmt.Errorf("%w: artifact %s is missing", ErrInvalid, digest)
		}
		// The loaders below compute the globals' SHA-256 over exact bytes.
		if digest != target.FieldLawSHA256 && digest != target.ValidationPolicyHash && ledger.Digest(sha256.Sum256(body)) != digest {
			return nil, fmt.Errorf("%w: artifact %s differs from its digest", ErrInvalid, digest)
		}
	}
	field, err := law.Load(artifacts[target.FieldLawSHA256], law.StandardV1())
	if err != nil {
		return nil, fmt.Errorf("%w: field law: %w", ErrInvalid, err)
	}
	if ledger.Digest(field.SHA256) != target.FieldLawSHA256 {
		return nil, fmt.Errorf("%w: field law differs from its digest", ErrInvalid)
	}
	policy, err := law.LoadValidationPolicy(artifacts[target.ValidationPolicyHash], law.StandardV1())
	if err != nil {
		return nil, fmt.Errorf("%w: validation policy: %w", ErrInvalid, err)
	}
	if policy.SHA256 != law.Digest(target.ValidationPolicyHash) {
		return nil, fmt.Errorf("%w: validation policy differs from its digest", ErrInvalid)
	}
	return refs, nil
}

// activationAffectedWorkspaces compares validated complete targets. A nil
// prior is bootstrap; no registration or unresolved-intent set enlarges it.
func activationAffectedWorkspaces(prior *ActivationTarget, next ActivationTarget) []uuid.UUID {
	old := make(map[uuid.UUID]ActivationWorkspacePolicy)
	global := prior == nil
	if prior != nil {
		global = prior.FieldLawSHA256 != next.FieldLawSHA256 || prior.ValidationPolicyHash != next.ValidationPolicyHash
		for _, policy := range prior.WorkspacePolicies {
			old[policy.WorkspaceID] = policy
		}
	}
	var affected []uuid.UUID
	for _, policy := range next.WorkspacePolicies {
		if global || old[policy.WorkspaceID] != policy {
			affected = append(affected, policy.WorkspaceID)
		}
		delete(old, policy.WorkspaceID)
	}
	// Removed entries are affected even when both globals remain unchanged.
	for id := range old {
		affected = append(affected, id)
	}
	slices.SortFunc(affected, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return affected
}
