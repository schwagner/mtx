package vimedit

import (
	"strings"
	"testing"
)

// state is a buffer written with '|' before the cursor's character.
func parseState(t *testing.T, s string) (string, int, int) {
	t.Helper()
	i := strings.Index(s, "|")
	if i < 0 {
		t.Fatalf("state %q has no cursor", s)
	}
	text := s[:i] + s[i+1:]
	b := &buf{t: []rune(text)}
	return text, b.lineOf(len([]rune(s[:i]))), len([]rune(s[:i])) - b.lineStart(len([]rune(s[:i])))
}

func render(text string, row, col int) string {
	b := &buf{t: []rune(text)}
	p := b.posAt(row, col)
	return string(b.t[:p]) + "|" + string(b.t[p:])
}

// keys splits vi notation: "d2w", "cwfoo<esc>", "<c-r>".
func keys(s string) []string {
	names := map[string]string{"esc": "esc", "c-r": "ctrl+r", "bs": "backspace", "space": " ", "left": "left", "del": "delete", "cr": "enter"}
	var out []string
	for s != "" {
		if s[0] == '<' {
			if j := strings.IndexByte(s, '>'); j > 0 {
				if n, ok := names[s[1:j]]; ok {
					out = append(out, n)
					s = s[j+1:]
					continue
				}
			}
		}
		r := []rune(s)
		out = append(out, string(r[0]))
		s = string(r[1:])
	}
	return out
}

// drive feeds keys to e from state, standing in for the textarea while
// the engine is in insert mode, and returns the final state and mode.
func drive(t *testing.T, e *Editor, state, ks string) (string, Mode) {
	t.Helper()
	text, row, col := parseState(t, state)
	mode := Normal
	for _, k := range keys(ks) {
		if mode == Insert {
			b := newBuf(text, row, col)
			switch k {
			case "esc":
				r := e.EndInsert(text, row, col)
				text, row, col, mode = r.Text, r.Row, r.Col, r.Mode
				continue
			case "backspace":
				if b.p > 0 {
					b.remove(b.p-1, b.p)
					b.p--
				}
			case "enter":
				b.insert(b.p, []rune{'\n'})
				b.p++
			default:
				b.insert(b.p, []rune(k))
				b.p += len([]rune(k))
			}
			text = string(b.t)
			row, col = b.rowCol(b.p)
			continue
		}
		r := e.Key(k, text, row, col)
		text, row, col, mode = r.Text, r.Row, r.Col, r.Mode
	}
	return render(text, row, col), mode
}

