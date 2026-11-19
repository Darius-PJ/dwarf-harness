package gitprocess

import (
	"bytes"
	"uuid"
)

const (
	slotsFormat      = "dwarf-harness/git-process-slots"
	slotCount        = 32
	slotHeaderSize   = 4096
	slotFrameSize    = 512
	slotFooterOffset = slotHeaderSize + slotCount*slotFrameSize
	slotsSize        = slotFooterOffset + slotFrameSize
)

type slotsHeader struct {
	Format  string      `json:"format"`
	Version int         `json:"version"`
	RootID  uuid.UUID   `json:"root_id"`
	BatchID uuid.UUID   `json:"batch_id"`
	Session uint32      `json:"windows_session_id"`
	RunIDs  []uuid.UUID `json:"run_ids"`
}

type slotsContainer struct {
	Header  slotsHeader
	Frames  [slotCount]fence
	Retired [slotCount]byte
}

func (h slotsHeader) validate() error {
	if h.Format != slotsFormat || h.Version != 1 || !validID(h.RootID) || !validID(h.BatchID) || h.RootID == h.BatchID || len(h.RunIDs) != slotCount {
		return ErrInvalid
	}
	for i, id := range h.RunIDs {
		if !validID(id) || id == h.RootID || id == h.BatchID || i > 0 && bytes.Compare(h.RunIDs[i-1][:], id[:]) >= 0 {
			return ErrInvalid
		}
	}
	return nil
}

func (h slotsHeader) candidate(i int) fence {
	id := h.RunIDs[i]
	return fence{Format: fenceFormat, Version: 1, RootID: h.RootID, RunID: id, JobName: jobName(h.RootID, id), Session: h.Session, State: "launch_pending"}
}

func (c slotsContainer) initial() bool {
	for i := range slotCount {
		if c.Frames[i] != (fence{}) || c.Retired[i] != 0 {
			return false
		}
	}
	return true
}

func paddedPrefix(raw []byte) ([]byte, error) {
	n := bytes.IndexByte(raw, 0)
	if n < 0 {
		return raw, nil
	}
	if n == 0 {
		return nil, ErrInvalid
	}
	for _, b := range raw[n:] {
		if b != 0 {
			return nil, ErrInvalid
		}
	}
	return raw[:n], nil
}

func encodeSlotFrame(f fence) ([]byte, error) {
	raw, err := canonicalJSON(f)
	if err != nil || f.State != "running" || len(raw) > slotFrameSize {
		return nil, ErrInvalid
	}
	if _, err := decodeFence(raw); err != nil {
		return nil, err
	}
	frame := make([]byte, slotFrameSize)
	copy(frame, raw)
	return frame, nil
}

func decodeSlots(raw []byte) (slotsContainer, error) {
	var c slotsContainer
	if len(raw) != slotsSize {
		return c, ErrInvalid
	}
	header, err := paddedPrefix(raw[:slotHeaderSize])
	if err != nil {
		return c, err
	}
	if err := decodeCanonical(header, &c.Header); err != nil {
		return c, err
	}
	if err := c.Header.validate(); err != nil {
		return c, err
	}
	for i := range slotCount {
		frame := raw[slotHeaderSize+i*slotFrameSize : slotHeaderSize+(i+1)*slotFrameSize]
		zero := true
		for _, b := range frame {
			zero = zero && b == 0
		}
		if !zero {
			prefix, err := paddedPrefix(frame)
			if err != nil {
				return c, err
			}
			f, err := decodeFence(prefix)
			if err != nil || f.State != "running" || f.RunID != c.Header.RunIDs[i] || f.RootID != c.Header.RootID || f.Session != c.Header.Session {
				return c, ErrInvalid
			}
			c.Frames[i] = f
		}
		c.Retired[i] = raw[slotFooterOffset+i]
		if c.Retired[i] > 1 || zero && c.Retired[i] != 0 {
			return c, ErrInvalid
		}
	}
	for _, b := range raw[slotFooterOffset+slotCount:] {
		if b != 0 {
			return c, ErrInvalid
		}
	}
	return c, nil
}

func encodeSlots(c slotsContainer) ([]byte, error) {
	if err := c.Header.validate(); err != nil {
		return nil, err
	}
	header, err := canonicalJSON(c.Header)
	if err != nil || len(header) > slotHeaderSize {
		return nil, ErrInvalid
	}
	raw := make([]byte, slotsSize)
	copy(raw, header)
	for i, f := range c.Frames {
		if f != (fence{}) {
			frame, err := encodeSlotFrame(f)
			if err != nil {
				return nil, err
			}
			copy(raw[slotHeaderSize+i*slotFrameSize:], frame)
		}
		raw[slotFooterOffset+i] = c.Retired[i]
	}
	if _, err := decodeSlots(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
