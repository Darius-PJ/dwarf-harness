package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/internal/core/host"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

type invocation struct {
	role, colony, settings, startupHandle string
	settingsSupplied                      bool
	command                               []string
}

func chooseRole(args []string) (invocation, error) {
	flags := flag.NewFlagSet("dwarf-harness", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	colony := flags.String("colony-dir", "", "initialized colony directory")
	settings := flags.String("settings", "", "startup settings")
	start := flags.Bool("host", false, "start or attach without a view")
	noTerminal := flags.Bool("no-terminal", false, "return at ready")
	shutdown := flags.Bool("shutdown", false, "explicitly shut down the host")
	child := flags.Bool("host-child", false, "internal detached host")
	startupHandle := flags.String("startup-handle", "", "internal startup channel")
	if flags.Parse(args) != nil || *colony == "" {
		return invocation{}, errors.New("usage")
	}
	settingsSupplied := false
	flags.Visit(func(f *flag.Flag) { settingsSupplied = settingsSupplied || f.Name == "settings" })
	count := 0
	for _, enabled := range []bool{*start, *shutdown, *child} {
		if enabled {
			count++
		}
	}
	if count > 1 || *start != *noTerminal || (*child) != (*startupHandle != "") || (flags.NArg() != 0 && count != 0) {
		return invocation{}, errors.New("usage")
	}
	role := "combined"
	if *start {
		role = "host"
	}
	if *shutdown {
		role = "shutdown"
	}
	if *child {
		role = "child"
	}
	return invocation{role: role, colony: *colony, settings: *settings, startupHandle: *startupHandle, settingsSupplied: settingsSupplied, command: flags.Args()}, nil
}

type startupResult struct {
	Version    int            `json:"version"`
	Outcome    string         `json:"outcome"`
	Stage      string         `json:"stage,omitempty"`
	Code       string         `json:"code,omitempty"`
	Conditions []string       `json:"conditions,omitempty"`
	Refusal    *setup.Failure `json:"refusal,omitempty"`
}

// hostCandidateOverride is a test-only barrier after publication and before
// recovery. Helper processes use it to observe every real lifecycle state.
var hostCandidateOverride func(*host.Host)

func hostChild(inv invocation, interrupts <-chan os.Signal) int {
	channel, err := localipc.StartupChannel(inv.startupHandle)
	if err != nil {
		return 1
	}
	report := func(result startupResult) { _ = json.NewEncoder(channel).Encode(result); _ = channel.Close() }
	candidate, failure := host.NewCandidate(inv.colony)
	if failure != nil {
		outcome := "failed"
		if failure.Code == "locked" {
			outcome = "locked"
		}
		report(startupResult{Version: 1, Outcome: outcome, Stage: failure.Stage, Code: failure.Class, Conditions: failure.Conditions, Refusal: startupFailure(failure, setup.Target{Kind: "colony"}, failure.Stage)})
		return 1
	}
	if hostCandidateOverride != nil {
		hostCandidateOverride(candidate)
	}
	if failure := candidate.Start(context.Background(), startupConfiguration(inv)); failure != nil {
		report(startupResult{Version: 1, Outcome: "failed", Stage: failure.Stage, Code: failure.Class, Conditions: failure.Conditions, Refusal: startupFailure(failure, failure.Target, failure.Stage)})
		return 1
	}
	report(startupResult{Version: 1, Outcome: "started"})
	done := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			candidate.ShutdownSignal()
		case <-done:
		}
	}()
	err = candidate.Wait()
	close(done)
	if err != nil {
		return 1
	}
	return 0
}
func readStartup(reader io.Reader) startupResult {
	raw, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(raw) > 4096 || !bytes.HasSuffix(raw, []byte{'\n'}) {
		return startupResult{}
	}
	var result startupResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Version != 1 || !slices.Contains([]string{"started", "locked", "failed"}, result.Outcome) || len(result.Conditions) > 8 {
		return startupResult{}
	}
	for _, condition := range result.Conditions {
		if !slices.Contains([]string{"process_unconfirmed", "private_integrity", "service_recovery_pending"}, condition) {
			return startupResult{}
		}
	}
	if result.Outcome == "failed" || result.Outcome == "locked" {
		if !slices.Contains([]string{"settings", "colony", "core", "workspace", "turn recovery", "credential binding", "turn loop", "session", "host endpoint", "host discovery", "host spawn"}, result.Stage) || !host.KnownClass(result.Code) || result.Refusal == nil || result.Refusal.Code == "" || result.Refusal.Target.Kind == "" || result.Refusal.FixCommand == "" {
			return startupResult{}
		}
	} else if result.Stage != "" || result.Code != "" || result.Refusal != nil {
		return startupResult{}
	}
	return result
}
func printStartupFailure(output io.Writer, failure *host.Failure) {
	for _, condition := range failure.Conditions {
		fmt.Fprintf(output, "[terminal] service-condition: %s\n", condition)
	}
	refusal := failure.Refusal
	if refusal == nil {
		target := failure.Target
		if target.Kind == "" {
			target.Kind = "colony"
		}
		refusal = startupFailure(failure, target, failure.Stage)
	}
	fmt.Fprintf(output, "[terminal] startup refused: %s: %s\n", failure.Stage, describeSetupFailure(refusal))
	_ = terminal.WriteHostCause(output, failure.Code)
}
func stateObserver(output io.Writer) func(launcher.HostStatusResult) {
	var revision int64
	var code launcher.Code
	return func(status launcher.HostStatusResult) {
		if status.LifecycleRevision > revision || status.Code != "" && status.Code != code {
			fmt.Fprintf(output, "[terminal] host %s %s\n", status.HostID, status.State)
			revision = status.LifecycleRevision
			code = status.Code
		}
	}
}
func printSummary(output io.Writer, summary *launcher.StartupSummary) {
	if summary == nil {
		return
	}
	fmt.Fprintf(output, "[terminal] colony opened: %d integrity issues, %d pending paid attempts, %d unresumable turns\n", summary.IntegrityIssues, summary.PendingPaid, summary.UnresumableTurns)
	for _, credential := range summary.Credentials {
		fmt.Fprintf(output, "[terminal] credential scope=%s provider=%s version=%s other_versions=%d\n", credential.ScopeID, credential.Provider, credential.Version, credential.OtherVersions)
	}
	for _, condition := range summary.Conditions {
		fmt.Fprintf(output, "[terminal] service-condition: %s\n", condition)
	}
}
func connectionCode(err error) string {
	var failure *host.ConnectionFailure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return "host_connection_lost"
}
func bootstrapHost(inv invocation, output io.Writer) int {
	observe := stateObserver(output)
	ready := func(control *host.Control) error {
		defer control.Close()
		status, err := control.AwaitReady(observe)
		if status.State == "quiescing" || status.State == "stopped" {
			return &host.ConnectionFailure{Code: "host_stopping"}
		}
		if err == nil {
			printSummary(output, status.Startup)
		}
		return err
	}
	if control, err := host.Connect(inv.colony); err == nil {
		if err = ready(control); err == nil {
			return 0
		}
		_ = terminal.WriteHostCause(output, connectionCode(err))
		return 1
	} else if code := connectionCode(err); code != "no_host" && code != "host_connection_lost" && code != "host_timeout" {
		_ = terminal.WriteHostCause(output, code)
		return 1
	}
	childArgs := []string{"--host-child", "--colony-dir", inv.colony}
	if inv.settingsSupplied {
		childArgs = append(childArgs, "--settings", inv.settings)
	}
	retried := false
start:
	reader, jobBound, err := localipc.Spawn(childArgs)
	if err != nil {
		printStartupFailure(output, &host.Failure{Stage: "host spawn", Class: "operation failed", Code: "spawn_failed"})
		return 1
	}
	defer reader.Close()
	if jobBound {
		fmt.Fprintln(output, "[terminal] host job-bound")
	}
	results := make(chan startupResult, 1)
	go func() { results <- readStartup(reader) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var result startupResult
	var deadline time.Time
	for {
		select {
		case result = <-results:
			results = nil
			if result.Version == 0 {
				printStartupFailure(output, &host.Failure{Stage: "host spawn", Class: "no startup result", Code: "no_startup_result"})
				return 1
			}
			if result.Outcome == "failed" {
				code := "startup_failed"
				if result.Stage == "host endpoint" {
					code = "endpoint_failed"
				}
				if result.Stage == "host discovery" {
					code = "discovery_failed"
				}
				printStartupFailure(output, &host.Failure{Stage: result.Stage, Class: result.Code, Code: code, Conditions: result.Conditions, Refusal: result.Refusal})
				return 1
			}
			deadline = time.Now().Add(localipc.Guard())
		case <-ticker.C:
		}
		if control, err := host.Connect(inv.colony); err == nil {
			if err := ready(control); err == nil {
				return 0
			} else if connectionCode(err) == "host_stopping" {
				_ = terminal.WriteHostCause(output, "host_stopping")
				return 1
			}
			// A startup refusal closes the lifecycle endpoint; its bounded result is
			// authoritative for the public stage/class, not the broken connection.
			if results != nil {
				result = <-results
				results = nil
			}
			if result.Version == 0 {
				printStartupFailure(output, &host.Failure{Stage: "host spawn", Class: "no startup result", Code: "no_startup_result"})
			} else if result.Outcome == "failed" {
				printStartupFailure(output, &host.Failure{Stage: result.Stage, Class: result.Code, Code: "startup_failed", Conditions: result.Conditions, Refusal: result.Refusal})
			} else {
				_ = terminal.WriteHostCause(output, "host_connection_lost")
			}
			return 1
		} else if code := connectionCode(err); code != "no_host" && code != "host_connection_lost" && code != "host_timeout" {
			_ = terminal.WriteHostCause(output, code)
			return 1
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			code := "host_timeout"
			if result.Outcome == "locked" {
				// An effect-free probe may have held the existing lock for an instant.
				// Retry Claim-first once if no holder remains; otherwise attach to the
				// winner or report the still-held non-host lock.
				if !retried {
					probe, err := host.Probe(inv.colony)
					if err == nil && probe.Kind == "no_host" {
						retried = true
						_ = reader.Close()
						ticker.Stop()
						goto start
					}
				}
				code = "non_host_holder"
			}
			_ = terminal.WriteHostCause(output, code)
			return 1
		}
	}
}
