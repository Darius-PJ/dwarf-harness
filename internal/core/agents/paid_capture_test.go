package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// Environment rotation is necessarily sequential; all immutable-source rows
// remain parallel. No actual provider credential or provider-key shape is used.
func paidLiveCredentialRows(t *testing.T) {
	for _, name := range []string{"pinned-before-source-change", "revoked-before-intent", "rotated-before-intent"} {
		t.Run(name, func(t *testing.T) {
			const source = "DWARF_PAID_SEND_ROTATION_SYNTHETIC"
			const old = "synthetic-pinned-before-change"
			var posts atomic.Int64
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+old {
					t.Error("dispatch reread mutable credential source")
				}
				paidSuccess(t, w, r)
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			t.Setenv(source, old)
			binding := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: source, RevokePreviousVersion: f.credential.CredentialVersion}
			if _, err := f.service.BindCredential(context.Background(), bindingOf(f.origin), binding); err != nil {
				t.Fatal(err)
			}
			f.credential = f.service.p.paidCredentials[binding.CredentialVersion]
			r := f.prepare(t)
			f.admit(t, r)
			t.Setenv(source, "synthetic-mutable-after-change")
			if name == "revoked-before-intent" {
				if _, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: f.id(), ScopeID: binding.ScopeID, ExpectedVersion: binding.ExpectedVersion + 1, Provider: "anthropic", CredentialVersion: binding.CredentialVersion}); err != nil {
					t.Fatal(err)
				}
			}
			if name == "rotated-before-intent" {
				binding.CommandID = f.id()
				binding.ExpectedVersion++
				binding.RevokePreviousVersion = binding.CredentialVersion
				binding.CredentialVersion = f.id()
				if _, err := f.service.BindCredential(context.Background(), bindingOf(f.origin), binding); err != nil {
					t.Fatal(err)
				}
			}
			before := f.store.Durable()
			d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
			out, err := f.service.DispatchPaid(context.Background(), d)
			if name == "pinned-before-source-change" {
				if err != nil {
					t.Fatal(err)
				}
				paidRecord(t, f, d, out)
				if posts.Load() != 1 {
					t.Fatal("authorized pinned exchange was not sent")
				}
			} else if err == nil || posts.Load() != 0 || f.store.Durable() != before {
				t.Fatal("revoked credential reached network")
			}
			paidAssertNoSecret(t, f.root, old)
		})
	}
}
func paidAssertNoSecret(t *testing.T, root, value string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "lock" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(body, []byte(value)) {
			t.Error("credential bytes escaped capture boundary")
		}
		if json.Valid(body) && paidContainsSecret(body, []byte(value)) {
			t.Error("escaped credential bytes persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestPaidLiteralCredentialInputRefused(t *testing.T) {
	t.Parallel()
	for _, escaped := range []bool{false, true} {
		t.Run(map[bool]string{false: "literal", true: "decoded-escape"}[escaped], func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			input := f.input()
			value := os.Getenv(paidTestEnvironment)
			input.Request.Messages[0].Content[0].Text = value
			if escaped {
				raw, err := json.Marshal(input.Request)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.ReplaceAll(raw, []byte("a"), []byte(`\u0061`))
				input.Request, err = providers.ParseRequest(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
			if err != nil {
				t.Fatal(err)
			}
			beforeObjects, err := os.ReadDir(scope.objects())
			if err != nil {
				t.Fatal(err)
			}
			before := f.store.Durable()
			p, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if !errors.Is(err, ErrInvalid) || p != nil || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
				t.Fatal("literal pinned credential reached sealing or admission")
			}
			afterObjects, err := os.ReadDir(scope.objects())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeObjects, afterObjects) || len(f.service.paid.prepared) != 0 {
				t.Fatal("refusal left an object or physical preparation")
			}
			paidAssertNoSecret(t, f.root, value)
		})
	}
}
func TestPaidCaptureRejectsCredentialEcho(t *testing.T) {
	t.Parallel()
	for _, where := range []string{"text", "escaped-tool", "opaque-extension", "usage-key", "stop-details", "header", "error"} {
		t.Run(where, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if where == "header" {
					w.Header().Set("Request-Id", value)
				}
				if where == "error" {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, `{"error":{"type":"api_error","message":"`+value+`"}}`)
					return
				}
				body := paidSSE(model, `{"input_tokens":7,"output_tokens":4}`, `{"output_tokens":4}`, "safe", "end_turn", "", true)
				switch where {
				case "text":
					body = strings.Replace(body, `"safe"`, `"`+value+`"`, 1)
				case "usage-key":
					body = strings.Replace(body, `"input_tokens":7`, `"`+value+`":0,"input_tokens":7`, 1)
				case "stop-details":
					body = strings.Replace(body, `"stop_details":null`, `"stop_details":{"other":"`+value+`"}`, 1)
				case "opaque-extension":
					body = strings.Replace(body, `"type":"text","text":""`, `"type":"redacted_thinking","data":"safe","extension":"`+value+`"`, 1)
					start := strings.Index(body, "data: {\"type\":\"content_block_delta\"")
					end := strings.Index(body[start:], "\n\n")
					body = body[:start] + body[start+end+2:]
				case "escaped-tool":
					body = strings.Replace(body, `"type":"text","text":""`, `"type":"tool_use","id":"local","name":"local","input":{"secret":"`+strings.ReplaceAll(value, "a", `\u0061`)+`"}`, 1)
					start := strings.Index(body, "data: {\"type\":\"content_block_delta\"")
					end := strings.Index(body[start:], "\n\n")
					body = body[:start] + body[start+end+2:]
				}
				_, _ = io.WriteString(w, body)
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			_, d, out := paidRun(t, f)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if v.State != "unknown" || v.HeldMicros == "0" || len(v.Result.MessageObjects) != 0 {
				t.Fatal("provider credential echo became output or settlement")
			}
			paidAssertNoSecret(t, f.root, os.Getenv(paidTestEnvironment))
		})
	}
}
