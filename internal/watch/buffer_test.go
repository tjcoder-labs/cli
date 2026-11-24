package watch

import (
	"fmt"
	"strings"
	"testing"
)

func TestBufferCapKeepsNewestLines(t *testing.T) {
	b := NewBuffer(100)
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(b, "line %d\n", i)
	}
	lines := b.Lines()
	if len(lines) != 100 {
		t.Fatalf("len = %d, want 100", len(lines))
	}
	if lines[0] != "line 900" || lines[99] != "line 999" {
		t.Fatalf("kept %q..%q", lines[0], lines[99])
	}
	if b.Dropped() != 900 {
		t.Fatalf("dropped = %d", b.Dropped())
	}
	fmt.Fprint(b, "partial")
	if got := b.Lines(); len(got) != 100 || got[99] != "partial" {
		t.Fatalf("partial line not last within cap: %d %q", len(got), got[len(got)-1])
	}
}

func TestBufferTranslatesANSI(t *testing.T) {
	b := NewBuffer(0)
	fmt.Fprint(b, "\x1b[31mred\x1b[0m plain\n\x1b[1;38;5;208mbold orange\x1b[0m\n\x1b[38;2;1;2;3mrgb\x1b[39m\n")
	lines := b.Lines()
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.Contains(lines[0], "[maroon") || !strings.Contains(lines[0], "red[-:-:-] plain") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "b]bold orange") {
		t.Errorf("line 1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], "#010203") {
		t.Errorf("line 2 = %q", lines[2])
	}
	for _, l := range lines {
		if strings.ContainsRune(l, 0x1b) {
			t.Errorf("raw escape left in %q", l)
		}
	}
}

func TestBufferEscapesTagLikeText(t *testing.T) {
	b := NewBuffer(0)
	fmt.Fprint(b, "[red]not a tag[-] [::b]\n")
	got := b.Lines()[0]
	if got != `[red[]not a tag[-[] [::b[]` {
		t.Fatalf("escaped = %q", got)
	}
}

func TestBufferDropsNonSGRAndHandlesCR(t *testing.T) {
	b := NewBuffer(0)
	// Cursor movement, erase-line, an OSC title, BEL, CR progress.
	fmt.Fprint(b, "\x1b]0;title\x07\x1b[2Kstart\x1b[1A\n 10%\r 50%\r\x1b[K100%\ndone\r\n")
	lines := b.Lines()
	want := []string{"start", "100%", "done"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestBufferSplitWritesAndColourCarry(t *testing.T) {
	b := NewBuffer(0)
	for _, chunk := range []string{"\x1b[3", "2mgr", "een\nstill\x1b", "[0m\nh\xc3", "\xa9\n"} {
		b.Write([]byte(chunk))
	}
	lines := b.Lines()
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.Contains(lines[0], "green") || !strings.HasPrefix(lines[1], "[green") {
		t.Errorf("colour should carry onto the next line: %q", lines)
	}
	if !strings.HasSuffix(lines[2], "hé") {
		t.Errorf("split UTF-8 not reassembled: %q", lines[2])
	}
}

func TestBufferReplaceAndNote(t *testing.T) {
	a, b := NewBuffer(3), NewBuffer(10)
	fmt.Fprint(b, "1\n2\n3\n4\n5\n")
	v := a.Version()
	a.Replace(b)
	if got := strings.Join(a.Lines(), ","); got != "3,4,5" || a.Version() == v {
		t.Fatalf("replace = %q", got)
	}
	a.AppendNote("[gray]", "exit [0]")
	if got := a.Lines(); got[2] != "[gray]exit [0[][-:-:-]" {
		t.Fatalf("note = %q", got)
	}
	a.Reset()
	if a.Len() != 0 {
		t.Fatal("reset left lines")
	}
}