func TestEditing(t *testing.T) {
	cases := []struct {
		name, from, keys, want string
		mode                   Mode
	}{
		// Motions.
		{"l", "|abc", "l", "a|bc", Normal},
		{"l stops on last char", "ab|c", "l", "ab|c", Normal},
		{"count l", "|abcdef", "3l", "abc|def", Normal},
		{"h", "ab|c", "h", "a|bc", Normal},
		{"$", "|abc\ndef", "$", "ab|c\ndef", Normal},
		{"0", "ab|c", "0", "|abc", Normal},
		{"^", "  ab|c", "^", "  |abc", Normal},
		{"w", "|foo bar", "w", "foo |bar", Normal},
		{"w punctuation", "|foo.bar", "w", "foo|.bar", Normal},
		{"W", "|foo.bar baz", "W", "foo.bar |baz", Normal},
		{"w across lines", "foo |bar\nbaz", "w", "foo bar\n|baz", Normal},
		{"w stops at empty line", "|foo\n\nbar", "w", "foo\n|\nbar", Normal},
		{"b", "foo ba|r", "b", "foo |bar", Normal},
		{"b to prev word", "foo |bar", "b", "|foo bar", Normal},
		{"e", "|foo bar", "e", "fo|o bar", Normal},
		{"e next", "fo|o bar", "e", "foo ba|r", Normal},
		{"j keeps column", "ab|c\ndefg", "j", "abc\nde|fg", Normal},
		{"j clamps column", "abc|d\nef", "j", "abcd\ne|f", Normal},
		{"j remembers column", "abc|d\nef\nghij", "jj", "abcd\nef\nghi|j", Normal},
		{"k", "abc\nd|ef", "k", "a|bc\ndef", Normal},
		{"$ then j", "a|b\ncdef", "$j", "ab\ncde|f", Normal},
		{"gg", "abc\n  d|ef", "gg", "|abc\n  def", Normal},
		{"G", "a|bc\n  def", "G", "abc\n  |def", Normal},
		{"count G", "a|bc\ndef\nghi", "2G", "abc\n|def\nghi", Normal},
		{"f", "|abcabc", "fc", "ab|cabc", Normal},
		{"f count", "|abcabc", "2fc", "abcab|c", Normal},
		{"t", "|abcabc", "tc", "a|bcabc", Normal},
		{"F", "abcab|c", "Fa", "abc|abc", Normal},
		{"; repeats f", "|a.b.c", "f.;", "a.b|.c", Normal},
		{", reverses f", "|a.b.c", "f.;,", "a|.b.c", Normal},
		{"; after t moves on", "|a.b.c", "t.;", "a.|b.c", Normal},
		{"%", "|(a [b] c)", "%", "(a [b] c|)", Normal},
		{"% back", "(a [b|] c)", "%", "(a |[b] c)", Normal},
		{"}", "|a\nb\n\nc", "}", "a\nb\n|\nc", Normal},
		{"{", "a\n\nb\n|c", "{", "a\n|\nb\nc", Normal},

		// Deleting.
		{"x", "a|bc", "x", "a|c", Normal},
		{"x at end", "ab|c", "x", "a|b", Normal},
		{"count x", "|abcdef", "3x", "|def", Normal},
		{"x on empty line", "ab\n|\ncd", "x", "ab\n|\ncd", Normal},
		{"X", "ab|c", "X", "a|c", Normal},
		{"dd", "abc\nd|ef\nghi", "dd", "abc\n|ghi", Normal},
		{"dd last line", "abc\nd|ef", "dd", "|abc", Normal},
		{"dd only line", "a|bc", "dd", "|", Normal},
		{"dd to first non-blank", "a|bc\n  def", "dd", "  |def", Normal},
		{"count dd", "|a\nb\nc\nd", "2dd", "|c\nd", Normal},
		{"d count j", "|a\nb\nc\nd", "dj", "|c\nd", Normal},
		{"dk", "a\nb\n|c\nd", "dk", "a\n|d", Normal},
		{"dw", "|foo bar", "dw", "|bar", Normal},
		{"dw last word keeps newline", "foo |bar\nbaz", "dw", "foo| \nbaz", Normal},
		{"d2w", "|a b c", "d2w", "|c", Normal},
		{"2dw", "|a b c", "2dw", "|c", Normal},
		{"de", "|foo bar", "de", "| bar", Normal},
		{"db", "foo |bar", "db", "|bar", Normal},
		{"d$", "a|bc\nd", "d$", "|a\nd", Normal},
		{"D", "a|bc", "D", "|a", Normal},
		{"d0", "ab|c", "d0", "|c", Normal},
		{"dfx", "|abxcd", "dfx", "|cd", Normal},
		{"dtx", "|abxcd", "dtx", "|xcd", Normal},
		{"dG", "a\n|b\nc", "dG", "|a", Normal},
		{"dgg", "a\nb\n|c\nd", "dgg", "|d", Normal},
		{"d}", "|a\nb\n\nc", "d}", "|\nc", Normal},
		{"d%", "x|(a)y", "d%", "x|y", Normal},
		{"failed motion deletes nothing", "|abc", "dfz", "|abc", Normal},
		{"esc cancels operator", "|abc", "d<esc>x", "|bc", Normal},

		// Text objects.
		{"diw", "foo b|ar baz", "diw", "foo | baz", Normal},
		{"daw", "foo b|ar baz", "daw", "foo |baz", Normal},
		{"daw last word", "foo b|ar", "daw", "fo|o", Normal},
		{"diW", "a foo.b|ar b", "diW", "a | b", Normal},
		{"di\"", `say "he|llo" now`, `di"`, `say "|" now`, Normal},
		{"da\"", `say "he|llo" now`, `da"`, `say |now`, Normal},
		{"di\" before quotes", `|say "hi"`, `di"`, `say "|"`, Normal},
		{"di(", "f(a, |b)", "di(", "f(|)", Normal},
		{"da(", "f(a, |b)", "da(", "|f", Normal},
		{"dib nested", "(a (b) |c)", "dib", "(|)", Normal},
		{"di( from inside nested", "(a (|b) c)", "di(", "(a (|) c)", Normal},
		{"di[", "x[1|2]", "di[", "x[|]", Normal},
		{"di{ multiline", "{\n  a|b\n}", "di{", "{|}", Normal},

		// Changing.
		{"cw is ce", "|foo bar", "cwbaz<esc>", "ba|z bar", Normal},
		{"cw stays in insert", "|foo bar", "cw", "| bar", Insert},
		{"cc", "abc\n  d|ef\nghi", "ccx<esc>", "abc\n|x\nghi", Normal},
		{"C", "a|bc", "Cxy<esc>", "ax|y", Normal},
		{"s", "a|bc", "sX<esc>", "a|Xc", Normal},
		{"S", "a|bc", "Sx<esc>", "|x", Normal},
		{"ci\"", `"a|b"`, `ci"x<esc>`, `"|x"`, Normal},
		{"ct", "|foo,bar", "ct,x<esc>", "|x,bar", Normal},
		{"r", "a|bc", "rX", "a|Xc", Normal},
		{"count r", "|abc", "2rX", "X|Xc", Normal},
		{"r past end fails", "a|b", "3rX", "a|b", Normal},
		{"~", "|abc", "~~", "AB|c", Normal},
		{"gUiw", "foo b|ar", "gUiw", "foo |BAR", Normal},
		{"guu", "F|OO\nBAR", "guu", "|foo\nBAR", Normal},
		{"g~~", "a|B", "g~~", "|Ab", Normal},
		{"J", "a|b\n  cd", "J", "ab| cd", Normal},
		{"J empty next", "a|b\n\ncd", "J", "a|b\ncd", Normal},
		{"3J", "|a\nb\nc\nd", "3J", "a b| c\nd", Normal},
		{"J last line fails", "|ab", "J", "|ab", Normal},

		// Inserting.
		{"i", "a|bc", "iX<esc>", "a|Xbc", Normal},
		{"a", "a|bc", "aX<esc>", "ab|Xc", Normal},
		{"a on empty", "|", "aX<esc>", "|X", Normal},
		{"I", "  a|bc", "IX<esc>", "  |Xabc", Normal},
		{"A", "a|bc", "AX<esc>", "abc|X", Normal},
		{"o", "a|b\ncd", "oX<esc>", "ab\n|X\ncd", Normal},
		{"O", "ab\nc|d", "OX<esc>", "ab\n|X\ncd", Normal},
		{"counted insert", "|", "3ia<esc>", "aa|a", Normal},
		{"esc at line start stays", "ab\n|cd", "i<esc>", "ab\n|cd", Normal},

		// Yank and put.
		{"yy p", "|ab\ncd", "yyp", "ab\n|ab\ncd", Normal},
		{"yy P", "ab\n|cd", "yyP", "ab\n|cd\ncd", Normal},
		{"yy on last line p", "ab\n|cd", "yyp", "ab\ncd\n|cd", Normal},
		{"dd p moves line", "|ab\ncd", "ddp", "cd\n|ab", Normal},
		{"yw p", "|ab cd", "ywP", "ab| ab cd", Normal},
		{"x p swaps", "|ab", "xp", "b|a", Normal},
		{"count p", "|a", "yl3p", "aaa|a", Normal},
		{"Y yanks to end", "a|bc", "Y$p", "abcb|c", Normal},
		{"yiw keeps text", "foo b|ar", "yiw", "foo |bar", Normal},
		{"p on empty register", "|ab", "p", "|ab", Normal},

		// Undo, redo, repeat.
		{"u", "a|bc", "xu", "a|bc", Normal},
		{"u twice", "|abc", "xxuu", "|abc", Normal},
		{"ctrl-r", "a|bc", "xu<c-r>", "a|c", Normal},
		{"u insert session", "|", "ifoo<esc>u", "|", Normal},
		{"cw undoes as one step", "|foo bar", "cwbaz<esc>u", "|foo bar", Normal},
		{"new change drops redo", "|abc", "xu~<c-r>", "A|bc", Normal},
		{". repeats x", "|abcd", "x.", "|cd", Normal},
		{". repeats dd", "|a\nb\nc", "dd.", "|c", Normal},
		{". repeats cw", "|foo bar", "cwX<esc>w.", "X |X", Normal},
		{". repeats A", "|a\nb", "A;<esc>j.", "a;\nb|;", Normal},
		{". with count", "|abcdef", "x3.", "|ef", Normal},
		{". undoes in one step", "|a b c", "dw.u", "|b c", Normal},
		{". repeats o", "|a", "ox<esc>.", "a\nx\n|x", Normal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e Editor
			text, _, _ := parseState(t, c.from)
			e.Reset(text)
			got, mode := drive(t, &e, c.from, c.keys)
			if got != c.want || mode != c.mode {
				t.Errorf("%q + %q = %q (mode %d), want %q (mode %d)", c.from, c.keys, got, mode, c.want, c.mode)
			}
		})
	}
}

