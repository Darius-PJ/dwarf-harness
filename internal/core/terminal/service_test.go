package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func TestServiceTerminal(t *testing.T) {
	t.Parallel()
	t.Run("snapshot_and_command_views", func(t *testing.T) {
		t.Parallel()
		agent, cause, outage := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
		v := session.ServiceView{AgentID: agent, State: "lost", ThroughSequence: 42, Recovery: session.ServiceRecovery{RetainedSources: 1, LastErrorClass: "unavailable"}, Active: []session.ServiceCauseView{{CauseID: cause, OutageID: outage, Phase: "lost", Generation: 1, Cause: ledger.ServiceCauseSpec{Family: "provider", Code: "credential_denied", ScopeKind: "credential"}}}}
		row := session.ServiceOutageRow{AgentID: agent, CauseID: cause, OutageID: outage, BeginNoticeID: uuid.NewV7(), Active: true, Generation: 1}
		var output bytes.Buffer
		if err := WriteServiceStatus(&output, &session.ServiceSnapshot{Agents: []session.ServiceView{v}, Outages: []session.ServiceOutageRow{row}, ThroughSequence: 42, Recovery: v.Recovery}, "process_unconfirmed"); err != nil {
			t.Fatal(err)
		}
		requireDisplay(t, output.String(), agent.String(), cause.String(), outage.String(), "state=service_loss", "retained=1", "credential_denied", "through=42", "process_unconfirmed")
		output.Reset()
		r := runner{cfg: Config{Output: &output}}
		r.result(job{}, session.CommandResult{Data: v}, nil)
		r.result(job{}, session.CommandResult{Data: outageListing{page: session.ServiceOutagePage{Rows: []session.ServiceOutageRow{row}, ThroughSequence: 42}}}, nil)
		requireDisplay(t, output.String(), agent.String(), cause.String(), outage.String(), "state=service_loss", "retained=1", "active=true")
		output.Reset()
		if err := WriteServiceStatus(&output, nil, "private_integrity"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "state=available") || !strings.Contains(output.String(), "private_integrity") {
			t.Fatal("unavailable snapshot invented healthy status", output.String())
		}
	})
	t.Run("strict_passive_send", func(t *testing.T) {
		t.Parallel()
		cursor := &session.ServiceOutageCursor{OutageID: uuid.NewV7(), AgentID: uuid.NewV7(), ThroughSequence: 42, CallerID: uuid.NewV7(), FilterOutageID: uuid.NewV7()}
		for _, args := range []any{session.ServiceStatus{AgentID: uuid.NewV7()}, session.ServiceOutages{OutageID: cursor.FilterOutageID, After: cursor, ThroughSequence: 42, Limit: 1}} {
			op := "service.status"
			if _, ok := args.(session.ServiceOutages); ok {
				op = "service.outages"
			}
			command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: uuid.NewV7(), Operation: op, Arguments: args}
			raw, err := json.Marshal(command)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeCommand(raw)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil || !bytes.Equal(raw, encoded) {
				t.Fatalf("passive query lost its bound cursor or selection: %s %v", encoded, err)
			}
		}
		for _, row := range []struct{ op, args string }{{"service.clear", `{}`}, {"service.status", `{"state":"available"}`}, {"service.outages", `{"limit":129}`}, {"service.outages", `{"after":{"outage_id":"bad"}}`}} {
			raw := fmt.Sprintf(`{"schema":1,"command_id":%q,"context_id":%q,"operation":%q,"arguments":%s}`, uuid.NewV7(), uuid.NewV7(), row.op, row.args)
			if _, err := DecodeCommand([]byte(raw)); !errors.Is(err, ErrCommand) {
				t.Fatalf("accepted state-setting or malformed query: %s %v", raw, err)
			}
		}
	})
}
