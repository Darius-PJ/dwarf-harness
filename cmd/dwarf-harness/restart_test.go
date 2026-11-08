package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
)

// An unavailable credential must not prevent restart bookkeeping. Exercise the
// actual CLI path, then inspect its durable result through a fresh Core Open.
func TestRuntimeRestartStartup(t *testing.T) {
	for _, row := range []struct {
		name    string
		through uint64
		outcome string
	}{
		{"legacy_bind", 78, "interrupted"},
		{"saved_wait", 87, "interrupted"},
		{"native_ended", 99, "cancelled"},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, turn, origin := restartPrefix(t, row.through)
			scope := uuid.NewV7() // deliberately absent: startup must reach this refusal only after recovery
			settings := fmt.Sprintf(`{"schema":1,"contexts":[{"agent_id":%q,"context_id":%q,"route_id":%q,"expected_route_version":1,"credential_scope_id":%q,"max_output_tokens":32,"display_title":"General work","turn_policy":{}}],"credential_bindings":[{"scope_id":%q,"provider":"anthropic","source_kind":"environment","source_locator":"DWARF_RESTART_UNUSED"}]}`, origin.AgentID, origin.ContextID, uuid.NewV7(), scope, scope)
			settingsPath := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			input, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			var acknowledged uint64
			for start := range 2 {
				var output bytes.Buffer
				code := run([]string{"-colony-dir", root, "-settings", settingsPath}, input, &output, nil)
				if code != 1 || !strings.Contains(output.String(), "startup refused: credential binding:") {
					t.Fatalf("startup did not reach the deliberate credential refusal after recovery: exit=%d output=%s", code, output.String())
				}
				store, _, err := ledgerstore.Open(root, ledger.Defined())
				if err != nil {
					t.Fatal(err)
				}
				service, report, err := agents.Open(context.Background(), store, root, agents.DefaultLimits())
				if err != nil {
					store.Close()
					t.Fatal(err)
				}
				if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
					t.Errorf("startup left an unended owner or damaged replay: %+v", report)
				}
				reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: ledger.ToEnd, AllTypes: true})
				if err != nil {
					service.Close()
					store.Close()
					t.Fatal(err)
				}
				native, ended, closed := 0, 0, 0
				for {
					commit, err := reader.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, record := range commit.Records {
						if record.TurnID != turn {
							continue
						}
						switch record.Type {
						case ledger.TypeRuntimeScopedReceipt:
							v, err := ledger.DecodeRuntimeScopedReceipt(record.Payload)
							if err != nil {
								t.Fatal(err)
							}
							if v.Submission.Operation == "turn.end" || v.Submission.Operation == "turn.cancel" {
								native++
							}
						case ledger.TypeRuntimeTurnResult:
							v, err := ledger.DecodeRuntimeTurnResult(record.Payload)
							if err != nil {
								t.Fatal(err)
							}
							ended++
							if v.Outcome != row.outcome {
								t.Errorf("startup replaced the committed recovery outcome: %+v", v)
							}
						case ledger.TypeRuntimeContinuationChanged:
							v, err := ledger.DecodeRuntimeContinuationChanged(record.Payload)
							if err != nil {
								t.Fatal(err)
							}
							if v.State == "terminal" {
								closed++
							}
						}
					}
				}
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
				through := store.Durable()
				if err := service.Close(); err != nil {
					t.Error(err)
				}
				if err := store.Close(); err != nil {
					t.Error(err)
				}
				if native != 1 || ended != 1 || closed != 1 || start == 1 && through != acknowledged {
					t.Fatalf("startup repeated or omitted recovery: native=%d loop=%d close=%d through=%d prior=%d", native, ended, closed, through, acknowledged)
				}
				acknowledged = through
			}
		})
	}
}

