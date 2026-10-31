// Command dwarf-harness hosts one initialized colony's Core and serves the
// plain terminal on it. It never initializes a colony, installs a provider,
// grant, spend term or capacity, or reads a credential source other than the
// owner's explicit startup bindings in the settings file.
//
//	dwarf-harness -colony-dir <colony> -settings <settings.json>
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
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func main() {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, interrupts))
}

// run returns 0 after a drained exit, 1 after a refusal and 2 for usage.
// Every refusal prints a fixed error class, never a path, value or locator.
func run(args []string, stdin *os.File, stdout io.Writer, interrupts <-chan os.Signal) int {
	flags := flag.NewFlagSet("dwarf-harness", flag.ContinueOnError)
	flags.SetOutput(stdout)
	colonyDir := flags.String("colony-dir", "", "initialized colony directory")
	settingsPath := flags.String("settings", "", "strict settings JSON file (nonsecret)")
	if err := flags.Parse(args); err != nil || *colonyDir == "" || *settingsPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stdout, "usage: dwarf-harness -colony-dir <colony> -settings <settings.json>")
		return 2
	}
	refuse := func(stage string, err error) int {
		fmt.Fprintf(stdout, "[terminal] startup refused: %s: %s\n", stage, describe(err))
		return 1
	}
	settings, err := readSettings(*settingsPath)
	if err != nil {
		return refuse("settings", err)
	}
	ctx := context.Background()
	store, _, err := ledgerstore.Open(*colonyDir, ledger.Defined())
	if err != nil {
		return refuse("colony", err)
	}
	// Drain order: Core stops its turns before the workspace, then the store.
	drain := []func() error{store.Close}
	stop := func() {
		for _, step := range drain {
			_ = step()
		}
	}
	service, report, err := agents.OpenPaid(ctx, store, *colonyDir, agents.DefaultLimits(), agents.PaidOptions{Limits: agents.DefaultPaidLimits()})
	if err != nil {
		stop()
		return refuse("core", err)
	}
	drain = []func() error{service.Close, store.Close}
	profile := workspace.ProvisionalCapacityProfile()
	ws, _, err := workspace.Open(ctx, store, *colonyDir, workspace.Config{Capacity: &profile})
	if err != nil {
		stop()
		return refuse("workspace", err)
	}
	drain = []func() error{service.Close, ws.Close, store.Close}
	fmt.Fprintf(stdout, "[terminal] colony opened: %d integrity issues, %d pending paid attempts, %d unresumable turns\n", len(report.Issues), len(report.PendingPaid), len(report.PendingLoops))
	bound, err := bindCredentials(ctx, service, settings)
	if err != nil {
		stop()
		return refuse("credential binding", err)
	}
	options := agents.LoopOptions{}
	views := []terminal.Agent{}
	index := map[uuid.UUID]int{}
	for _, c := range settings.Contexts {
		p := c.Policy
		options.Contexts = append(options.Contexts, agents.LoopContextSettings{Binding: agents.Binding{AgentID: c.AgentID, ContextID: c.ContextID}, RouteID: c.RouteID, ExpectedRouteVersion: c.ExpectedRouteVersion, CredentialScopeID: c.CredentialScopeID, MaxOutputTokens: c.MaxOutputTokens, DisplayTitle: c.DisplayTitle, TaskBinding: c.TaskBinding, TimeoutMS: p.TimeoutMS, Policy: agents.TurnPolicy{Schema: 1, MaxModelCalls: p.MaxModelCalls, MaxToolCalls: p.MaxToolCalls, MaxArgumentBytes: p.MaxArgumentBytes, MaxResultBytes: p.MaxResultBytes, MaxDiscoveryEntries: p.MaxDiscoveryEntries, MaxSearchEntries: p.MaxSearchEntries, MaxSearchMatches: p.MaxSearchMatches, MaxLiveBytes: p.MaxLiveBytes}})
		i, ok := index[c.AgentID]
		if !ok {
			i, index[c.AgentID] = len(views), len(views)
			views = append(views, terminal.Agent{AgentID: c.AgentID})
		}
		views[i].Contexts = append(views[i].Contexts, terminal.Context{ContextID: c.ContextID, Title: c.DisplayTitle})
	}
	if err := service.EnableTurnLoop(ctx, ws, options); err != nil {
		stop()
		return refuse("turn loop", err)
	}
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
	info, err := stdin.Stat()
	interactive := err == nil && info.Mode()&os.ModeCharDevice != 0
	err = terminal.Run(ctx, terminal.Config{Agents: views, Input: stdin, Output: stdout, Interactive: interactive, Interrupts: interrupts, Describe: describe, Drain: drain})
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
			return nil, err
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
