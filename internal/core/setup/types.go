package setup

import (
	"encoding/json"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// Services borrows the single owner's already open services. The host serializes
// setup with other owner configuration operations and never enables a turn loop.
type Services struct {
	Store       *ledgerstore.Store
	Agents      *agents.Service
	Workspace   *workspace.Service
	afterNative func(string)
}

type Target struct {
	Kind    string    `json:"kind"`
	ID      uuid.UUID `json:"id,omitzero"`
	AgentID uuid.UUID `json:"agent_id,omitzero"`
}

type Step struct {
	CommandID        uuid.UUID       `json:"command_id"`
	Operation        string          `json:"operation"`
	Target           Target          `json:"target"`
	ExpectedRevision int64           `json:"expected_revision"`
	Origin           *ledger.Binding `json:"origin,omitempty"`
	Arguments        json.RawMessage `json:"arguments"`
}

type Artifact struct {
	SHA256 ledger.Digest `json:"sha256"`
	Bytes  int64         `json:"bytes"`
	Data   []byte        `json:"data_base64,omitempty"`
}

// ReviewDocument is owner-private: instructions and nonsecret source locators
// are visible only in the explicit review, never in ordinary result formatting.
type ReviewDocument struct {
	Format       string        `json:"format"`
	Schema       int           `json:"schema"`
	ColonyID     uuid.UUID     `json:"colony_id"`
	Directory    string        `json:"directory"`
	Operation    string        `json:"operation"`
	Steps        []Step        `json:"steps"`
	Artifacts    []Artifact    `json:"artifacts"`
	ReviewSHA256 ledger.Digest `json:"review_sha256"`
}

type Receipt struct {
	CommandID        uuid.UUID     `json:"command_id"`
	Operation        string        `json:"operation"`
	Target           Target        `json:"target"`
	SubmissionSHA256 ledger.Digest `json:"submission_sha256"`
	Revision         int64         `json:"revision"`
	Sequence         uint64        `json:"sequence"`
}

type Failure struct {
	Code       string `json:"code"`
	Operation  string `json:"operation,omitempty"`
	Target     Target `json:"target"`
	Path       string `json:"path,omitempty"`
	FixCommand string `json:"fix_command"`
	cause      error
}

func (e *Failure) Error() string { return "setup: " + e.Code }
func (e *Failure) Unwrap() error { return e.cause }

func fail(code, operation string, target Target, cause error) error {
	fix := operation
	switch fix {
	case "capacity configure":
		fix = "capacity set"
	case "grant.issue":
		fix = "grant issue"
	}
	if fix != "" {
		fix += " --input --review-out"
	}
	switch code {
	case "approval_required":
		fix = operation + " --apply --approve"
		if operation == "capacity configure" {
			fix = "capacity set --apply --approve"
		}
	case "review_changed", "review_invalid":
		if operation == "" || operation == "review" {
			fix = "--input --review-out"
		}
	case "creation_incomplete", "colony_exists":
		fix = "colony create --input --review-out"
	case "legacy_configuration", "settings_required":
		fix = "--settings"
	case "mixed_configuration":
		fix = "--colony-dir"
	case "setup_required", "agent_exists":
		fix = "agent create --input --review-out"
	case "context_busy", "colony_in_use":
		fix = "/quit"
	case "provider_move_requires_context_move":
		fix = "context configure --input --review-out"
	case "route_missing", "route_revision", "profile_invalid", "profile_expired":
		fix = "route set --input --review-out"
	case "credential_missing", "credential_invalid", "credential_unsupported", "credential_revoked":
		fix = "credential bind --input --review-out"
	case "spend_ceiling_missing", "spend_disabled", "spend_period_mismatch":
		fix = "spend set --input --review-out"
	case "private_integrity", "storage_unavailable":
		fix = "--colony-dir"
	}
	if fix == "" {
		fix = "--input --review-out"
	}
	return &Failure{Code: code, Operation: operation, Target: target, FixCommand: fix, cause: cause}
}

func classify(err error, operation string, target Target) error {
	if err == nil {
		return nil
	}
	var f *Failure
	if errors.As(err, &f) {
		return err
	}
	code := "storage_unavailable"
	var paid *agents.RefusalCause
	var busy *agents.BusyError
	switch {
	case errors.As(err, &paid):
		code = paid.Code
		if paid.Scope.Kind != "" {
			target = Target{Kind: paid.Scope.Kind, ID: paid.Scope.ID}
		}
		if paid.RouteID != (uuid.UUID{}) {
			target = Target{Kind: "route", ID: paid.RouteID}
		}
		if paid.CredentialScopeID != (uuid.UUID{}) {
			target = Target{Kind: "credential", ID: paid.CredentialScopeID}
		}
	case errors.Is(err, ledgerstore.ErrCommandMismatch), errors.Is(err, agents.ErrCommandMismatch):
		code = "command_mismatch"
	case errors.Is(err, ledgerstore.ErrLocked):
		code = "colony_in_use"
	case errors.Is(err, agents.ErrRevision), errors.Is(err, workspace.ErrRevision), errors.Is(err, ledgerstore.ErrTailMoved):
		code = "stale_revision"
	case errors.Is(err, workspace.ErrStaleProposal):
		code = "stale_proposal"
	case errors.Is(err, workspace.ErrExists):
		code = "workspace_exists"
	case errors.Is(err, ledgerstore.ErrExists):
		code = "colony_exists"
	case errors.Is(err, providers.ErrExpired):
		code = "profile_expired"
	case errors.Is(err, providers.ErrInvalid), errors.Is(err, providers.ErrUnsupported):
		code = "profile_invalid"
		if operation == "credential bind" || operation == "credential revoke" {
			code = "credential_unsupported"
		}
	case errors.Is(err, credentials.ErrMissing):
		code = "credential_missing"
	case errors.Is(err, credentials.ErrUnavailable):
		code = "credential_unsupported"
	case errors.Is(err, credentials.ErrInvalid), errors.Is(err, credentials.ErrNative):
		code = "credential_invalid"
	case errors.Is(err, agents.ErrAlreadyRevoked):
		code = "credential_revoked"
	case errors.Is(err, agents.ErrPaidPeriod):
		code = "spend_period_mismatch"
	case errors.Is(err, agents.ErrIntegrity), errors.Is(err, agents.ErrMaterialization):
		code = "private_integrity"
	case errors.As(err, &busy):
		code = "context_busy"
	case errors.Is(err, agents.ErrNotBound), errors.Is(err, agents.ErrInvalid), errors.Is(err, ledger.ErrInvalid), errors.Is(err, workspace.ErrInvalid):
		code = "review_invalid"
	case errors.Is(err, agents.ErrDenied):
		code = "grant_missing"
	}
	return fail(code, operation, target, err)
}
