package main

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// loadStartupConfiguration is the single configuration-source seam for both
// today's direct terminal and the later detached host composition.
func loadStartupConfiguration(ctx context.Context, store *ledgerstore.Store, path string, supplied bool) (terminal.Settings, bool, error) {
	var external []byte
	if supplied {
		external = []byte{}
	}
	configuration, err := setup.LoadConfiguration(ctx, store, external)
	if err != nil {
		return terminal.Settings{}, false, err
	}
	if !configuration.Managed {
		s, err := readSettings(path)
		return s, false, err
	}
	// This build has no host-settings decoder or validation runner. The opaque
	// document remains retained; no host capacity is inferred from it.
	s := terminal.Settings{}
	for _, c := range configuration.Contexts {
		s.Contexts = append(s.Contexts, terminal.ConfiguredContext(c.AgentID, c.ContextID, c.Settings))
	}
	return s, true, nil
}

func managedCredentialBindings(ctx context.Context, service *agents.Service, settings terminal.Settings) (terminal.Settings, error) {
	snapshot, err := service.OwnerSetupSnapshot(ctx)
	if err != nil {
		return settings, err
	}
	used := map[uuid.UUID]bool{}
	for _, c := range settings.Contexts {
		if used[c.CredentialScopeID] {
			continue
		}
		used[c.CredentialScopeID] = true
		latest := -1
		for i, credential := range snapshot.Credentials {
			if credential.ScopeID == c.CredentialScopeID && (latest < 0 || credential.Version > snapshot.Credentials[latest].Version) {
				latest = i
			}
		}
		// An unconfigured scope is a turn refusal, not a reason to fabricate a
		// binding or prevent the owner from opening the terminal.
		if latest < 0 {
			continue
		}
		credential := snapshot.Credentials[latest]
		settings.CredentialBindings = append(settings.CredentialBindings, terminal.CredentialBinding{ScopeID: credential.ScopeID, Provider: credential.Provider, SourceKind: credential.SourceKind, SourceLocator: credential.SourceLocator, RevokePreviousVersion: credential.CredentialVersion})
	}
	return settings, nil
}

// startupFailure never formats the wrapped error: native errors may contain
// paths or source locators. Only fixed codes and typed identities cross here.
func startupFailure(err error, target setup.Target, path string) *setup.Failure {
	var failure *setup.Failure
	if errors.As(err, &failure) {
		copy := *failure
		if copy.Path == "" {
			copy.Path = path
		}
		if copy.Target.Kind == "" {
			copy.Target = target
		}
		return &copy
	}
	code, fix := "storage_unavailable", "--colony-dir"
	var cause *agents.RefusalCause
	var refusal *terminal.RefusalError
	switch {
	case errors.As(err, &cause):
		code = cause.Code
		if cause.Scope.Kind != "" {
			target = setup.Target{Kind: cause.Scope.Kind, ID: cause.Scope.ID}
		}
		if cause.RouteID != (uuid.UUID{}) {
			target = setup.Target{Kind: "route", ID: cause.RouteID}
		}
		if cause.CredentialScopeID != (uuid.UUID{}) {
			target = setup.Target{Kind: "credential", ID: cause.CredentialScopeID}
		}
		switch code {
		case "route_missing", "route_revision":
			fix = "route set --input --review-out"
		case "credential_missing":
			fix = "credential bind --input --review-out"
		case "spend_ceiling_missing", "spend_disabled":
			fix = "spend set --input --review-out"
		}
	case errors.As(err, &refusal):
		code, path, fix = "review_invalid", refusal.Path, "--settings"
	case errors.Is(err, ledgerstore.ErrLocked):
		code, fix = "colony_in_use", "/quit"
	case errors.Is(err, credentials.ErrMissing):
		code, fix = "credential_missing", "credential bind --input --review-out"
	case errors.Is(err, credentials.ErrUnavailable):
		code, fix = "credential_unsupported", "credential bind --input --review-out"
	case errors.Is(err, credentials.ErrInvalid), errors.Is(err, credentials.ErrNative):
		code, fix = "credential_invalid", "credential bind --input --review-out"
	case errors.Is(err, agents.ErrIntegrity), errors.Is(err, agents.ErrMaterialization):
		code = "private_integrity"
	case errors.Is(err, agents.ErrRevision), errors.Is(err, workspace.ErrRevision):
		code, fix = "stale_revision", "context configure --input --review-out"
	case errors.Is(err, providers.ErrExpired):
		code, fix = "profile_expired", "route set --input --review-out"
	case errors.Is(err, providers.ErrInvalid), errors.Is(err, providers.ErrUnsupported):
		code, fix = "profile_invalid", "route set --input --review-out"
	case errors.Is(err, errSettingsFile), errors.Is(err, terminal.ErrSettings):
		code, path, fix = "review_invalid", "settings", "--settings"
	case errors.Is(err, agents.ErrInvalid), errors.Is(err, agents.ErrNotBound):
		code, fix = "review_invalid", "context configure --input --review-out"
	}
	if blocked, _ := workspace.ClassifyExecutionFailure(err); blocked {
		code = "process_recovery_required"
	}
	return commandFailure(code, path, fix, target)
}

func describeSetupFailure(f *setup.Failure) string {
	return fmt.Sprintf("code=%s target=%s/%s member=%s fix=%q", f.Code, f.Target.Kind, f.Target.ID, f.Path, f.FixCommand)
}
