package terminal

import (
	"strings"
	"testing"
	"uuid"
)

// Model and tool text reach the owner's terminal; a control sequence there
// could rewrite earlier lines, retitle the window or hide a label.
func TestTerminalRender(t *testing.T) {
	t.Parallel()
	ids := lineIDs{agent: uuid.MustParse(testAgent), context: uuid.MustParse(testWork), turn: uuid.MustParse("019a3f00-0000-7000-8000-0000000b0002")}
	label := ids.label()
	if label != "[0000a001/0000c002/000b0002]" || (lineIDs{agent: ids.agent}).label() != "[0000a001/--------/--------]" {
		t.Fatal("labels lost the identities' random tails:", label)
	}
	text := "plain é漢字🙂\x1b[2J\x1b]0;title\x07\rover\u009b31m\u202edesrever\x7f\tend\xff\nsecond line\n\nfourth"
	got := render(label, "final (durable):", text)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("each text line was not labelled once: %q", got)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, label+" final (durable):") {
			t.Fatalf("unlabelled line %q", line)
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 || r == 0x202e {
				t.Fatalf("control or format character reached the terminal in %q", line)
			}
		}
	}
	want := label + " final (durable): plain é漢字🙂\\u001b[2J\\u001b]0;title\\u0007\\rover\\u009b31m\\u202edesrever\\u007f\\tend\\xff"
	if lines[0] != want || lines[1] != label+" final (durable): second line" || lines[2] != label+" final (durable):" || lines[3] != label+" final (durable): fourth" {
		t.Fatalf("printable text changed or escapes are ambiguous: %q", got)
	}
}
