// Command dwarf-harness hosts a colony's Core and plain terminal. Positional
// setup commands review and apply explicit owner operations without turns.
// Managed colonies load their settings from the ledger; legacy colonies use
// the owner's explicit --settings file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"uuid"

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

func main() {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, interrupts))
}

// workspaceCloseOverride substitutes the workspace drain only in closure tests.
// Production leaves it nil.
var workspaceCloseOverride func(*agents.Service, func() error) error

// workspaceOpenOverride substitutes only workspace.Open's result in startup
// refusal tests. Production leaves it nil.
var workspaceOpenOverride func(context.Context, *ledgerstore.Store, string, workspace.Config) (*workspace.Service, workspace.Reconciliation, error)

// run returns 0 after a drained exit, 1 after a refusal and 2 for usage.
// Every refusal prints a fixed error class, never a path, value or locator.
func run(args []string, stdin *os.File, stdout io.Writer, interrupts <-chan os.Signal) int {
	flags := flag.NewFlagSet("dwarf-harness", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	colonyDir := flags.String("colony-dir", "", "colony directory")
	settingsPath := flags.String("settings", "", "legacy strict settings JSON file (nonsecret)")
	if err := flags.Parse(args); err != nil || *colonyDir == "" {
		return writeSetupFailure(stdout, false, commandFailure("review_invalid", "command", "--colony-dir", setup.Target{Kind: "colony"}), 2)
	}
	settingsSupplied := false
	flags.Visit(func(f *flag.Flag) { settingsSupplied = settingsSupplied || f.Name == "settings" })
	if flags.NArg() != 0 {
		return runSetup(*colonyDir, settingsSupplied, flags.Args(), stdout)
	}
	target := setup.Target{Kind: "colony"}
	refuse := func(stage string, err error) int {
		fmt.Fprintf(stdout, "[terminal] startup refused: %s: %s\n", stage, describe(startupFailure(err, target, stage)))
		return 1
	}
	ctx := context.Background()
	store, _, err := ledgerstore.Open(*colonyDir, ledger.Defined())
	if err != nil {
		return refuse("colony", err)
	}
	target.ID = store.Colony()
	settings, managed, err := loadStartupConfiguration(ctx, store, *settingsPath, settingsSupplied)
	if err != nil {
		return refuse("settings", errors.Join(err, store.Close()))
	}
	if managed {
		fmt.Fprintln(stdout, "[terminal] validation disabled: this build has no validation startup settings")
	}
	// Drain order: Core stops its turns before the workspace, then the store.
	drain := []func() error{store.Close}
	stop := func() error {
		var stopped error
		for _, step := range drain {
			stopped = errors.Join(stopped, step())
		}
		if stopped != nil {
			fmt.Fprintln(stdout, "[terminal] shutdown failed: closure unconfirmed")
		}
		return stopped
	}
	service, report, err := agents.OpenPaid(ctx, store, *colonyDir, agents.DefaultLimits(), agents.PaidOptions{Limits: agents.DefaultPaidLimits()})
	if err != nil {
		stop()
		return refuse("core", err)
	}
	drain = []func() error{service.Close, store.Close}
	printService := func(condition string, recoveryErr error) error {
		if recoveryErr != nil {
			if condition != "" {
				condition += "; "
			}
			condition += "service_recovery_pending"
		}
		snapshot, err := service.ServiceStatusSnapshot(ctx)
		if err != nil {
			return terminal.WriteServiceStatus(stdout, nil, condition)
		}
		return terminal.WriteServiceStatus(stdout, &snapshot, condition)
	}
	profile := workspace.ProvisionalCapacityProfile()
	openWorkspace := func() (*workspace.Service, workspace.Reconciliation, error) {
		cfg := workspace.Config{Capacity: &profile}
		if override := workspaceOpenOverride; override != nil {
			return override(ctx, store, *colonyDir, cfg)
		}
		return workspace.Open(ctx, store, *colonyDir, cfg)
	}
	ws, workspaceReport, err := openWorkspace()
	if err != nil {
		if blocked, _ := workspace.ClassifyExecutionFailure(err); blocked {
			_, recoveryErr := service.ReconcileServiceEvidence(ctx, nil)
			_ = printService("process_unconfirmed", recoveryErr)
		}
		stop()
		return refuse("workspace", err)
	}
	closeWorkspace := ws.Close
	if override := workspaceCloseOverride; override != nil {
		closeWorkspace = func() error { return override(service, ws.Close) }
	}
	drain = []func() error{service.Close, closeWorkspace, store.Close}
	fmt.Fprintf(stdout, "[terminal] colony opened: %d integrity issues, %d pending paid attempts, %d unresumable turns\n", len(report.Issues), len(report.PendingPaid), len(report.PendingLoops))
	for _, pending := range report.PendingLoops {
		if pending.ToolID != (uuid.UUID{}) {
			continue
		}
		_, err := service.ReconcileLoop(ctx, agents.LoopReconciliation{CommandID: uuid.NewV7(), TurnID: pending.TurnID, ExpectedOwnerVersion: pending.OwnerVersion})
		if errors.Is(err, agents.ErrRecoveryPending) {
			fmt.Fprintf(stdout, "[terminal] turn %s recovery pending: %s\n", pending.TurnID, describe(err))
			continue
		}
		if err != nil {
			stop()
			return refuse("turn recovery", err)
		}
	}
	if managed {
		settings, err = managedCredentialBindings(ctx, service, settings)
		if err != nil {
			stop()
			return refuse("credential binding", err)
		}
	}
	bound, err := bindCredentials(ctx, service, settings)
	if err != nil {
		stop()
		return refuse("credential binding", err)
	}
	options := agents.LoopOptions{}
	views := []terminal.Agent{}
	index := map[uuid.UUID]int{}
	for _, c := range settings.Contexts {
		setting := setup.LoopSetting(agents.Binding{AgentID: c.AgentID, ContextID: c.ContextID}, c.Configuration())
		if err := agents.ValidateSetupTemplate(setting); err != nil {
			stop()
			failure := commandFailure("review_invalid", "turn_policy", "context configure --input --review-out", setup.Target{Kind: "context", ID: c.ContextID, AgentID: c.AgentID})
			return refuse("turn loop", failure)
		}
		options.Contexts = append(options.Contexts, setting)
		i, ok := index[c.AgentID]
		if !ok {
			i, index[c.AgentID] = len(views), len(views)
			views = append(views, terminal.Agent{AgentID: c.AgentID})
		}
		views[i].Contexts = append(views[i].Contexts, terminal.Context{ContextID: c.ContextID, Title: c.DisplayTitle})
	}
	if err := service.EnableTurnLoop(ctx, ws, options); err != nil {
		if errors.Is(err, agents.ErrIntegrity) {
			_, recoveryErr := service.ReconcileServiceEvidence(ctx, nil)
			_ = printService("private_integrity", recoveryErr)
		}
		stop()
		return refuse("turn loop", err)
	}
	_, serviceRecoveryErr := service.ReconcileServiceEvidence(ctx, &agents.ServiceHostReports{ProcessClosures: workspaceReport.ProcessClosures})
	for i := range views {
		view, err := service.OpenSession(ctx, agents.Binding{AgentID: views[i].AgentID, ContextID: views[i].Contexts[0].ContextID})
		if err != nil {
			for _, opened := range views[:i] {
				opened.Session.Close()
			}
			stop()
			return refuse("session", err)
		}
		views[i].Session = view
	}
	if err := terminal.WriteStartup(stdout, settings, bound); err != nil {
		for _, v := range views {
			v.Session.Close()
		}
		stop()
		return 1
	}
	if err := printService("", serviceRecoveryErr); err != nil {
		for _, v := range views {
			v.Session.Close()
		}
		stop()
		return 1
	}
	info, err := stdin.Stat()
	interactive := err == nil && info.Mode()&os.ModeCharDevice != 0
	err = terminal.Run(ctx, terminal.Config{Agents: views, Input: stdin, Output: stdout, Interactive: interactive, Interrupts: interrupts, Describe: describe, Drain: []func() error{stop}})
	if err != nil {
		return 1
	}
	return 0
}

func readSettings(path string) (terminal.Settings, error) {
	file, err := os.Open(path)
	if err != nil {
		return terminal.Settings{}, errSettingsFile
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, terminal.MaxSettingsBytes+1))
	if err != nil {
		return terminal.Settings{}, errSettingsFile
	}
	return terminal.DecodeSettings(raw)
}

