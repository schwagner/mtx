package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/schwagner/mtx/internal/ui/messages"
)

// screenPos finds the first cell showing needle in the rendered frame.
func screenPos(t *testing.T, a *App, needle string) (x, y int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(a.View().Content), "\n") {
		if i := strings.Index(line, needle); i >= 0 {
			return ansi.StringWidth(line[:i]), y
		}
	}
	t.Fatalf("%q not on screen", needle)
	return 0, 0
}

func clickAt(a *App, x, y int) tea.Cmd {
	_, _ = a.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, cmd := a.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return cmd
}

func TestMouseClickOnLinkOpensIt(t *testing.T) {
	a := newTestApp(t, withWindowSize(120, 30), withMessages(messages.MessageItem{
		TS: "1.0", UserID: "U1", UserName: "alice", Timestamp: "1:00 PM",
		Text: "go <https://example.com/x|thelabel> or <https://bare.example> plaintext",
	}))

	for _, c := range []struct{ needle, want string }{
		{"thelabel", "https://example.com/x"},
		{"bare.example", "https://bare.example"},
	} {
		x, y := screenPos(t, a, c.needle)
		var got string
		for _, m := range drainCmd(clickAt(a, x+2, y)) {
			if o, ok := m.(OpenLinkMsg); ok {
				got = o.URL
			}
		}
		if got != c.want {
			t.Errorf("click on %q opened %q, want %q", c.needle, got, c.want)
		}
	}

	x, y := screenPos(t, a, "plaintext")
	for _, m := range drainCmd(clickAt(a, x+2, y)) {
		if o, ok := m.(OpenLinkMsg); ok {
			t.Errorf("click on plain text opened %q", o.URL)
		}
	}
}