func TestPending(t *testing.T) {
	var e Editor
	r := e.Key("2", "abc", 0, 0)
	r = e.Key("d", r.Text, r.Row, r.Col)
	if r.Pending != "2d" || !e.Pending() {
		t.Fatalf("pending = %q, want 2d", r.Pending)
	}
	r = e.Key("q", r.Text, r.Row, r.Col)
	if r.Pending != "" || e.Pending() || r.Text != "abc" {
		t.Fatalf("invalid key left pending %q, text %q", r.Pending, r.Text)
	}
}

// An edit the engine did not make (typing after the App's own i, a
// picker, a paste) becomes one undo step.
func TestOutsideEditIsUndoable(t *testing.T) {
	var e Editor
	e.Reset("draft")
	r := e.EndInsert("draft and more", 0, 14)
	if r.Col != 13 {
		t.Fatalf("Esc cursor col = %d, want 13", r.Col)
	}
	r = e.Key("u", r.Text, r.Row, r.Col)
	if r.Text != "draft" {
		t.Fatalf("undo = %q, want draft", r.Text)
	}
	r = e.Key("u", r.Text, r.Row, r.Col)
	if r.Text != "draft" {
		t.Fatalf("undo past Reset = %q, want draft", r.Text)
	}
}

func TestResetDropsHistory(t *testing.T) {
	var e Editor
	e.Reset("abc")
	r := e.Key("x", "abc", 0, 0)
	e.Reset("new")
	r = e.Key("u", "new", 0, 0)
	if r.Text != "new" {
		t.Fatalf("undo after Reset = %q, want new", r.Text)
	}
}