var errSettingsFile = errors.New("settings file unreadable")

// bindCredentials performs each explicit owner binding once with fresh IDs at
// the scope's current version. A concurrent change refuses with ErrRevision;
// there is no retry.
func bindCredentials(ctx context.Context, service *agents.Service, s terminal.Settings) ([]terminal.ScopeBinding, error) {
	var bound []terminal.ScopeBinding
	for _, b := range s.CredentialBindings {
		var origin agents.Binding
		for _, c := range s.Contexts {
			if c.CredentialScopeID == b.ScopeID {
				origin = agents.Binding{AgentID: c.AgentID, ContextID: c.ContextID}
				break
			}
		}
		scope, err := service.CredentialScopeStatus(ctx, agents.CredentialScopeLookup{ScopeID: b.ScopeID})
		if err != nil {
			return nil, err
		}
		version := uuid.NewV7()
		_, err = service.BindCredential(ctx, origin, agents.CredentialBinding{CommandID: uuid.NewV7(), ScopeID: b.ScopeID, ExpectedVersion: scope.Version, Provider: b.Provider, CredentialVersion: version, SourceKind: b.SourceKind, SourceLocator: b.SourceLocator, RevokePreviousVersion: b.RevokePreviousVersion})
		if err != nil {
			return nil, startupFailure(err, setup.Target{Kind: "credential", ID: b.ScopeID}, "credential_bindings")
		}
		after, err := service.CredentialScopeStatus(ctx, agents.CredentialScopeLookup{ScopeID: b.ScopeID})
		if err != nil {
			return nil, err
		}
		result := terminal.ScopeBinding{ScopeID: b.ScopeID, Provider: b.Provider, Version: version}
		for _, id := range after.ActiveVersions {
			if id != version {
				result.Others = append(result.Others, id)
			}
		}
		bound = append(bound, result)
	}
	return bound, nil
}

