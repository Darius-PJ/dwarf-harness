package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

var workspaceOpenOverride func(context.Context, *ledgerstore.Store, string, workspace.Config) (*workspace.Service, workspace.Reconciliation, error)
var workspaceCloseOverride func(*agents.Service, func() error) error

var ErrSettingsFile = errors.New("settings file unreadable")

// Failure contains only public classes. The originating error is never sent to
// a view. Conditions preserve Core's existing startup refusal evidence.
type Failure struct {
	Stage      string         `json:"stage"`
	Class      string         `json:"class"`
	Code       string         `json:"code"`
	Conditions []string       `json:"conditions,omitempty"`
	Target     setup.Target   `json:"target"`
	Refusal    *setup.Failure `json:"refusal,omitempty"`
	cause      error
}

func (f *Failure) Error() string { return "startup refused: " + f.Stage + ": " + f.Class }
func (f *Failure) Unwrap() error { return f.cause }

// Configuration supplies the owner's configuration source and managed
// credential selection. Load runs before agents open; Bindings runs afterwards.
type Configuration struct {
	Load     func(context.Context, *ledgerstore.Store) (terminal.Settings, bool, error)
	Bindings func(context.Context, *agents.Service, terminal.Settings) (terminal.Settings, error)
}

// Composition owns the single ordered startup and reverse dependency drain.
// A direct owner command may stop after any stage, before EnableTurnLoop.
// Methods are called serially; only the ready Service is shared with dispatch.
type Composition struct {
	Dir             string
	Settings        terminal.Settings
	Output          io.Writer
	Store           *ledgerstore.Store
	Service         *agents.Service
	Workspace       *workspace.Service
	Report          agents.Reconciliation
	WorkspaceReport workspace.Reconciliation
	Bound           []terminal.ScopeBinding
	Conditions      []string
	stage           int
	configuration   Configuration
	managed         bool
}

