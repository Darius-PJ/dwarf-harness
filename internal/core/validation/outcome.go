package validation

import (
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reconcileGo(r *goReducer, f *ledger.ValidationAttemptFinished, stopReason string, overflow bool) {
	f.RequiredResults = r.results()
	f.Canary, f.RunnerSuspect = r.canary()
	f.Outcome, f.Reason = "no_status", "no_execution"
	if r.executed {
		f.Outcome = "incomplete"
	}
	if stopReason != "" {
		f.Outcome, f.Reason = "incomplete", stopReason
		if stopReason == "timeout" {
			f.Outcome = "timeout"
		}
		return
	}
	if !f.RootExited || !f.TreeClosed || !f.IOJoined || f.ClosureProof != "live" {
		f.Reason = "recovery_pending"
		return
	}
	if f.InputIntegrity != "verified" {
		switch f.InputIntegrity {
		case "changed":
			f.Reason = "input_changed"
		case "missing":
			f.Reason = "input_missing"
		default:
			f.Reason = "integrity_unavailable"
		}
		return
	}
	if overflow || r.invalid {
		f.Reason = "invalid_output"
		return
	}
	if r.cached {
		f.Reason = "cached_output"
		return
	}
	if r.buildFailed {
		if r.executed {
			f.Reason = "invalid_output"
			return
		}
		f.Outcome, f.Reason = "infra_failure", "build_failed"
		f.Canary.State, f.RunnerSuspect = "not_run", false
		return
	}
	if !r.executed {
		return
	}
	if f.Canary.State != "expected_failed" {
		f.Reason = "canary_" + f.Canary.State
		return
	}
	if !r.packageStarted || r.packageTerminal == "" {
		f.Reason = "terminal_missing"
		return
	}
	for _, n := range f.RequiredResults {
		if n.Outcome == "missing" || n.Outcome == "unfinished" {
			f.Reason = "terminal_missing"
			return
		}
	}
	if r.packageTerminal != "fail" || !f.ExitKnown || f.ExitCode == nil || *f.ExitCode != 1 {
		f.Reason = "exit_mismatch"
		return
	}
	for _, n := range f.RequiredResults {
		if n.Outcome == "skipped" {
			f.Outcome, f.Reason = "skipped", "required_skipped"
			return
		}
	}
	for _, n := range f.RequiredResults {
		if n.Outcome == "failed" {
			f.Outcome, f.Reason = "failed", "required_failed"
			return
		}
	}
	f.Outcome, f.Reason = "passed", "required_passed"
}

// Summary compares only attempts the caller has already bound to one comparison
// identity. Interrupted or unavailable attempts never establish an assertion.
func Summary(outcomes []string) string {
	pass, fail, other := false, false, len(outcomes) == 0
	for _, outcome := range outcomes {
		switch outcome {
		case "passed":
			pass = true
		case "failed":
			fail = true
		default:
			other = true
		}
	}
	if pass && fail {
		return "flaky"
	}
	if other {
		return "inconclusive"
	}
	if pass {
		return "passed"
	}
	return "failed"
}
