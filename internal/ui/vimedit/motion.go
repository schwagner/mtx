package vimedit

import "unicode"

type motionKind int

const (
	exclusive motionKind = iota
	inclusive
	linewiseMotion
)

// move runs a plain motion: the cursor goes to its target.
func (e *Editor) move(b *buf, c command) {
	target, _, ok := e.motion(b, c.name, c.arg, c.n(), c.hasCount(), false)
	if !ok {
		return
	}
	switch c.name {
	case "j", "k", "up", "down":
		// keeps want
	case "$", "end":
		e.want, e.wantSet = -1, true
	default:
		e.wantSet = false
	}
	b.p = target
	b.clamp()
}

// motion returns the target of motion name from the cursor. forOp is
// set when an operator applies it, which changes l, $ and w at the end
// of a line the way vi does.
func (e *Editor) motion(b *buf, name string, arg rune, n int, hasCount, forOp bool) (int, motionKind, bool) {
	p := b.p
	ls, le := b.lineStart(p), b.lineEnd(p)
	switch name {
	case "h", "left", "backspace":
		if p == ls {
			return 0, 0, false
		}
		return max(ls, p-n), exclusive, true
	case "l", "right", " ":
		limit := le
		if !forOp {
			limit = max(ls, le-1)
		}
		if p >= limit {
			return 0, 0, false
		}
		return min(p+n, limit), exclusive, true
	case "0", "home":
		return ls, exclusive, true
	case "^":
		return b.firstNonBlank(p), exclusive, true
	case "$", "end":
		line := min(b.lineOf(p)+n-1, b.lines()-1)
		return b.lineEnd(b.posOfLine(line)), exclusive, true
	case "j", "down", "k", "up":
		line := b.lineOf(p)
		to := line + n
		if name == "k" || name == "up" {
			to = line - n
		}
		to = max(0, min(to, b.lines()-1))
		if to == line {
			return 0, 0, false
		}
		if !e.wantSet {
			_, e.want = b.rowCol(p)
			e.wantSet = true
		}
		col := e.want
		if col < 0 {
			col = 1 << 30
		}
		return b.posAt(to, col), linewiseMotion, true
	case "gg", "G":
		line := 0
		if name == "G" {
			line = b.lines() - 1
		}
		if hasCount {
			line = min(n, b.lines()) - 1
		}
		return b.firstNonBlank(b.posOfLine(line)), linewiseMotion, true
	case "w", "W", "e", "E", "b", "B":
		big := name == "W" || name == "E" || name == "B"
		q := p
		for i := 0; i < n; i++ {
			var next int
			switch name {
			case "w", "W":
				next = b.wordForward(q, big)
				// The last word an operator moves over ends at the end
				// of its line, not at the next line's first word.
				if forOp && i == n-1 && b.lineOf(next) > b.lineOf(q) && b.lineEnd(q) > q {
					next = b.lineEnd(q)
				}
			case "e", "E":
				next = b.wordEnd(q, big)
			default:
				next = b.wordBackward(q, big)
			}
			if next == q {
				break
			}
			q = next
		}
		if q == p {
			return 0, 0, false
		}
		if name == "e" || name == "E" {
			return q, inclusive, true
		}
		return q, exclusive, true
	case "}", "{":
		q := p
		for i := 0; i < n; i++ {
			if name == "}" {
				q = b.paragraphForward(q)
			} else {
				q = b.paragraphBackward(q)
			}
		}
		if q == p {
			return 0, 0, false
		}
		return q, exclusive, true
	case "%":
		q, ok := b.matchBracket(p)
		return q, inclusive, ok
	case "f", "F", "t", "T":
		e.findCmd, e.findCh = name, arg
		q, ok := b.find(p, name, arg, n, false)
		return q, findKind(name), ok
	case ";", ",":
		if e.findCmd == "" {
			return 0, 0, false
		}
		cmd := e.findCmd
		if name == "," {
			cmd = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[cmd]
		}
		q, ok := b.find(p, cmd, e.findCh, n, true)
		return q, findKind(cmd), ok
	}
	return 0, 0, false
}

