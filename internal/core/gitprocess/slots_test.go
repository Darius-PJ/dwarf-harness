package gitprocess

import (
	"bytes"
	"errors"
	"testing"
	"uuid"
)

func seedSlots() slotsContainer {
	c := slotsContainer{Header: slotsHeader{Format: slotsFormat, Version: 1, RootID: seedFence().RootID, BatchID: seedFence().RunID, Session: ^uint32(0)}}
	for i := range slotCount {
		c.Header.RunIDs = append(c.Header.RunIDs, uuid.UUID{6: 0x70, 8: 0x80, 15: byte(i + 3)})
	}
	return c
}

func runningSlot(c slotsContainer, i int) fence {
	f := c.Header.candidate(i)
	f.State, f.RootPID, f.RootCreated = "running", ^uint32(0), ^uint64(0)
	return f
}

func slotCodecCases() map[string][]byte {
	c := seedSlots()
	initial, _ := encodeSlots(c)
	c.Frames[0] = runningSlot(c, 0)
	running, _ := encodeSlots(c)
	cases := make(map[string][]byte)
	for _, name := range []string{"torn", "frame-padding", "footer", "footer-padding", "zero-retired", "closed-frame", "cross-link", "frame-session", "header-padding", "header-zero", "header-duplicate", "header-unknown", "header-null", "header-cardinality", "header-empty", "header-extra-id", "duplicate-id", "unsorted-id", "wrong-format", "batch-run", "bad-uuid"} {
		raw := bytes.Clone(running)
		header := c.Header
		header.RunIDs = append([]uuid.UUID(nil), header.RunIDs...)
		var replacement []byte
		switch name {
		case "torn":
			raw[slotHeaderSize+17] = 0
		case "frame-padding":
			raw[slotHeaderSize+slotFrameSize-1] = 1
		case "footer":
			raw[slotFooterOffset] = 2
		case "footer-padding":
			raw[slotsSize-1] = 1
		case "zero-retired":
			raw[slotFooterOffset+1] = 1
		case "closed-frame", "cross-link", "frame-session":
			f := c.Frames[0]
			if name == "closed-frame" {
				f.State, f.Proof = "closed", "name_absent"
			} else if name == "cross-link" {
				f.RunID = c.Header.RunIDs[1]
				f.JobName = jobName(f.RootID, f.RunID)
			} else {
				f.Session--
			}
			encoded, _ := canonicalJSON(f)
			clear(raw[slotHeaderSize : slotHeaderSize+slotFrameSize])
			copy(raw[slotHeaderSize:], encoded)
		case "header-padding":
			raw[slotHeaderSize-1] = 1
		case "header-zero":
			clear(raw[:slotHeaderSize])
		case "header-duplicate", "header-unknown", "header-null":
			replacement, _ = canonicalJSON(header)
			value := `"version":1,"version":1`
			if name == "header-unknown" {
				value = `"version":1,"extra":true`
			}
			if name == "header-null" {
				value = `"version":null`
			}
			replacement = bytes.Replace(replacement, []byte(`"version":1`), []byte(value), 1)
		case "header-cardinality":
			header.RunIDs = header.RunIDs[:31]
			replacement, _ = canonicalJSON(header)
		case "header-empty":
			header.RunIDs = nil
			replacement, _ = canonicalJSON(header)
		case "header-extra-id":
			header.RunIDs = append(header.RunIDs, uuid.UUID{6: 0x70, 8: 0x80, 15: 255})
			replacement, _ = canonicalJSON(header)
		case "duplicate-id":
			header.RunIDs[1] = header.RunIDs[0]
			replacement, _ = canonicalJSON(header)
		case "unsorted-id":
			header.RunIDs[0], header.RunIDs[1] = header.RunIDs[1], header.RunIDs[0]
			replacement, _ = canonicalJSON(header)
		case "wrong-format":
			header.Format = fenceFormat
			replacement, _ = canonicalJSON(header)
		case "batch-run":
			header.BatchID = header.RunIDs[3]
			replacement, _ = canonicalJSON(header)
		case "bad-uuid":
			header.RunIDs[0][6] = 0
			replacement, _ = canonicalJSON(header)
		}
		if replacement != nil {
			clear(raw[:slotHeaderSize])
			copy(raw, replacement)
		}
		cases[name] = raw
	}
	cases["short"] = initial[:len(initial)-1]
	cases["oversize"] = append(bytes.Clone(initial), 0)
	return cases
}

func TestGitProcessSlotCodec(t *testing.T) {
	t.Parallel()
	for name, raw := range slotCodecCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeSlots(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unsafe slot container accepted: %v", err)
			}
		})
	}
	for _, state := range []string{"initial", "running", "retired"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			c := seedSlots()
			if state != "initial" {
				c.Frames[31] = runningSlot(c, 31)
			}
			if state == "retired" {
				c.Retired[31] = 1
			}
			raw, err := encodeSlots(c)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeSlots(raw)
			if err != nil || got.Frames != c.Frames || got.Retired != c.Retired || got.initial() != (state == "initial") {
				t.Fatalf("lost root identity or retirement state: %+v %v", got, err)
			}
			encoded, err := encodeSlots(got)
			if err != nil || !bytes.Equal(raw, encoded) {
				t.Fatal("noncanonical slot roundtrip", err)
			}
		})
	}
}

func FuzzGitProcessSlots(f *testing.F) {
	c := seedSlots()
	for i := range 3 {
		if i > 0 {
			c.Frames[0] = runningSlot(c, 0)
		}
		if i == 2 {
			c.Retired[0] = 1
		}
		raw, _ := encodeSlots(c)
		f.Add(raw)
	}
	for _, raw := range slotCodecCases() {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := decodeSlots(raw)
		if err != nil {
			return
		}
		encoded, err := encodeSlots(c)
		if err != nil || len(raw) != slotsSize || !bytes.Equal(raw, encoded) {
			t.Fatal("accepted lossy or noncanonical slot container", err)
		}
	})
}
