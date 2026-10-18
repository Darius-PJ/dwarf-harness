package winexec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"uuid"
)

func TestFenceRejectsAmbiguousClosure(t *testing.T) {
	t.Parallel()
	root, run := uuid.NewV7().String(), uuid.NewV7().String()
	f := runFence{Format: 1, RootID: root, RunID: run, ProfileName: "dwarf-harness-" + run, ProfileCreated: true, ProfilePath: `C:\Users\owner\AppData\Local\Packages\dwarf-harness-` + run + `\AC`, ProfileVolume: "01234567", ProfileFileID: "0123456789abcdef", State: "closed", Proof: "job_empty"}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := decodeFence(data, root, run); err != nil || decoded != f {
		t.Fatalf("valid closed proof: %+v %v", decoded, err)
	}
	base := string(data)
	cases := map[string]string{
		"duplicate-key":          strings.Replace(base, `"format":1`, `"format":1,"format":1`, 1),
		"alternate-number":       strings.Replace(base, `"format":1`, `"format":1.0`, 1),
		"unknown-key":            strings.Replace(base, `"format":1`, `"format":1,"safe":true`, 1),
		"missing-key":            strings.Replace(base, `"profile_created":true,`, "", 1),
		"null-creation":          strings.Replace(base, `"profile_created":true`, `"profile_created":null`, 1),
		"false-with-binding":     strings.Replace(base, `"profile_created":true`, `"profile_created":false`, 1),
		"unconfirmed-with-proof": strings.Replace(base, `"state":"closed"`, `"state":"unconfirmed"`, 1),
		"unknown-proof":          strings.Replace(base, `"closed_proof":"job_empty"`, `"closed_proof":"root_exit"`, 1),
		"missing-proof":          strings.Replace(base, `,"closed_proof":"job_empty"`, "", 1),
		"foreign-run":            strings.ReplaceAll(base, run, uuid.NewV7().String()),
		"foreign-root":           strings.Replace(base, root, uuid.NewV7().String(), 1),
		"noncanonical-volume":    strings.Replace(base, `"profile_volume":"01234567"`, `"profile_volume":"ABCDEF12"`, 1),
		"traversal-path":         strings.Replace(base, `\\AC"`, `\\..\\AC"`, 1),
		"trailing-value":         base + ` {}`,
		"truncated":              base[:len(base)-1],
		"oversized":              base + strings.Repeat(" ", markerLimit),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeFence([]byte(data), root, run); !errors.Is(err, ErrQuarantined) {
				t.Fatalf("ambiguous proof accepted: %v", err)
			}
		})
	}
}
func TestRootMarkerBindsNativeOwner(t *testing.T) {
	t.Parallel()
	id := uuid.NewV7().String()
	data := []byte(`{"format":1,"root_id":"` + id + `","owner_sid":"S-1-5-21-1-2-3-4"}`)
	if _, err := decodeRoot(data, "S-1-5-21-9-8-7-6"); !errors.Is(err, ErrInvalidRoot) {
		t.Fatalf("foreign owner accepted: %v", err)
	}
	if _, err := decodeRoot(append(data, []byte(`{}`)...), "S-1-5-21-1-2-3-4"); !errors.Is(err, ErrInvalidRoot) {
		t.Fatalf("trailing marker accepted: %v", err)
	}
}

func FuzzFenceDecode(f *testing.F) {
	const root = "018f0000-0000-7000-8000-000000000001"
	const run = "018f0000-0000-7000-8000-000000000002"
	base := `{"format":1,"root_id":"` + root + `","run_id":"` + run + `","profile_name":"dwarf-harness-` + run + `","profile_created":false,"state":"unconfirmed"}`
	closed := strings.Replace(base, `"state":"unconfirmed"`, `"state":"closed","closed_proof":"not_created"`, 1)
	bound := strings.Replace(base, `"profile_created":false`, `"profile_created":true,"profile_path":"C:\\Users\\owner\\AppData\\Local\\Packages\\dwarf-harness-`+run+`\\AC","profile_volume":"01234567","profile_file_id":"0123456789abcdef"`, 1)
	for _, seed := range []string{
		base, closed, bound,
		strings.Replace(bound, `"state":"unconfirmed"`, `"state":"closed","closed_proof":"job_empty"`, 1),
		strings.Replace(closed, `"not_created"`, `"root_exit"`, 1),
		strings.Replace(closed, `"not_created"`, `"job_empty"`, 1),
		strings.Replace(base, `"format":1`, `"format":1,"format":1`, 1),
		strings.Replace(base, `"profile_created":false`, `"profile_created":null`, 1),
		strings.Replace(base, root, run, 1),
		base + `{}`, base[:len(base)-1],
		base + strings.Repeat(" ", markerLimit-len(base)),
		base + strings.Repeat(" ", markerLimit+1-len(base)),
		"", string([]byte{0xff, 0xfe}),
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		got, err := decodeFence(data, root, run)
		if err != nil {
			if !errors.Is(err, ErrQuarantined) {
				t.Fatalf("invalid fence did not quarantine: %v", err)
			}
			return
		}
		if len(data) > markerLimit || got.Format != 1 || got.RootID != root || got.RunID != run || got.ProfileName != "dwarf-harness-"+run {
			t.Fatalf("accepted unbounded or foreign authority: %+v", got)
		}
		switch got.State {
		case "unconfirmed":
			if got.Proof != "" {
				t.Fatalf("uncertainty acquired closure proof: %+v", got)
			}
		case "closed":
			if got.Proof != "not_created" && got.Proof != "job_empty" || got.Proof == "job_empty" && !got.ProfileCreated {
				t.Fatalf("unsupported closure proof: %+v", got)
			}
		default:
			t.Fatalf("unknown authority state: %+v", got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		again, err := decodeFence(encoded, root, run)
		if err != nil || again != got {
			t.Fatalf("authority changed on canonical encoding: %+v %v", again, err)
		}
		// A valid proof cannot authorize a different owner/run, a second JSON
		// value, or contradictory duplicate state even after canonicalization.
		for _, ids := range [][2]string{{run, run}, {root, root}} {
			if _, err := decodeFence(encoded, ids[0], ids[1]); !errors.Is(err, ErrQuarantined) {
				t.Fatalf("foreign context accepted: %v", err)
			}
		}
		for _, ambiguous := range []string{
			string(encoded) + `{}`,
			string(encoded[:len(encoded)-1]) + `,"state":"closed"}`,
			string(encoded[:len(encoded)-1]) + `,"unknown_authority":true}`,
		} {
			if _, err := decodeFence([]byte(ambiguous), root, run); !errors.Is(err, ErrQuarantined) {
				t.Fatalf("ambiguous authority accepted: %v", err)
			}
		}
	})
}
