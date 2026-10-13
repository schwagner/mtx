package compose

import (
	"strings"
	"testing"
)

// The textarea steps down by wrapped rows; setCursor must still land
// on the logical row and column the engine asked for.
func TestVimCursorAcrossWrappedLines(t *testing.T) {
	m := New("general")
	m.SetWidth(20) // wraps at 15 columns
	long := strings.Repeat("word ", 10)
	m.SetValue(long + "\nshort\n" + long)
	m.setCursor(0, 0)

	r := m.VimKey("j")
	if m.input.Line() != 1 || m.input.Column() != 0 || r.Row != 1 {
		t.Fatalf("j: cursor (%d,%d), want (1,0)", m.input.Line(), m.input.Column())
	}
	m.VimKey("j")
	m.VimKey("$")
	if m.input.Line() != 2 || m.input.Column() != len(long)-1 {
		t.Fatalf("j$: cursor (%d,%d), want (2,%d)", m.input.Line(), m.input.Column(), len(long)-1)
	}
	m.VimKey("d")
	m.VimKey("d")
	if got := m.Value(); got != long+"\nshort" {
		t.Fatalf("dd: %q", got)
	}
	if m.input.Line() != 1 {
		t.Fatalf("dd: cursor row %d, want 1", m.input.Line())
	}
}

func TestVimHistoryResetOnSend(t *testing.T) {
	m := New("general")
	_ = m.Focus()
	m.SetValue("sent")
	m.VimKey("x")
	m.Reset()
	m.VimKey("u")
	if got := m.Value(); got != "" {
		t.Fatalf("u after Reset restored %q; a sent message must not come back", got)
	}
}
