package winexec

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
	"uuid"
)

const markerLimit = 4096

type rootMarker struct {
	Format int    `json:"format"`
	RootID string `json:"root_id"`
	Owner  string `json:"owner_sid"`
}
type runFence struct {
	Format         int    `json:"format"`
	RootID         string `json:"root_id"`
	RunID          string `json:"run_id"`
	ProfileName    string `json:"profile_name"`
	ProfileCreated bool   `json:"profile_created"`
	ProfilePath    string `json:"profile_path,omitempty"`
	ProfileVolume  string `json:"profile_volume,omitempty"`
	ProfileFileID  string `json:"profile_file_id,omitempty"`
	State          string `json:"state"`
	Proof          string `json:"closed_proof,omitempty"`
}

func canonicalID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s && id[6]>>4 == 7 && id[8]&0xc0 == 0x80
}
func canonicalDrivePath(s string) bool {
	if len(s) < 3 || !utf8.ValidString(s) || strings.ContainsAny(s, "/\x00") || s[1] != ':' || s[2] != '\\' || !(s[0] >= 'A' && s[0] <= 'Z' || s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	if len(s) == 3 {
		return false
	}
	for _, p := range strings.Split(s[3:], "\\") {
		if p == "" || p == "." || p == ".." || strings.Contains(p, ":") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") {
			return false
		}
	}
	return true
}
func strictObject(data []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	if len(data) > markerLimit || !utf8.Valid(data) {
		return nil, ErrQuarantined
	}
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, ErrQuarantined
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, ErrQuarantined
		}
		key, ok := t.(string)
		if !ok || !allowed[key] {
			return nil, ErrQuarantined
		}
		if _, exists := fields[key]; exists {
			return nil, ErrQuarantined
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, ErrQuarantined
		}
		fields[key] = raw
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return nil, ErrQuarantined
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrQuarantined
	}
	if string(fields["format"]) != "1" {
		return nil, ErrQuarantined
	}
	return fields, nil
}
func decodeRoot(data []byte, owner string) (rootMarker, error) {
	allowed := map[string]bool{"format": true, "root_id": true, "owner_sid": true}
	fields, err := strictObject(data, allowed)
	var r rootMarker
	if err != nil || len(fields) != 3 {
		return r, ErrInvalidRoot
	}
	if err := json.Unmarshal(data, &r); err != nil || !canonicalID(r.RootID) || r.Owner != owner {
		return rootMarker{}, ErrInvalidRoot
	}
	return r, nil
}
func decodeFence(data []byte, rootID, runID string) (runFence, error) {
	allowed := map[string]bool{"format": true, "root_id": true, "run_id": true, "profile_name": true, "profile_created": true, "profile_path": true, "profile_volume": true, "profile_file_id": true, "state": true, "closed_proof": true}
	fields, err := strictObject(data, allowed)
	var f runFence
	if err != nil {
		return f, ErrQuarantined
	}
	for _, key := range []string{"format", "root_id", "run_id", "profile_name", "profile_created", "state"} {
		if _, ok := fields[key]; !ok {
			return f, ErrQuarantined
		}
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return runFence{}, ErrQuarantined
	}
	if !canonicalID(rootID) || !canonicalID(runID) || f.RootID != rootID || f.RunID != runID || f.ProfileName != "dwarf-harness-"+runID {
		return runFence{}, ErrQuarantined
	}
	if string(fields["profile_created"]) != "true" && string(fields["profile_created"]) != "false" {
		return runFence{}, ErrQuarantined
	}
	for _, key := range []string{"profile_path", "profile_volume", "profile_file_id"} {
		_, present := fields[key]
		if present != f.ProfileCreated {
			return runFence{}, ErrQuarantined
		}
	}
	validHex := func(s string, n int) bool {
		_, err := hex.DecodeString(s)
		return err == nil && len(s) == n && s == strings.ToLower(s)
	}
	if f.ProfileCreated && (!canonicalDrivePath(f.ProfilePath) || !validHex(f.ProfileVolume, 8) || !validHex(f.ProfileFileID, 16)) {
		return runFence{}, ErrQuarantined
	}
	_, hasProof := fields["closed_proof"]
	switch f.State {
	case "unconfirmed":
		if hasProof {
			return runFence{}, ErrQuarantined
		}
	case "closed":
		if !hasProof || f.Proof != "not_created" && f.Proof != "job_empty" || f.Proof == "job_empty" && !f.ProfileCreated {
			return runFence{}, ErrQuarantined
		}
	default:
		return runFence{}, ErrQuarantined
	}
	return f, nil
}
