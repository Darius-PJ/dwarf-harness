package terminal

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// lineIDs are a displayed line's bound identities, always taken from the
// event, turn or command the line reports, never from the current selection.
type lineIDs struct{ agent, context, turn uuid.UUID }

// short is an identity's last eight hexadecimal digits: the random tail of a
// UUIDv7, unlike its millisecond prefix.
func short(id uuid.UUID) string {
	if id == (uuid.UUID{}) {
		return "--------"
	}
	s := id.String()
	return s[len(s)-8:]
}

func (ids lineIDs) label() string {
	return "[" + short(ids.agent) + "/" + short(ids.context) + "/" + short(ids.turn) + "]"
}

// render labels every line of text and escapes everything a terminal could
// interpret: C0 and C1 controls (ESC starts ANSI and OSC sequences), DEL,
// carriage return, format characters such as bidirectional overrides, and
// invalid UTF-8. Printable Unicode and line breaks are kept.
func render(label, kind, text string) string {
	var out strings.Builder
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		out.WriteString(label)
		out.WriteString(" ")
		out.WriteString(kind)
		if line != "" {
			out.WriteString(" ")
			out.WriteString(escape(line))
		}
		out.WriteString("\n")
	}
	return out.String()
}

func escape(s string) string {
	var out strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, `\x%02x`, s[0])
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\r':
			out.WriteString(`\r`)
		case unicode.IsPrint(r):
			out.WriteString(s[:size])
		case r <= 0xffff:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			fmt.Fprintf(&out, `\U%08x`, r)
		}
		s = s[size:]
	}
	return out.String()
}
