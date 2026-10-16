package ledger

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// These fixtures cover ticket 09's part of F2, proposed for the owner's
// freeze (D113), through its public payload encoders and decoders.
func decodeF2_09(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypePolicyActivationRequested:
		return DecodePolicyActivationRequested(js)
	case TypePolicyActivationHoldChanged:
		return DecodePolicyActivationHoldChanged(js)
	case TypePolicyActivationEnded:
		return DecodePolicyActivationEnded(js)
	case TypePolicyActivated:
		return DecodePolicyActivated(js)
	}
	panic("not an F2-09 type: " + typ)
}

func f2_09Payloads() []struct {
	name string
	typ  string
	p    payload
} {
	request := PolicyActivationRequested{
		RequestID: fid("39"), OwnerApprovalID: fid("49"), PinID: fid("87"),
		FieldLawSHA256: goldenB1, ValidationPolicyHash: goldenB2,
		WorkspacePolicies: []WorkspacePolicy{}, AffectedWorkspaceIDs: []uuid.UUID{},
		Schedule: ScheduleImmediate, AdmissionUTCNS: -1, NotBeforeUTCNS: -1,
	}
	full := request
	full.WorkspacePolicies = []WorkspacePolicy{{fid("33"), goldenB1, goldenB2}, {fid("34"), goldenB2, goldenB3}}
	full.AffectedWorkspaceIDs = []uuid.UUID{fid("33"), fid("34")}
	scheduled := full
	scheduled.Schedule, scheduled.AdmissionUTCNS, scheduled.NotBeforeUTCNS = ScheduleNotBefore, math.MaxInt64-1, math.MaxInt64
	scheduled.ExpectedActiveRecordHash = goldenB3
	replacement := scheduled
	replacement.ReplacesRequestID, replacement.ReplacesRevision = fid("3a"), math.MaxInt64
	hold := PolicyActivationHoldChanged{RequestID: request.RequestID, ActivationRequestRecordHash: goldenB3, Hold: HoldDue}
	negativeHold := hold
	negativeHold.ObservedUTCNS = math.MinInt64
	none, untrusted := hold, hold
	none.Hold, none.ObservedUTCNS, untrusted.Hold = HoldNone, math.MaxInt64, HoldClockUntrusted
	ended := PolicyActivationEnded{RequestID: request.RequestID, ActivationRequestRecordHash: goldenB3, ExpectedPendingRevision: 1, Outcome: ActivationCancelled}
	replaced := ended
	replaced.Outcome, replaced.ReplacementRequestID, replaced.ExpectedPendingRevision = ActivationReplaced, fid("3a"), math.MaxInt64
	future, futureReplacement := ended, replaced
	future.Outcome, futureReplacement.Outcome = "future_outcome", "future_outcome"
	activated := PolicyActivated{
		ActivationID: request.RequestID, Epoch: 1, FieldLawSHA256: goldenB1, ValidationPolicyHash: goldenB2,
		WorkspacePolicies: []WorkspacePolicy{}, OwnerApprovalID: request.OwnerApprovalID,
		ActivationRequestRecordHash: goldenB3, NotBeforeUTCNS: math.MinInt64, EffectiveUTCNS: math.MinInt64,
	}
	later := activated
	later.Epoch, later.ExpectedActiveRecordHash, later.EffectiveUTCNS = math.MaxInt64, goldenB1, math.MaxInt64
	later.WorkspacePolicies = full.WorkspacePolicies
	zeroRequest, minimumRequest, maximumRequest := request, request, request
	zeroRequest.AdmissionUTCNS, zeroRequest.NotBeforeUTCNS = 0, 0
	minimumRequest.AdmissionUTCNS, minimumRequest.NotBeforeUTCNS = math.MinInt64, math.MinInt64
	maximumRequest.AdmissionUTCNS, maximumRequest.NotBeforeUTCNS = math.MaxInt64, math.MaxInt64
	zeroActivation := activated
	zeroActivation.NotBeforeUTCNS, zeroActivation.EffectiveUTCNS = 0, 0
	return []struct {
		name string
		typ  string
		p    payload
	}{
		{"genesis empty", TypePolicyActivationRequested, request},
		{"genesis policies", TypePolicyActivationRequested, full},
		{"scheduled", TypePolicyActivationRequested, scheduled},
		{"replacement", TypePolicyActivationRequested, replacement},
		{"due zero", TypePolicyActivationHoldChanged, hold},
		{"due minimum", TypePolicyActivationHoldChanged, negativeHold},
		{"none", TypePolicyActivationHoldChanged, none},
		{"clock untrusted", TypePolicyActivationHoldChanged, untrusted},
		{"cancelled", TypePolicyActivationEnded, ended},
		{"replaced", TypePolicyActivationEnded, replaced},
		{"future", TypePolicyActivationEnded, future},
		{"future replacement", TypePolicyActivationEnded, futureReplacement},
		{"activated genesis", TypePolicyActivated, activated},
		{"activated later", TypePolicyActivated, later},
		{"request zero", TypePolicyActivationRequested, zeroRequest},
		{"request minimum", TypePolicyActivationRequested, minimumRequest},
		{"request maximum", TypePolicyActivationRequested, maximumRequest},
		{"activated zero", TypePolicyActivated, zeroActivation},
	}
}

