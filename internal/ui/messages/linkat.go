package messages

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// LinkAt returns the target of the OSC 8 hyperlink drawn at display
// column col of lines[row], or "" when that cell is not inside one.
// Hyperlink state carries across lines, so a link the word-wrapper split
// over two lines without re-opening it still hits on the second line.
// Columns count grapheme widths, the same model as PlainLines.
func LinkAt(lines []string, row, col int) string {
	if row < 0 || row >= len(lines) || col < 0 {
		return ""
	}
	var url string
	for i := 0; i <= row; i++ {
		line := lines[i]
		x := 0
		var state byte
		for len(line) > 0 {
			seq, width, n, newState := ansi.DecodeSequence(line, state, nil)
			state = newState
			line = line[n:]
			if width > 0 {
				if i == row && col < x+width {
					return url
				}
				x += width
				continue
			}
			if target, ok := osc8Target(seq); ok {
				url = target
			}
		}
	}
	return ""
}

// osc8Target parses an OSC 8 sequence (ESC ] 8 ; params ; URI ST) and
// returns its URI, which is "" for the sequence that closes a link.
func osc8Target(seq string) (string, bool) {
	rest, ok := strings.CutPrefix(seq, "\x1b]8;")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "\x1b\\"), "\x07")
	_, uri, ok := strings.Cut(rest, ";")
	return uri, ok
}
