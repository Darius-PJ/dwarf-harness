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