func findKind(cmd string) motionKind {
	if cmd == "f" || cmd == "t" {
		return inclusive
	}
	return exclusive
}

// --- the buffer ---

// buf is the text as runes with the cursor as a rune offset.
type buf struct {
	t []rune
	p int
}

func newBuf(text string, row, col int) *buf {
	b := &buf{t: []rune(text)}
	b.p = b.posAt(row, col)
	return b
}

func (b *buf) lineStart(p int) int {
	for p > 0 && b.t[p-1] != '\n' {
		p--
	}
	return p
}

// lineEnd is the offset of p's line's newline, or len at the last line.
func (b *buf) lineEnd(p int) int {
	for p < len(b.t) && b.t[p] != '\n' {
		p++
	}
	return p
}

func (b *buf) lineOf(p int) int {
	n := 0
	for i := 0; i < p && i < len(b.t); i++ {
		if b.t[i] == '\n' {
			n++
		}
	}
	return n
}

func (b *buf) lines() int { return b.lineOf(len(b.t)) + 1 }

func (b *buf) posOfLine(line int) int {
	p := 0
	for n := 0; n < line && p < len(b.t); p++ {
		if b.t[p] == '\n' {
			n++
			if n == line {
				return p + 1
			}
		}
	}
	return p
}

// posAt is the offset of (row, col), col clamped to the line.
func (b *buf) posAt(row, col int) int {
	s := b.posOfLine(max(0, row))
	return s + max(0, min(col, b.lineEnd(s)-s))
}

func (b *buf) rowCol(p int) (int, int) {
	return b.lineOf(p), p - b.lineStart(p)
}

func (b *buf) firstNonBlank(p int) int {
	p = b.lineStart(p)
	for p < len(b.t) && (b.t[p] == ' ' || b.t[p] == '\t') {
		p++
	}
	return p
}

// clamp keeps the cursor on a character, as normal mode does: never
// past the last character of its line.
func (b *buf) clamp() {
	b.p = max(0, min(b.p, len(b.t)))
	ls, le := b.lineStart(b.p), b.lineEnd(b.p)
	if b.p >= le && le > ls {
		b.p = le - 1
	}
}

func (b *buf) insert(at int, r []rune) {
	b.t = append(b.t[:at], append(append([]rune(nil), r...), b.t[at:]...)...)
}

func (b *buf) remove(s, e int) {
	b.t = append(b.t[:s], b.t[e:]...)
}

// deleteLines removes the whole lines spanning [s, e) (s a line start,
// e a line end) and leaves the cursor on the line that takes their place.
func (b *buf) deleteLines(s, e int) {
	switch {
	case e < len(b.t): // take the trailing newline
		b.remove(s, e+1)
		b.p = s
	case s > 0: // last lines: take the newline before them
		b.remove(s-1, e)
		b.p = b.lineStart(s - 1)
	default:
		b.remove(s, e)
		b.p = 0
	}
}

// join is J: n joins starting at the cursor's line.
func (b *buf) join(n int) bool {
	if b.lineOf(b.p) == b.lines()-1 {
		return false
	}
	for i := 0; i < n; i++ {
		le := b.lineEnd(b.p)
		if le == len(b.t) {
			break
		}
		ws := le + 1
		we := ws
		for we < len(b.t) && (b.t[we] == ' ' || b.t[we] == '\t') {
			we++
		}
		var sep []rune
		ls := b.lineStart(le)
		nextEmpty := we == len(b.t) || b.t[we] == '\n'
		if !nextEmpty && b.t[we] != ')' && le > ls && b.t[le-1] != ' ' && b.t[le-1] != '\t' {
			sep = []rune{' '}
		}
		b.remove(le, we)
		b.insert(le, sep)
		b.p = le
		if len(sep) == 0 && le > ls {
			b.p = le - 1
		}
	}
	return true
}