// Build an isolated prefix of the frozen ledger with its exact immutable
// objects, following the agents golden-fixture hydration convention.
func restartPrefix(t *testing.T, through uint64) (string, uuid.UUID, ledger.Origin) {
	t.Helper()
	source := filepath.Join("..", "..", "contract", "ledger", "testdata", "f3", "16")
	colony := uuid.MustParse("019a1600-0000-7000-8000-000000000100")
	root := filepath.Join(t.TempDir(), "colony")
	if err := os.CopyFS(root, os.DirFS(filepath.Join(source, "objects"))); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "bodies"), os.DirFS(filepath.Join(source, "bodies"))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "colony.json"), []byte(fmt.Sprintf("{\"format\":1,\"colony_id\":\"%s\"}\n", colony)), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "log", ledger.SegmentName(1)))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(raw, []byte{'\n'})
	if err := os.WriteFile(filepath.Join(root, "log", ledger.SegmentName(1)), bytes.Join(lines[:through], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := ledger.OpenReader(filepath.Join(root, "log"), ledger.ReadOptions{Colony: colony, Through: ledger.ToEnd, AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	directories := map[uuid.UUID]string{}
	var turn uuid.UUID
	var origin ledger.Origin
	var commits []ledger.Commit
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, commit)
		for _, record := range commit.Records {
			if record.Type == ledger.TypeRuntimeAgentRegistered {
				v, err := ledger.DecodeRuntimeAgentRegistered(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				directories[record.AgentID] = v.Directory
			}
			if record.Type == ledger.TypeRuntimeTurnBound {
				turn = record.TurnID
				origin = ledger.Origin{AgentID: record.AgentID, SessionID: record.ExecutionSessionID, ContextID: record.ContextID}
			}
			var revision ledger.PrivateRevision
			switch record.Type {
			case ledger.TypePrivateRevision:
				revision, err = ledger.DecodePrivateRevision(record.Payload)
			case ledger.TypePrivateScopedRevision:
				var v ledger.PrivateScopedRevision
				v, err = ledger.DecodePrivateScopedRevision(record.Payload)
				revision = ledger.PrivateRevision(v)
			default:
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "agents", directories[record.AgentID])
			if revision.Scope == "context" {
				destination = filepath.Join(destination, "contexts", record.ContextID.String())
			}
			destination = filepath.Join(destination, "core", "objects")
			if err := os.MkdirAll(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, change := range revision.Changes {
				if change.Mode == "clear" {
					continue
				}
				name := change.SHA256.String()
				data, err := os.ReadFile(filepath.Join(source, "objects", name))
				if err != nil {
					t.Fatal(err)
				}
				if ledger.Digest(sha256.Sum256(data)) != change.SHA256 || int64(len(data)) != change.Bytes {
					t.Fatal("invalid frozen private object")
				}
				if err := os.WriteFile(filepath.Join(destination, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	rebindStartupPolicy(t, root, directories[origin.AgentID], origin, turn, commits)
	return root, turn, origin
}

// The golden deliberately treats policy/render as opaque. Startup needs the
// actual runtime formats, so only this isolated copy's bind is re-encoded.
func rebindStartupPolicy(t *testing.T, root, directory string, origin ledger.Origin, turn uuid.UUID, commits []ledger.Commit) {
	t.Helper()
	deadline := int64(1) // recovery is clock-free, including an expired legacy turn
	for _, c := range commits {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeContinuationChanged {
				v, err := ledger.DecodeRuntimeContinuationChanged(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				deadline = v.DeadlineUTCNS
			}
		}
	}
	publish := func(value any) ledger.ObjectRef {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		ref := ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256(raw)), Bytes: int64(len(raw))}
		path := filepath.Join(root, "agents", directory, "contexts", origin.ContextID.String(), "core", "objects", ref.SHA256.String())
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	policy := publish(agents.TurnPolicy{Schema: 1, DeadlineUTCNS: deadline, MaxModelCalls: 4, MaxToolCalls: 8, MaxArgumentBytes: 64 << 10, MaxResultBytes: 64 << 10, MaxDiscoveryEntries: 8, MaxSearchEntries: 4096, MaxSearchMatches: 100, MaxLiveBytes: 64 << 10})
	render := publish(map[string]any{"schema": 1, "origin": origin, "turn_id": turn, "provider": "anthropic", "model": "startup-recovery"})
	log := filepath.Join(root, "log", ledger.SegmentName(1))
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, bytes.Join(bytes.SplitAfter(raw, []byte{'\n'})[:75], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, c := range commits {
		if c.First < 76 {
			continue
		}
		drafts := make([]ledger.Draft, len(c.Records))
		for i, record := range c.Records {
			drafts[i] = record.Draft
		}
		if c.First == 76 {
			bound, err := ledger.DecodeRuntimeTurnBound(drafts[1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			bound.PolicyObject, bound.RenderObject = policy, render
			receipt, err := ledger.DecodeRuntimeLoopReceipt(drafts[2].Payload)
			if err != nil {
				t.Fatal(err)
			}
			args := receipt.Submission.Arguments.(ledger.LoopBindArguments)
			args.PolicyObject, args.RenderObject = policy, render
			receipt.Submission.Arguments = args
			submission, err := receipt.Submission.Encode()
			if err != nil {
				t.Fatal(err)
			}
			receipt.SubmissionSHA256 = ledger.Digest(sha256.Sum256(submission))
			command := ledger.Command{CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256}
			drafts[0].Payload, err = command.Encode()
			if err != nil {
				t.Fatal(err)
			}
			drafts[1].Payload, err = bound.Encode()
			if err != nil {
				t.Fatal(err)
			}
			drafts[2].Payload, err = receipt.Encode()
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
	}
}
