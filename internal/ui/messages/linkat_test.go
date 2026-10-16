package messages

import "testing"

func TestLinkAt(t *testing.T) {
	link := osc8Hyperlink("https://example.com", "\x1b[4mexample\x1b[0m")
	lines := []string{
		"▌see " + link + " now",
		"\x1b]8;;https://wrapped.example\x1b\\first half",
		"second half\x1b]8;;\x1b\\ tail",
		"😀 " + osc8Hyperlink("https://emoji.example", "x"),
	}
	cases := []struct {
		name     string
		row, col int
		want     string
	}{
		{"before link", 0, 3, ""},
		{"first link cell", 0, 5, "https://example.com"},
		{"last link cell", 0, 11, "https://example.com"},
		{"after link", 0, 12, ""},
		{"past end of line", 0, 40, ""},
		{"opened on previous line", 2, 0, "https://wrapped.example"},
		{"after close on continued line", 2, 12, ""},
		{"wide emoji shifts columns", 3, 3, "https://emoji.example"},
		{"on the emoji", 3, 1, ""},
		{"row out of range", 9, 0, ""},
	}
	for _, c := range cases {
		if got := LinkAt(lines, c.row, c.col); got != c.want {
			t.Errorf("%s: LinkAt(%d, %d) = %q, want %q", c.name, c.row, c.col, got, c.want)
		}
	}
}
