package thread

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/schwagner/mtx/internal/ui/messages"
)

func TestLinkAtFindsLinksInParentAndReplies(t *testing.T) {
	m := New()
	m.SetThread(
		messages.MessageItem{TS: "100.0", UserID: "U1", Text: "see <https://parent.example|plabel>"},
		[]messages.MessageItem{{TS: "101.0", UserID: "U2", Text: "and <https://reply.example|rlabel> ok"}},
		"C1", "100.0")
	view := m.View(20, 80)

	find := func(needle string) (x, y int) {
		for y, line := range strings.Split(ansi.Strip(view), "\n") {
			if i := strings.Index(line, needle); i >= 0 {
				return ansi.StringWidth(line[:i]), y
			}
		}
		t.Fatalf("%q not in view:\n%s", needle, ansi.Strip(view))
		return 0, 0
	}
	for _, c := range []struct{ needle, want string }{
		{"plabel", "https://parent.example"},
		{"rlabel", "https://reply.example"},
		{"ok", ""},
	} {
		x, y := find(c.needle)
		if got := m.LinkAt(y, x+1); got != c.want {
			t.Errorf("LinkAt on %q = %q, want %q", c.needle, got, c.want)
		}
	}
}
