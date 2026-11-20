package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// The full TLS execution smoke uses the existing agents-package private seam.
// This complementary row exercises the actual run() startup and error callback
// without a provider: a held request must stop before resolving its route.
func TestEligibilityStartupTerminal(t *testing.T) {
	// integratedStartupSettings uses t.Setenv, so this fixture is sequential.
	ctx := context.Background()
	root, oldTurn, origin := restartPrefix(t, 99)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	settings := integratedStartupSettings(t, root, origin)
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, _, err := agents.Open(ctx, store, root, agents.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: ledger.ToEnd, AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var authority uint64
	var capacity ledger.Capacity
	var taskBinding *ledger.TaskBinding
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range commit.Records {
			if record.Type == ledger.TypeRuntimeWorkAdmitted {
				work, err := ledger.DecodeRuntimeWorkAdmitted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if work.WorkID == oldTurn {
					taskBinding = work.TaskBinding
				}
			}
			if record.Type != ledger.TypeRuntimeGrantIssued {
				continue
			}
			grant, err := ledger.DecodeRuntimeGrantIssued(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			authority = max(authority, uint64(grant.AuthorityVersion))
			if grant.Grantee.ContextID == origin.ContextID {
				capacity = grant.Limits
			}
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	binding := agents.Binding{AgentID: origin.AgentID, ContextID: origin.ContextID}
	_, err = service.IssueGrant(ctx, binding, agents.GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: authority, Spec: agents.GrantSpec{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"context.inspect", "turn.inspect"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []agents.Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	first := agents.FirstActionTurn{ContextID: origin.ContextID, TurnID: uuid.NewV7(), StartCommandID: uuid.NewV7(), AdmissionCommandID: uuid.NewV7(), TaskBinding: taskBinding}
	first.Input = session.MessageSend{TurnID: first.TurnID, Text: "Declared startup action."}
	request := agents.EligibilityRequireFirst{CommandID: uuid.NewV7(), AgentID: origin.AgentID, Reason: "owner_review", ActionID: uuid.NewV7(), FirstActionTurn: first}
	if _, err := service.RequireFirstAction(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var input bytes.Buffer
	ordinary := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: origin.ContextID, Operation: "message.send", Arguments: session.MessageSend{TurnID: uuid.NewV7(), Text: "Ordinary startup input.", Artifacts: []ledger.ArtifactRef{}}}
	status := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: origin.ContextID, Operation: "context.status", Arguments: session.ContextStatus{}}
	for _, command := range []session.Command{ordinary, status} {
		raw, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&input, "/send %s\n", raw)
	}
	fmt.Fprintf(&input, "/status %s\n/quit\n", oldTurn)
	name := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(name, input.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var output bytes.Buffer
	code := run([]string{"-colony-dir", root, "-settings", settings}, stdin, &output, nil)
	transcript := output.String()
	t.Logf("EXECUTABLE TRANSCRIPT\n%s", transcript)
	if code != 0 {
		t.Fatalf("startup terminal exit=%d", code)
	}
	for _, part := range []string{"refused: message.send eligibility held", "state=first_action_required", "reason=owner_review", "context revision=", "status state=cancelled", request.ActionID.String(), first.TurnID.String(), first.StartCommandID.String(), first.AdmissionCommandID.String()} {
		if !strings.Contains(transcript, part) {
			t.Fatalf("actual executable omitted %q", part)
		}
	}
	if strings.Contains(transcript, "unclassified failure") || strings.Contains(transcript, "command message.send applied") {
		t.Fatal("actual executable lost the held class or admitted ordinary work")
	}
}