func TestF2_09PayloadsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_09Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js := mustEncode(tc.p)
			nested := bytes.ReplaceAll(js, []byte(`{"workspace_id":`), []byte(`{"later":{"a":[1,null]},"workspace_id":`))
			for _, data := range [][]byte{js, nested, append([]byte(`{"later":{"a":[1,null],"b":"x"},`), js[1:]...)} {
				got, err := decodeF2_09(tc.typ, data)
				if err != nil || !reflect.DeepEqual(got, tc.p) {
					t.Fatalf("Decode(%s) = %+v, %v; want %+v", data, got, err, tc.p)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encoding = %s, %v; want %s", again, err, js)
				}
			}
			// Required empty slices have the same wire form whether nil or empty.
			v := reflect.New(reflect.TypeOf(tc.p)).Elem()
			v.Set(reflect.ValueOf(tc.p))
			for i := range v.NumField() {
				if v.Field(i).Kind() == reflect.Slice && v.Field(i).Len() == 0 {
					v.Field(i).SetZero()
				}
			}
			if got := mustEncode(v.Interface().(payload)); !bytes.Equal(got, js) {
				t.Fatalf("nil required array changed encoding: %s; want %s", got, js)
			}
		})
	}
	// An open outcome accepts the longest token, including optional identity.
	ended := f2_09Payloads()[11].p.(PolicyActivationEnded)
	ended.Outcome = "a" + strings.Repeat("0_", 31) + "z"
	if got, err := DecodePolicyActivationEnded(mustEncode(ended)); err != nil || got != ended {
		t.Fatalf("maximum token: %+v, %v; want %+v", got, err, ended)
	}
}

// goActivationPayload constructs the Go-side analogue without using any
// contract decoder. Digest has MarshalText but no inverse; turn only digest
// strings into byte arrays for encoding/json's ordinary struct decoder.
func goActivationPayload(data []byte, value any) error {
	var wire any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	var arrays func(any)
	arrays = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if strings.HasSuffix(key, "_hash") || strings.HasSuffix(key, "_sha256") {
					if s, ok := item.(string); ok {
						if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
							ns := make([]int, len(b))
							for i, n := range b {
								ns[i] = int(n)
							}
							v[key] = ns
						}
					}
				}
				arrays(item)
			}
		case []any:
			for _, item := range v {
				arrays(item)
			}
		}
	}
	arrays(wire)
	rewritten, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	return json.Unmarshal(rewritten, value)
}