// --- words, paragraphs, brackets, finds ---

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }

// class sorts runes into vi's word classes: 0 blank, 1 punctuation,
// 2 word characters. A big word (W) is any run of non-blanks.
func class(r rune, big bool) int {
	switch {
	case isSpace(r):
		return 0
	case big:
		return 1
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 2
	default:
		return 1
	}
}

// emptyLineAt reports whether p is the start of an empty line.
func (b *buf) emptyLineAt(p int) bool {
	return (p == len(b.t) || b.t[p] == '\n') && (p == 0 || b.t[p-1] == '\n')
}

func (b *buf) wordForward(p int, big bool) int {
	n := len(b.t)
	if p >= n {
		return p
	}
	if c := class(b.t[p], big); c != 0 {
		for p < n && class(b.t[p], big) == c {
			p++
		}
	}
	for p < n && isSpace(b.t[p]) {
		if b.t[p] == '\n' {
			p++
			if b.emptyLineAt(p) && p < n {
				return p
			}
			continue
		}
		p++
	}
	return p
}

func (b *buf) wordEnd(p int, big bool) int {
	n := len(b.t)
	if p+1 >= n {
		return p
	}
	p++
	for p < n-1 && isSpace(b.t[p]) {
		p++
	}
	c := class(b.t[p], big)
	for p+1 < n && class(b.t[p+1], big) == c && c != 0 {
		p++
	}
	return p
}

func (b *buf) wordBackward(p int, big bool) int {
	if p == 0 {
		return 0
	}
	p--
	for p > 0 && isSpace(b.t[p]) {
		if b.emptyLineAt(p) {
			return p
		}
		p--
	}
	c := class(b.t[p], big)
	for p > 0 && class(b.t[p-1], big) == c && c != 0 {
		p--
	}
	return p
}

func (b *buf) paragraphForward(p int) int {
	p = b.lineEnd(p)
	for p < len(b.t) && b.emptyLineAt(p+1) {
		p = b.lineEnd(p + 1)
	}
	for p < len(b.t) {
		p++
		if b.emptyLineAt(p) {
			return p
		}
		p = b.lineEnd(p)
	}
	return len(b.t)
}

func (b *buf) paragraphBackward(p int) int {
	p = b.lineStart(p)
	for p > 0 && b.emptyLineAt(b.lineStart(p-1)) {
		p = b.lineStart(p - 1)
	}
	for p > 0 {
		p = b.lineStart(p - 1)
		if b.emptyLineAt(p) {
			return p
		}
	}
	return 0
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}

// matchBracket is %: the partner of the first bracket at or after p on
// its line.
func (b *buf) matchBracket(p int) (int, bool) {
	le := b.lineEnd(p)
	for ; p < le; p++ {
		if _, ok := pairs[b.t[p]]; ok {
			break
		}
	}
	if p == le {
		return 0, false
	}
	open := b.t[p]
	close := pairs[open]
	step := 1
	if open == ')' || open == ']' || open == '}' {
		step = -1
	}
	depth := 0
	for q := p; q >= 0 && q < len(b.t); q += step {
		switch b.t[q] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return q, true
			}
		}
	}
	return 0, false
}

// find is f/F/t/T within the line. again is set for ; and , so a
// repeated t does not stick on the character it stopped before.
func (b *buf) find(p int, cmd string, ch rune, n int, again bool) (int, bool) {
	ls, le := b.lineStart(p), b.lineEnd(p)
	q := p
	if again && (cmd == "t" || cmd == "T") {
		if cmd == "t" {
			q++
		} else {
			q--
		}
	}
	for i := 0; i < n; i++ {
		if cmd == "f" || cmd == "t" {
			q++
			for q < le && b.t[q] != ch {
				q++
			}
			if q >= le {
				return 0, false
			}
		} else {
			q--
			for q >= ls && b.t[q] != ch {
				q--
			}
			if q < ls {
				return 0, false
			}
		}
	}
	switch cmd {
	case "t":
		q--
	case "T":
		q++
	}
	if q == p {
		return 0, false
	}
	return q, true
}

