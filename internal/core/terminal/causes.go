package terminal

import (
	"fmt"
	"io"
)

// HostCause is fixed public text. Callers supply codes, never native errors,
// settings values or locators. The table is shared by executable and terminal.
type HostCause struct{ Cause, Next string }

var hostCauses = map[string]HostCause{
	"unauthorized":         {"this terminal uses another sign-in session or a restricted token", "use the colony owner's unrestricted sign-in session"},
	"incompatible":         {"the host was started by a different build", "shut it down with that build or end its process, then start again"},
	"wrong_colony":         {"the endpoint belongs to another colony", "select the intended colony and start again"},
	"busy":                 {"the host's connection or request capacity is occupied", "close an unused view or wait, then try explicitly"},
	"protocol_error":       {"the host connection sent an invalid protocol frame", "use matching builds and reconnect"},
	"no_host":              {"no host is running", "start with --host --no-terminal and -settings"},
	"non_host_holder":      {"the colony is held by a program that is not an answering host", "close that program before starting a host"},
	"host_timeout":         {"the host did not answer within the transport guard", "check the host and reconnect"},
	"host_stopping":        {"the previous host is finishing shutdown", "retry the start after shutdown finishes"},
	"host_connection_lost": {"host connection lost; this does not cancel work in the host", "reconnect to inspect its state"},
	"closure_unconfirmed":  {"the host did not confirm closing", "the next start reports what is held"},
	"stale_revision":       {"the host, context or turn changed since the command was prepared", "read status and send a new command"},
	"command_mismatch":     {"the command ID already names different work", "inspect the original request or use a new command ID"},
	"invalid_binding":      {"the requested session binding is not valid", "select a currently enabled context"},
	"invalid_command":      {"this operation or its arguments are not supported", "use an available command"},
	"not_ready":            {"the host is not ready for this operation", "watch its state before sending new work"},
	"in_progress":          {"the original command is still in progress", "inspect its status without submitting replacement work"},
	"recovery_pending":     {"Core has not confirmed recovery of the operation", "inspect the retained request and recovery conditions"},
	"held":                 {"the agent is held by its current eligibility", "inspect eligibility before sending new work"},
	"unavailable":          {"the requested host operation is unavailable", "inspect host status before retrying explicitly"},
	"too_large":            {"the request or result exceeds the protocol limit", "request a smaller page or send smaller input"},
	"closed":               {"the connection or operation is closed", "reconnect and inspect the original request"},
	"stale_page":           {"the state changed between pages", "start a fresh status query"},
	"startup_failed":       {"host startup did not complete", "correct the reported startup stage before starting again"},
	"no_startup_result":    {"no startup result was received", "check the executable and start again"},
	"endpoint_failed":      {"the owner-local endpoint could not be secured", "check owner access without broadening endpoint permissions"},
	"discovery_failed":     {"the host discovery could not be safely published", "check the colony's private run directory"},
	"spawn_failed":         {"the detached host could not be started", "check executable access and start again"},
}

func HostCauseFor(code string) HostCause {
	if cause, ok := hostCauses[code]; ok {
		return cause
	}
	return hostCauses["unavailable"]
}
func WriteHostCause(w io.Writer, code string) error {
	cause, ok := hostCauses[code]
	if !ok {
		code = "unavailable"
		cause = hostCauses[code]
	}
	_, err := fmt.Fprintf(w, "[terminal] %s: %s; next: %s\n", code, cause.Cause, cause.Next)
	return err
}