func TestF2_09PayloadsRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range f2_09Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js := mustEncode(tc.p)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(js, &fields); err != nil {
				t.Fatal(err)
			}
			refuse := func(name string, bad []byte, checkEncode bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if _, err := decodeF2_09(tc.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Errorf("Decode(%s) = %v; want ErrInvalid", bad, err)
					}
					if !checkEncode {
						return
					}
					p := reflect.New(reflect.TypeOf(tc.p))
					if goActivationPayload(bad, p.Interface()) != nil {
						return
					}
					if out, err := p.Elem().Interface().(payload).Encode(); !errors.Is(err, ErrInvalid) || out != nil {
						t.Errorf("Encode(%s) = %s, %v; want nil, ErrInvalid", bad, out, err)
					}
				})
			}
			mutate := func(name, key, value string, checkEncode bool) {
				t.Helper()
				bad := maps.Clone(fields)
				if value == "" {
					delete(bad, key)
				} else {
					bad[key] = json.RawMessage(value)
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				refuse(name, data, checkEncode)
			}
			optional := map[string]bool{"expected_active_record_hash": true, "replaces_request_id": true, "replaces_revision": true, "replacement_request_id": true}
			for _, key := range slices.Sorted(maps.Keys(fields)) {
				value := fields[key]
				if !optional[key] {
					// Zero signed instants and nil arrays are valid Go values, not absence.
					goRequired := !strings.HasSuffix(key, "_utc_ns") && key != "workspace_policies" && key != "affected_workspace_ids"
					mutate("missing "+key, key, "", goRequired)
				}
				mutate("null "+key, key, "null", false)
				refuse("repeated "+key, append([]byte(`{"`+key+`":`+string(value)+`,`), js[1:]...), false)
				switch {
				case strings.HasSuffix(key, "_id"):
					for _, value := range []string{`"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, `"00000000-0000-0000-0000-000000000000"`, `"0199C000-0000-7000-8000-000000000039"`, `1`} {
						checkEncode := strings.Contains(value, "4c3a") || !optional[key] && strings.Contains(value, "00000000-0000-0000")
						mutate("ID "+key+"="+value, key, value, checkEncode)
					}
				case strings.HasSuffix(key, "_hash"), strings.HasSuffix(key, "_sha256"):
					for _, value := range []string{`"` + strings.Repeat("0", 64) + `"`, `"` + strings.Repeat("A", 64) + `"`, `"abc"`, `1`} {
						mutate("digest "+key+"="+value, key, value, !optional[key] && value == `"`+strings.Repeat("0", 64)+`"`)
					}
				case strings.HasSuffix(key, "_utc_ns"):
					for _, value := range []string{`-0`, `1.0`, `1e3`, `"1"`, `9223372036854775808`, `-9223372036854775809`, `true`, `[]`, `{}`} {
						mutate("signed "+key+"="+value, key, value, false)
					}
					for _, value := range []string{`01`, `-01`, `+1`} {
						refuse("signed "+key+"="+value, bytes.Replace(js, []byte(`"`+key+`":`+string(fields[key])), []byte(`"`+key+`":`+value), 1), false)
					}
				case key == "epoch" || strings.HasSuffix(key, "revision"):
					for _, value := range []string{`0`, `-1`, `1.0`, `1e3`, `"1"`, `9223372036854775808`} {
						mutate("positive "+key+"="+value, key, value, value == "-1" || value == "0")
					}
				}
			}
			refuse("not object", []byte(`[]`), false)
			refuse("malformed", []byte(`{"`), false)
			refuse("nested repeated unknown", append([]byte(`{"later":{"x":1,"x":2},`), js[1:]...), false)
			if _, ok := fields["workspace_policies"]; ok {
				one := `{"workspace_id":"` + fid("33").String() + `","check_policy_hash":"` + goldenB1.String() + `","environment_policy_hash":"` + goldenB2.String() + `"}`
				two := strings.Replace(one, fid("33").String(), fid("34").String(), 1)
				for _, value := range []string{`null`, `{}`, `1`, `[1]`, `[null]`, `[` + one + `,` + one + `]`, `[` + two + `,` + one + `]`} {
					mutate("policies="+value, "workspace_policies", value, value != "null")
				}
				var element map[string]json.RawMessage
				if err := json.Unmarshal([]byte(one), &element); err != nil {
					t.Fatal(err)
				}
				for _, key := range slices.Sorted(maps.Keys(element)) {
					for _, value := range []string{"", "null", `1`} {
						bad := maps.Clone(element)
						if value == "" {
							delete(bad, key)
						} else {
							bad[key] = json.RawMessage(value)
						}
						el, err := json.Marshal(bad)
						if err != nil {
							t.Fatal(err)
						}
						mutate("element "+key+"="+value, "workspace_policies", `[`+string(el)+`]`, true)
					}
					mutate("element repeated "+key, "workspace_policies", `[{"`+key+`":`+string(element[key])+`,`+one[1:]+`]`, false)
					bad := strings.Replace(one, string(element[key]), `"`+strings.Repeat("0", 64)+`"`, 1)
					if key == "workspace_id" {
						bad = strings.Replace(one, string(element[key]), `"6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"`, 1)
					}
					mutate("element invalid "+key, "workspace_policies", `[`+bad+`]`, true)
				}
			}
			switch p := tc.p.(type) {
			case PolicyActivationRequested:
				mutate("unknown schedule", "schedule", `"later"`, true)
				if p.Schedule == ScheduleImmediate {
					mutate("immediate differs", "not_before_utc_ns", `1`, true)
				} else {
					mutate("scheduled equal", "not_before_utc_ns", string(fields["admission_utc_ns"]), true)
					mutate("scheduled earlier", "not_before_utc_ns", `-1`, true)
				}
				if p.ReplacesRequestID == (uuid.UUID{}) {
					mutate("replacement ID alone", "replaces_request_id", `"`+fid("3a").String()+`"`, true)
					mutate("replacement revision alone", "replaces_revision", `1`, true)
					mutate("present zero replacement revision", "replaces_revision", `0`, false)
					mutate("present null replacement revision", "replaces_revision", `null`, false)
					mutate("present zero replacement ID", "replaces_request_id", `"00000000-0000-0000-0000-000000000000"`, false)
				} else {
					mutate("missing replacement ID", "replaces_request_id", "", true)
					mutate("missing replacement revision", "replaces_revision", "", true)
					mutate("replaces itself", "replaces_request_id", string(fields["request_id"]), true)
				}
				mutate("approval equals request", "owner_approval_id", string(fields["request_id"]), true)
				mutate("pin equals request", "pin_id", string(fields["request_id"]), true)
				mutate("pin equals approval", "pin_id", string(fields["owner_approval_id"]), true)
				for _, value := range []string{`null`, `{}`, `1`, `[1]`, `[null]`, `["` + fid("33").String() + `","` + fid("33").String() + `"]`, `["` + fid("34").String() + `","` + fid("33").String() + `"]`, `["6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f"]`} {
					mutate("affected="+value, "affected_workspace_ids", value, value != "null")
				}
			case PolicyActivationHoldChanged:
				mutate("unknown hold", "hold", `"later"`, true)
				if p.Hold == HoldClockUntrusted {
					mutate("untrusted observed zero", "observed_utc_ns", `0`, false)
					mutate("untrusted observed nonzero", "observed_utc_ns", `1`, true)
				}
			case PolicyActivationEnded:
				for _, value := range []string{`""`, `"A"`, `"a.b"`, `"a-b"`, `"0a"`, `"a b"`, `"` + strings.Repeat("a", 65) + `"`} {
					mutate("outcome="+value, "outcome", value, true)
				}
				if p.Outcome == ActivationReplaced {
					mutate("missing replacement", "replacement_request_id", "", true)
					mutate("replaces itself", "replacement_request_id", string(fields["request_id"]), true)
				} else if p.Outcome == ActivationCancelled {
					mutate("cancelled replacement", "replacement_request_id", `"`+fid("3a").String()+`"`, true)
				}
			case PolicyActivated:
				mutate("approval equals activation", "owner_approval_id", string(fields["activation_id"]), true)
				if p.Epoch == 1 {
					mutate("genesis expectation", "expected_active_record_hash", `"`+goldenB1.String()+`"`, true)
					mutate("later without expectation", "epoch", `2`, true)
				} else {
					mutate("later missing expectation", "expected_active_record_hash", "", true)
					mutate("genesis with expectation", "epoch", `1`, true)
				}
				bad := maps.Clone(fields)
				bad["not_before_utc_ns"], bad["effective_utc_ns"] = json.RawMessage(`0`), json.RawMessage(`-1`)
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				refuse("effective before schedule", data, true)
			}
		})
	}
}