func NewComposition(dir string, configuration Configuration, output io.Writer) *Composition {
	if output == nil {
		output = io.Discard
	}
	return &Composition{Dir: dir, configuration: configuration, Output: output}
}
func (c *Composition) failure(stage string, err error) *Failure {
	failure := &Failure{Stage: stage, Class: Describe(err), Code: "startup_failed", Target: setup.Target{Kind: "colony"}, cause: err}
	if c.Store != nil {
		failure.Target.ID = c.Store.Colony()
	}
	for _, condition := range c.Conditions {
		switch condition {
		case "process_unconfirmed", "private_integrity", "service_recovery_pending":
			failure.Conditions = append(failure.Conditions, condition)
		}
	}
	return failure
}
func (c *Composition) Compose(ctx context.Context, claim *ledgerstore.Claim) *Failure {
	if failure := c.OpenStore(claim); failure != nil {
		return failure
	}
	for _, stage := range []func(context.Context) *Failure{c.OpenAgents, c.OpenWorkspace, c.RecoverLoops, c.BindCredentials, c.EnableTurnLoop} {
		if failure := stage(ctx); failure != nil {
			return failure
		}
	}
	return nil
}
func (c *Composition) OpenStore(claim *ledgerstore.Claim) *Failure {
	if c.stage != 0 {
		return c.failure("colony", agents.ErrInvalid)
	}
	var err error
	if claim == nil {
		c.Store, _, err = ledgerstore.Open(c.Dir, ledger.Defined())
	} else {
		c.Store, _, err = claim.Open(ledger.Defined())
	}
	if err != nil {
		return c.failure("colony", err)
	}
	c.stage = 1
	return nil
}
func (c *Composition) OpenAgents(ctx context.Context) *Failure {
	if c.stage != 1 {
		return c.failure("core", agents.ErrInvalid)
	}
	var err error
	if c.configuration.Load == nil {
		return c.failure("settings", agents.ErrInvalid)
	}
	c.Settings, c.managed, err = c.configuration.Load(ctx, c.Store)
	if err != nil {
		return c.failure("settings", err)
	}
	if c.managed {
		fmt.Fprintln(c.Output, "[terminal] validation disabled: this build has no validation startup settings")
	}
	c.Service, c.Report, err = agents.OpenPaid(ctx, c.Store, c.Dir, agents.DefaultLimits(), agents.PaidOptions{Limits: agents.DefaultPaidLimits()})
	if err != nil {
		return c.failure("core", err)
	}
	c.stage = 2
	return nil
}
func (c *Composition) OpenWorkspace(ctx context.Context) *Failure {
	if c.stage != 2 {
		return c.failure("workspace", agents.ErrInvalid)
	}
	profile := workspace.ProvisionalCapacityProfile()
	cfg := workspace.Config{Capacity: &profile}
	var err error
	if workspaceOpenOverride != nil {
		c.Workspace, c.WorkspaceReport, err = workspaceOpenOverride(ctx, c.Store, c.Dir, cfg)
	} else {
		c.Workspace, c.WorkspaceReport, err = workspace.Open(ctx, c.Store, c.Dir, cfg)
	}
	if err != nil {
		if blocked, _ := workspace.ClassifyExecutionFailure(err); blocked {
			c.Conditions = append(c.Conditions, "process_unconfirmed")
			_, recoveryErr := c.Service.ReconcileServiceEvidence(ctx, nil)
			_ = c.printService(ctx, recoveryErr)
		}
		return c.failure("workspace", err)
	}
	c.stage = 3
	return nil
}
func (c *Composition) RecoverLoops(ctx context.Context) *Failure {
	if c.stage != 3 {
		return c.failure("turn recovery", agents.ErrInvalid)
	}
	fmt.Fprintf(c.Output, "[terminal] colony opened: %d integrity issues, %d pending paid attempts, %d unresumable turns\n", len(c.Report.Issues), len(c.Report.PendingPaid), len(c.Report.PendingLoops))
	for _, pending := range c.Report.PendingLoops {
		if pending.ToolID != (uuid.UUID{}) {
			continue
		}
		_, err := c.Service.ReconcileLoop(ctx, agents.LoopReconciliation{CommandID: uuid.NewV7(), TurnID: pending.TurnID, ExpectedOwnerVersion: pending.OwnerVersion})
		if errors.Is(err, agents.ErrRecoveryPending) {
			fmt.Fprintf(c.Output, "[terminal] turn %s recovery pending: %s\n", pending.TurnID, Describe(err))
			continue
		}
		if err != nil {
			return c.failure("turn recovery", err)
		}
	}
	c.stage = 4
	return nil
}
func (c *Composition) BindCredentials(ctx context.Context) *Failure {
	if c.stage != 4 {
		return c.failure("credential binding", agents.ErrInvalid)
	}
	if c.managed {
		if c.configuration.Bindings == nil {
			return c.failure("credential binding", agents.ErrInvalid)
		}
		var err error
		c.Settings, err = c.configuration.Bindings(ctx, c.Service, c.Settings)
		if err != nil {
			return c.failure("credential binding", err)
		}
	}
	for _, b := range c.Settings.CredentialBindings {
		var origin agents.Binding
		for _, setting := range c.Settings.Contexts {
			if setting.CredentialScopeID == b.ScopeID {
				origin = agents.Binding{AgentID: setting.AgentID, ContextID: setting.ContextID}
				break
			}
		}
		scope, err := c.Service.CredentialScopeStatus(ctx, agents.CredentialScopeLookup{ScopeID: b.ScopeID})
		if err != nil {
			return c.failure("credential binding", err)
		}
		version := uuid.NewV7()
		_, err = c.Service.BindCredential(ctx, origin, agents.CredentialBinding{CommandID: uuid.NewV7(), ScopeID: b.ScopeID, ExpectedVersion: scope.Version, Provider: b.Provider, CredentialVersion: version, SourceKind: b.SourceKind, SourceLocator: b.SourceLocator, RevokePreviousVersion: b.RevokePreviousVersion})
		if err != nil {
			failure := c.failure("credential binding", err)
			failure.Target = setup.Target{Kind: "credential", ID: b.ScopeID}
			return failure
		}
		after, err := c.Service.CredentialScopeStatus(ctx, agents.CredentialScopeLookup{ScopeID: b.ScopeID})
		if err != nil {
			return c.failure("credential binding", err)
		}
		binding := terminal.ScopeBinding{ScopeID: b.ScopeID, Provider: b.Provider, Version: version}
		for _, id := range after.ActiveVersions {
			if id != version {
				binding.Others = append(binding.Others, id)
			}
		}
		c.Bound = append(c.Bound, binding)
	}
	c.stage = 5
	return nil
}
func (c *Composition) EnableTurnLoop(ctx context.Context) *Failure {
	if c.stage != 5 {
		return c.failure("turn loop", agents.ErrInvalid)
	}
	options := agents.LoopOptions{}
	for _, setting := range c.Settings.Contexts {
		loop := setup.LoopSetting(agents.Binding{AgentID: setting.AgentID, ContextID: setting.ContextID}, setting.Configuration())
		if err := agents.ValidateSetupTemplate(loop); err != nil {
			return c.failure("turn loop", &setup.Failure{Code: "review_invalid", Path: "turn_policy", FixCommand: "context configure --input --review-out", Target: setup.Target{Kind: "context", ID: setting.ContextID, AgentID: setting.AgentID}})
		}
		options.Contexts = append(options.Contexts, loop)
	}
	if err := c.Service.EnableTurnLoop(ctx, c.Workspace, options); err != nil {
		if errors.Is(err, agents.ErrIntegrity) {
			c.Conditions = append(c.Conditions, "private_integrity")
			_, recoveryErr := c.Service.ReconcileServiceEvidence(ctx, nil)
			_ = c.printService(ctx, recoveryErr)
		}
		return c.failure("turn loop", err)
	}
	_, recoveryErr := c.Service.ReconcileServiceEvidence(ctx, &agents.ServiceHostReports{ProcessClosures: c.WorkspaceReport.ProcessClosures})
	if err := terminal.WriteStartup(c.Output, c.Settings, c.Bound); err != nil {
		return c.failure("turn loop", err)
	}
	if err := c.printService(ctx, recoveryErr); err != nil {
		return c.failure("turn loop", err)
	}
	c.stage = 6
	return nil
}
func (c *Composition) printService(ctx context.Context, recoveryErr error) error {
	if recoveryErr != nil {
		c.Conditions = append(c.Conditions, "service_recovery_pending")
	}
	condition := ""
	for _, v := range c.Conditions {
		if condition != "" {
			condition += "; "
		}
		condition += v
	}
	snapshot, err := c.Service.ServiceStatusSnapshot(ctx)
	if err != nil {
		return terminal.WriteServiceStatus(c.Output, nil, condition)
	}
	return terminal.WriteServiceStatus(c.Output, &snapshot, condition)
}
func (c *Composition) Summary() launcher.StartupSummary {
	summary := launcher.StartupSummary{IntegrityIssues: int64(len(c.Report.Issues)), PendingPaid: int64(len(c.Report.PendingPaid)), UnresumableTurns: int64(len(c.Report.PendingLoops)), Conditions: append([]string{}, c.Conditions...), Credentials: []launcher.CredentialVersion{}}
	for _, b := range c.Bound {
		summary.Credentials = append(summary.Credentials, launcher.CredentialVersion{ScopeID: b.ScopeID, Provider: b.Provider, Version: b.Version, OtherVersions: int64(len(b.Others))})
	}
	return summary
}
func (c *Composition) Close() error {
	var err error
	if c.Service != nil {
		err = errors.Join(err, c.Service.Close())
	}
	if c.Workspace != nil {
		if workspaceCloseOverride != nil {
			err = errors.Join(err, workspaceCloseOverride(c.Service, c.Workspace.Close))
		} else {
			err = errors.Join(err, c.Workspace.Close())
		}
	}
	if c.Store != nil {
		err = errors.Join(err, c.Store.Close())
	}
	return err
}
func ReadSettings(path string) (terminal.Settings, error) {
	f, err := os.Open(path)
	if err != nil {
		return terminal.Settings{}, ErrSettingsFile
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, terminal.MaxSettingsBytes+1))
	if errors.Join(readErr, f.Close()) != nil {
		return terminal.Settings{}, ErrSettingsFile
	}
	return terminal.DecodeSettings(raw)
}