// --- text objects ---

// object returns the [s, e) range of text object obj ("iw", "a(") at
// the cursor.
func (b *buf) object(obj string) (int, int, bool) {
	inner := obj[0] == 'i'
	switch k := rune(obj[1]); k {
	case 'w', 'W':
		return b.wordObject(inner, k == 'W')
	case '"', '\'', '`':
		return b.quoteObject(inner, k)
	default:
		open := map[rune]rune{'(': '(', ')': '(', 'b': '(', '[': '[', ']': '[', '{': '{', '}': '{', 'B': '{', '<': '<', '>': '<'}[k]
		return b.bracketObject(inner, open)
	}
}

func (b *buf) wordObject(inner, big bool) (int, int, bool) {
	p := b.p
	ls, le := b.lineStart(p), b.lineEnd(p)
	if p == le {
		return 0, 0, false
	}
	blank := func(r rune) bool { return r == ' ' || r == '\t' }
	same := func(q int) bool {
		if blank(b.t[p]) {
			return blank(b.t[q])
		}
		return !blank(b.t[q]) && class(b.t[q], big) == class(b.t[p], big)
	}
	s, e := p, p+1
	for s > ls && same(s-1) {
		s--
	}
	for e < le && same(e) {
		e++
	}
	if inner {
		return s, e, true
	}
	if blank(b.t[p]) { // aw on blanks: the blanks and the word after
		if e < le {
			c := class(b.t[e], big)
			for e < le && !blank(b.t[e]) && class(b.t[e], big) == c {
				e++
			}
		}
		return s, e, true
	}
	if e < le && blank(b.t[e]) {
		for e < le && blank(b.t[e]) {
			e++
		}
	} else {
		for s > ls && blank(b.t[s-1]) {
			s--
		}
	}
	return s, e, true
}

func (b *buf) quoteObject(inner bool, q rune) (int, int, bool) {
	ls, le := b.lineStart(b.p), b.lineEnd(b.p)
	var at []int
	for i := ls; i < le; i++ {
		if b.t[i] == q && (i == ls || b.t[i-1] != '\\') {
			at = append(at, i)
		}
	}
	for i := 0; i+1 < len(at); i += 2 {
		s, e := at[i], at[i+1]
		if b.p > e && i+3 < len(at) {
			continue
		}
		if b.p > e {
			break
		}
		if inner {
			return s + 1, e, true
		}
		e++
		if e < le && (b.t[e] == ' ' || b.t[e] == '\t') {
			for e < le && (b.t[e] == ' ' || b.t[e] == '\t') {
				e++
			}
		} else {
			for s > ls && (b.t[s-1] == ' ' || b.t[s-1] == '\t') {
				s--
			}
		}
		return s, e, true
	}
	return 0, 0, false
}

func (b *buf) bracketObject(inner bool, open rune) (int, int, bool) {
	close := map[rune]rune{'(': ')', '[': ']', '{': '}', '<': '>'}[open]
	// The enclosing open bracket: scan back, skipping nested pairs.
	o := -1
	depth := 0
	start := b.p
	if start < len(b.t) && b.t[start] == close {
		depth = 1
		start--
	} else if start < len(b.t) && b.t[start] == open {
		o = start
	}
	for q := start; o < 0 && q >= 0; q-- {
		switch b.t[q] {
		case close:
			depth++
		case open:
			if depth == 0 {
				o = q
			} else {
				depth--
			}
		}
	}
	if o < 0 {
		return 0, 0, false
	}
	depth = 0
	for q := o; q < len(b.t); q++ {
		switch b.t[q] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				if inner {
					return o + 1, q, true
				}
				return o, q + 1, true
			}
		}
	}
	return 0, 0, false
}
