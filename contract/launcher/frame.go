package launcher

import (
	"encoding/binary"
	"io"
	"math"
	"unicode/utf8"
)

// ReadFrame returns io.EOF only for a clean detach before any header byte.
// The length is checked before allocating or reading any body bytes.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 {
		return nil, ErrProtocol
	}
	if n > MaxFrameBytes {
		return nil, ErrTooLarge
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// WriteFrame loops over short writes. A zero-progress write is an error; no
// caller can accidentally report success for an incomplete frame.
func WriteFrame(w io.Writer, body []byte) error {
	if len(body) == 0 {
		return ErrProtocol
	}
	if len(body) > MaxFrameBytes {
		return ErrTooLarge
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	for _, part := range [][]byte{header[:], body} {
		for len(part) != 0 {
			n, err := w.Write(part)
			if n < 0 || n > len(part) {
				return io.ErrShortWrite
			}
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

// ChunkText turns a complete text.attach snapshot or text.final result into
// contiguous response parts, starting at part zero and the given frame seq.
// Metadata appears only in snapshot part zero. Queue all these parts before
// live deltas; otherwise the high-water mark cannot delimit a coherent view.
// Other operations are never chunked or silently truncated.
func ChunkText(response Response, operation string) ([]Response, error) {
	if response.Part != 0 {
		return nil, ErrProtocol
	}
	var text string
	switch p := response.Result.(type) {
	case TextSnapshotPart:
		if operation != "text.attach" {
			return nil, ErrProtocol
		}
		text = p.Text
	case TextPart:
		if operation != "text.final" {
			return nil, ErrProtocol
		}
		text = p.Text
	default:
		return nil, ErrProtocol
	}
	if !utf8.ValidString(text) {
		return nil, ErrProtocol
	}
	var parts []Response
	for {
		n := min(len(text), MaxTextBytes)
		if n < len(text) {
			for !utf8.RuneStart(text[n]) {
				n--
			}
		}
		part := response
		if int64(len(parts)) > math.MaxInt64-response.Seq {
			return nil, ErrProtocol
		}
		part.Seq += int64(len(parts))
		part.Part, part.Last = int64(len(parts)), n == len(text)
		if snapshot, ok := response.Result.(TextSnapshotPart); ok && part.Part == 0 {
			snapshot.Text = text[:n]
			part.Result = snapshot
		} else {
			part.Result = TextPart{Text: text[:n]}
		}
		if _, err := Encode(part, operation); err != nil {
			return nil, err
		}
		parts = append(parts, part)
		if part.Last {
			return parts, nil
		}
		text = text[n:]
	}
}