// Describe never formats an originating error or a settings member path.
func Describe(err error) string {
	if blocked, _ := workspace.ClassifyExecutionFailure(err); blocked {
		return "process_unconfirmed"
	}
	var refusal *terminal.RefusalError
	if errors.As(err, &refusal) {
		if errors.Is(err, terminal.ErrSettings) {
			return "invalid settings"
		}
		return "invalid command"
	}
	var busy *agents.BusyError
	if errors.As(err, &busy) {
		return "busy"
	}
	for _, known := range publicErrors {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "operation failed"
}

// KnownClass validates the fixed class crossing the private startup channel.
func KnownClass(class string) bool {
	for _, known := range publicErrors {
		if class == known.Error() {
			return true
		}
	}
	switch class {
	case "process_unconfirmed", "invalid settings", "invalid command", "busy", "operation failed":
		return true
	}
	return false
}

var publicErrors = []error{
	ErrSettingsFile, terminal.ErrSettings, terminal.ErrCommand, terminal.ErrConfig,
	session.ErrReattachRequired, session.ErrFinished, session.ErrClosed,
	ledgerstore.ErrNotColony, ledgerstore.ErrLocked, ledgerstore.ErrInconsistent,
	agents.ErrDenied, agents.ErrUnavailable, agents.ErrNotBound, agents.ErrClosed, agents.ErrRevision, agents.ErrCommandMismatch,
	agents.ErrInvalid, agents.ErrTooLarge, agents.ErrIntegrity, agents.ErrPoisoned, agents.ErrInconsistent, agents.ErrMaterialization,
	agents.ErrRecoveryPending,
	agents.ErrAssignment, agents.ErrNoEffect, agents.ErrPaidClock, agents.ErrPaidSent, agents.ErrPaidPeriod,
	credentials.ErrUnavailable, credentials.ErrMissing, credentials.ErrInvalid, credentials.ErrReleased, credentials.ErrNative,
	providers.ErrInvalid, providers.ErrUnsupported, providers.ErrExpired, providers.ErrProtocol, providers.ErrLimit, providers.ErrUsage,
	workspace.ErrInvalid, workspace.ErrUnavailable, workspace.ErrBusy, workspace.ErrGitUnavailable, workspace.ErrNotGitRepository, workspace.ErrUnknownWorkspace, workspace.ErrNoScratch,
	context.Canceled, context.DeadlineExceeded,
}
