package validation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func recordedStream(binding canaryBinding) string {
	marker, _ := json.Marshal(binding)
	output, _ := json.Marshal("    dwarf_canary_v1_test.go:9: DWARF_CANARY_V1:" + string(marker) + ":1234\n")
	return fmt.Sprintf(`{"Action":"start","Package":"example.test/check"}
{"Action":"run","Package":"example.test/check","Test":"TestRequired"}
{"Action":"output","Package":"example.test/check","Test":"TestRequired","Output":"=== RUN   TestRequired\n","OutputType":"frame"}
{"Action":"pass","Package":"example.test/check","Test":"TestRequired"}
{"Action":"run","Package":"example.test/check","Test":"TestDwarfCanaryV1"}
{"Action":"output","Package":"example.test/check","Test":"TestDwarfCanaryV1","Output":%s}
{"Action":"output","Package":"example.test/check","Test":"TestDwarfCanaryV1","Output":"    dwarf_canary_v1_test.go:10: dwarf sentinel expected failure\n","OutputType":"error"}
{"Action":"fail","Package":"example.test/check","Test":"TestDwarfCanaryV1"}
{"Action":"fail","Package":"example.test/check"}
`, output)
}

func TestGoEventReconcile(t *testing.T) {
	t.Parallel()
	check := ledger.CheckSpec{Inventory: []ledger.ValidationInventoryNode{{Package: "example.test/check", Name: "TestRequired", Expected: "passed"}}, Canary: ledger.ValidationCanarySpec{TestName: "TestDwarfCanaryV1", TemplateSHA256: ledger.Digest{1}}}
	binding := canaryBinding{Protocol: "go-fail-v1", AttemptID: uuid.NewV7(), RunID: uuid.NewV7(), BindingSHA256: ledger.Digest{2}, TemplateSHA256: check.Canary.TemplateSHA256, Nonce: strings.Repeat("a", 64)}
	stream := recordedStream(binding)
	for _, row := range []struct {
		name         string
		stream       string
		exit         int64
		closed       bool
		want, reason string
		suspect      bool
	}{
		{"complete", stream, 1, true, "passed", "required_passed", false},
		{"missing terminal", strings.Replace(stream, `{"Action":"pass","Package":"example.test/check","Test":"TestRequired"}`+"\n", "", 1), 1, true, "incomplete", "terminal_missing", false},
		{"cached", strings.Replace(stream, `{"Action":"fail","Package":"example.test/check"}`, `{"Action":"output","Package":"example.test/check","Output":"ok example.test/check (cached)\n"}`+"\n"+`{"Action":"fail","Package":"example.test/check"}`, 1), 1, true, "incomplete", "cached_output", false},
		{"passed canary", strings.Replace(stream, `"Action":"fail","Package":"example.test/check","Test":"TestDwarfCanaryV1"`, `"Action":"pass","Package":"example.test/check","Test":"TestDwarfCanaryV1"`, 1), 1, true, "incomplete", "canary_passed", true},
		{"nonce mismatch", strings.Replace(stream, strings.Repeat("a", 64), strings.Repeat("b", 64), 1), 1, true, "incomplete", "canary_unbound", true},
		{"exit disagreement", stream, 0, true, "incomplete", "exit_mismatch", false},
		{"closure unproven", stream, 1, false, "incomplete", "recovery_pending", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			r := newGoReducer(check, binding)
			// Feed splits through JSON strings and marker text, as owned pipe
			// reads do; no event-sized buffering contract is assumed.
			for p := []byte(row.stream); len(p) != 0; {
				n := min(17, len(p))
				r.write(p[:n])
				p = p[n:]
			}
			r.finish()
			f := ledger.ValidationAttemptFinished{Started: true, ExitKnown: true, ExitCode: &row.exit, RootExited: true, TreeClosed: row.closed, IOJoined: true, ClosureProof: "live", InputIntegrity: "verified"}
			reconcileGo(r, &f, "", false)
			if f.Outcome != row.want || f.Reason != row.reason || f.RunnerSuspect != row.suspect {
				t.Fatalf("outcome=%s reason=%s suspect=%v; want %s/%s/%v", f.Outcome, f.Reason, f.RunnerSuspect, row.want, row.reason, row.suspect)
			}
			if row.want == "passed" && (f.Canary.State != "expected_failed" || f.RequiredResults[0].Outcome != "passed") {
				t.Fatalf("missing independent evidence: %+v", f)
			}
		})
	}
}