// describe prints only a known error class's fixed text. Wrapped errors can
// carry paths or digests, so their own text is never printed.
func describe(err error) string {
	var setupFailure *setup.Failure
	if errors.As(err, &setupFailure) {
		return describeSetupFailure(setupFailure)
	}
	var cause *agents.RefusalCause
	if errors.As(err, &cause) {
		return describeSetupFailure(startupFailure(err, setup.Target{}, "dispatch"))
	}
	var busy *agents.BusyError
	if errors.As(err, &busy) {
		return "busy: " + busy.Reason
	}
	var refusal *terminal.RefusalError
	if errors.As(err, &refusal) {
		return refusal.Error()
	}
	for _, known := range []error{
		errSettingsFile, terminal.ErrSettings, terminal.ErrCommand, terminal.ErrConfig,
		session.ErrReattachRequired, session.ErrFinished, session.ErrClosed,
		ledgerstore.ErrNotColony, ledgerstore.ErrLocked, ledgerstore.ErrInconsistent,
		agents.ErrDenied, agents.ErrUnavailable, agents.ErrNotBound, agents.ErrClosed, agents.ErrRevision, agents.ErrCommandMismatch,
		agents.ErrInvalid, agents.ErrTooLarge, agents.ErrIntegrity, agents.ErrPoisoned, agents.ErrInconsistent, agents.ErrMaterialization,
		agents.ErrAssignment, agents.ErrNoEffect, agents.ErrPaidClock, agents.ErrPaidSent, agents.ErrPaidPeriod,
		credentials.ErrUnavailable, credentials.ErrMissing, credentials.ErrInvalid, credentials.ErrReleased, credentials.ErrNative,
		providers.ErrInvalid, providers.ErrUnsupported, providers.ErrExpired, providers.ErrProtocol, providers.ErrLimit, providers.ErrUsage,
		workspace.ErrInvalid, workspace.ErrUnavailable, workspace.ErrBusy, workspace.ErrGitUnavailable, workspace.ErrNotGitRepository, workspace.ErrUnknownWorkspace, workspace.ErrNoScratch,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "unclassified failure"
}
