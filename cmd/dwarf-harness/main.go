// Command dwarf-harness hosts a colony's Core and plain terminal, or a detached
// host. Positional setup commands review and apply explicit owner operations.
// Managed colonies load configuration from the ledger; legacy colonies use the
// owner's explicit --settings file. The default remains the combined program.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/host"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

func main() {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, interrupts))
}

func run(args []string, stdin *os.File, stdout io.Writer, interrupts <-chan os.Signal) int {
	invocation, err := chooseRole(args)
	if err != nil {
		return writeSetupFailure(stdout, false, commandFailure("review_invalid", "command", "--colony-dir", setup.Target{Kind: "colony"}), 2)
	}
	if len(invocation.command) != 0 {
		return runSetup(invocation.colony, invocation.settingsSupplied, invocation.command, stdout)
	}
	switch invocation.role {
	case "host":
		return bootstrapHost(invocation, stdout)
	case "shutdown":
		return host.Shutdown(invocation.colony, stdout)
	case "child":
		return hostChild(invocation, interrupts)
	}
	ctx := context.Background()
	core := host.NewComposition(invocation.colony, startupConfiguration(invocation), stdout)
	stop := func() error {
		err := core.Close()
		if err != nil {
			_ = terminal.WriteHostCause(stdout, "closure_unconfirmed")
		}
		return err
	}
	if failure := core.Compose(ctx, nil); failure != nil {
		_ = stop()
		printStartupFailure(stdout, failure)
		return 1
	}
	views := []terminal.Agent{}
	index := map[uuid.UUID]int{}
	for _, setting := range core.Settings.Contexts {
		i, ok := index[setting.AgentID]
		if !ok {
			i = len(views)
			index[setting.AgentID] = i
			views = append(views, terminal.Agent{AgentID: setting.AgentID})
		}
		views[i].Contexts = append(views[i].Contexts, terminal.Context{ContextID: setting.ContextID, Title: setting.DisplayTitle})
	}
	for i := range views {
		view, err := core.Service.OpenSession(ctx, agents.Binding{AgentID: views[i].AgentID, ContextID: views[i].Contexts[0].ContextID})
		if err != nil {
			for _, opened := range views[:i] {
				opened.Session.Close()
			}
			_ = stop()
			printStartupFailure(stdout, &host.Failure{Stage: "session", Class: host.Describe(err), Code: "startup_failed"})
			return 1
		}
		views[i].Session = view
	}
	info, err := stdin.Stat()
	interactive := err == nil && info.Mode()&os.ModeCharDevice != 0
	if terminal.Run(ctx, terminal.Config{Agents: views, Input: stdin, Output: stdout, Interactive: interactive, Interrupts: interrupts, Describe: describe, Drain: []func() error{stop}}) != nil {
		return 1
	}
	return 0
}
