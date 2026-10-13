// Package vimedit is the vi-style modal editing engine behind the compose
// box's normal mode (compose.vim_mode).
//
// It is pure: every entry point takes the buffer's text and cursor
// (row and column in runes, as bubbles' textarea reports them) and
// returns the new text, cursor and mode. It never holds the buffer
// itself, so whatever the textarea did in insert mode — typing, pickers,
// paste — is simply what the next call sees. The Editor keeps only the
// state vi keeps beside a buffer: the pending keys of an incomplete
// command, the unnamed register, undo/redo, the last f/t search and the
// last change for '.'.
//
// Commands follow vi's grammar, [count] operator [count] motion, so
// "dd" is the d operator on the current line and "d3w", "c$", "yi("
// and "gUiw" fall out of the same path.
package vimedit

import (
	"strings"
	"unicode"
)

// Mode is the editing mode a call leaves the buffer in.
type Mode int

const (
	Normal Mode = iota
	Insert
)

// Result is the buffer after a call.
type Result struct {
	Text     string
	Row, Col int
	Mode     Mode
	// Pending holds the keys of an incomplete command ("d", "2f").
	Pending string
}

type snapshot struct {
	text string
	pos  int
}

type register struct {
	text     string
	linewise bool
}

// command is one parsed vi command.
type command struct {
	count  int    // leading count; 0 when absent
	op     string // operator: d c y gu gU g~; "" for a plain motion or command
	count2 int    // count between operator and motion
	name   string // motion, text object ("iw"), command, or "_" for a doubled operator
	arg    rune   // character argument of f t F T r
}

// n is the effective count (both counts multiply, as in vi).
func (c command) n() int { return max(1, c.count) * max(1, c.count2) }

func (c command) hasCount() bool { return c.count > 0 || c.count2 > 0 }

type change struct {
	cmd  command
	text string // what was typed in the insert the command opened
}

// Editor is one composer's vi state. The zero value is ready to use.
type Editor struct {
	pending   []string
	reg       register
	undo      []snapshot
	redo      []snapshot
	committed snapshot // the buffer as of the engine's last answer
	want      int      // column j/k aim for; -1 = end of line
	wantSet   bool
	findCmd   string // last f/t/F/T, for ; and ,
	findCh    rune
	last      *change // last change, for '.'

	// The insert opened by an engine command, finished by EndInsert.
	inserting   *change
	insertBase  []rune
	insertAt    int
	insertCount int
}

// Reset starts a fresh buffer holding text: history, pending keys and
// the in-progress insert are dropped; the register and '.' survive, as
// they survive switching buffers in vi.
func (e *Editor) Reset(text string) {
	e.pending = nil
	e.undo, e.redo = nil, nil
	e.committed = snapshot{text: text}
	e.wantSet = false
	e.inserting = nil
}

// Pending reports whether a command is partly typed.
func (e *Editor) Pending() bool { return len(e.pending) > 0 }

// Key feeds one normal-mode key. key is the typed character ("d", "$",
// " ") or a key name ("esc", "ctrl+r", "left", "backspace").
func (e *Editor) Key(key, text string, row, col int) Result {
	b := newBuf(text, row, col)
	e.sync(b)
	e.pending = append(e.pending, key)
	c, st := parse(e.pending)
	switch st {
	case more:
		return e.result(b, Normal)
	case invalid:
		e.pending = nil
		b.clamp()
		return e.result(b, Normal)
	}
	e.pending = nil
	mode := e.run(b, c, false)
	return e.result(b, mode)
}

// EndInsert leaves insert mode (Esc): it records what was typed for
// '.', repeats it for a counted insert ("3ihi<Esc>"), makes the insert
// one undo step and steps the cursor back onto the last typed char.
func (e *Editor) EndInsert(text string, row, col int) Result {
	b := newBuf(text, row, col)
	if ch := e.inserting; ch != nil {
		e.inserting = nil
		if typed, ok := insertedText(e.insertBase, b.t, e.insertAt); ok {
			ch.text = string(typed)
			for i := 1; i < e.insertCount; i++ {
				b.insert(b.p, typed)
				b.p += len(typed)
			}
			e.last = ch
		}
	}
	e.sync(b)
	if b.p > b.lineStart(b.p) {
		b.p--
	}
	b.clamp()
	e.wantSet = false
	e.committed = snapshot{string(b.t), b.p}
	return e.result(b, Normal)
}

// sync turns any edit made outside the engine since its last answer
// (an insert session, a picker, a paste) into one undo step.
func (e *Editor) sync(b *buf) {
	if string(b.t) != e.committed.text {
		e.pushUndo(e.committed)
		e.committed = snapshot{string(b.t), b.p}
	}
}

func (e *Editor) pushUndo(s snapshot) {
	e.undo = append(e.undo, s)
	e.redo = nil
}

func (e *Editor) result(b *buf, mode Mode) Result {
	if mode == Normal {
		e.committed = snapshot{string(b.t), b.p}
	}
	row, col := b.rowCol(b.p)
	return Result{
		Text:    string(b.t),
		Row:     row,
		Col:     col,
		Mode:    mode,
		Pending: strings.Join(e.pending, ""),
	}
}

// --- parsing ---

type status int

const (
	done status = iota
	more
	invalid
)

var (
	operators = map[string]bool{"d": true, "c": true, "y": true, "gu": true, "gU": true, "g~": true}
	// motions usable alone or after an operator.
	motions = map[string]bool{
		"h": true, "j": true, "k": true, "l": true, "left": true, "right": true, "up": true, "down": true,
		" ": true, "backspace": true, "0": true, "home": true, "^": true, "$": true, "end": true,
		"w": true, "W": true, "b": true, "B": true, "e": true, "E": true,
		"gg": true, "G": true, "{": true, "}": true, "%": true, ";": true, ",": true,
	}
	charMotions = map[string]bool{"f": true, "F": true, "t": true, "T": true}
	// commands that stand alone.
	commands = map[string]bool{
		"i": true, "a": true, "I": true, "A": true, "o": true, "O": true,
		"x": true, "delete": true, "X": true, "D": true, "C": true, "s": true, "S": true, "Y": true,
		"p": true, "P": true, "J": true, "~": true, "u": true, "ctrl+r": true, ".": true,
	}
)

// parse reads one command from toks.
func parse(toks []string) (command, status) {
	var c command
	i := 0
	c.count, i = parseCount(toks, i)
	tok, i, st := word(toks, i)
	if st != done {
		return c, st
	}
	switch {
	case operators[tok]:
		c.op = tok
		c.count2, i = parseCount(toks, i)
		m, j, st := word(toks, i)
		if st != done {
			return c, st
		}
		i = j
		switch {
		case m == tok || (len(tok) == 2 && m == tok[1:]): // dd, guu, gugu
			c.name = "_"
		case m == "i" || m == "a":
			if i == len(toks) {
				return c, more
			}
			obj := m + toks[i]
			i++
			if !textObject(obj) {
				return c, invalid
			}
			c.name = obj
		case charMotions[m]:
			if i == len(toks) {
				return c, more
			}
			c.name, c.arg = m, oneRune(toks[i])
			i++
			if c.arg == 0 {
				return c, invalid
			}
		case motions[m]:
			c.name = m
		default:
			return c, invalid
		}
	case charMotions[tok] || tok == "r":
		if i == len(toks) {
			return c, more
		}
		c.name, c.arg = tok, oneRune(toks[i])
		i++
		if c.arg == 0 {
			return c, invalid
		}
	case motions[tok] || commands[tok]:
		c.name = tok
	default:
		return c, invalid
	}
	if i != len(toks) {
		return c, invalid
	}
	return c, done
}

// word reads one key, joining a g prefix with its second key.
func word(toks []string, i int) (string, int, status) {
	if i == len(toks) {
		return "", i, more
	}
	t := toks[i]
	i++
	if t == "g" {
		if i == len(toks) {
			return "", i, more
		}
		t += toks[i]
		i++
	}
	return t, i, done
}

// parseCount reads a count; a leading 0 is the motion, not a count.
func parseCount(toks []string, i int) (int, int) {
	n := 0
	for i < len(toks) && len(toks[i]) == 1 && toks[i][0] >= '0' && toks[i][0] <= '9' {
		if n == 0 && toks[i] == "0" {
			break
		}
		n = min(n*10+int(toks[i][0]-'0'), 99999)
		i++
	}
	return n, i
}

func oneRune(s string) rune {
	r := []rune(s)
	if len(r) != 1 {
		return 0
	}
	return r[0]
}

func textObject(s string) bool {
	if len(s) != 2 {
		return false
	}
	return strings.ContainsRune("wW\"'`()b[]{}B<>", rune(s[1]))
}

// --- execution ---

// run executes c on b and returns the mode it leaves. replay is set
// when '.' runs it, so it does not become the new last change.
func (e *Editor) run(b *buf, c command, replay bool) Mode {
	pre := snapshot{string(b.t), b.p}
	switch c.name {
	case "u":
		e.undoStep(b, c.n())
		return Normal
	case "ctrl+r":
		e.redoStep(b, c.n())
		return Normal
	case ".":
		return e.repeat(b, c)
	}
	if c.op == "" && !commands[c.name] && c.name != "r" { // a plain motion
		e.move(b, c)
		return Normal
	}
	// Commands that are an operator in disguise.
	switch c.name {
	case "x", "delete":
		c.op, c.name = "d", "l"
	case "X":
		c.op, c.name = "d", "h"
	case "D":
		c.op, c.name = "d", "$"
	case "C":
		c.op, c.name = "c", "$"
	case "s":
		c.op, c.name = "c", "l"
	case "S":
		c.op, c.name = "c", "_"
	case "Y":
		c.op, c.name = "y", "$"
	}
	mode, changed := e.do(b, c)
	e.wantSet = false
	if c.op == "y" || !changed && mode == Normal {
		return mode
	}
	if !replay {
		ch := &change{cmd: c}
		if mode == Insert {
			e.inserting = ch
		} else {
			e.last = ch
		}
	}
	if mode == Insert {
		// EndInsert pushes the undo step, covering the command and
		// what is typed after it ("cw" + text undoes as one).
		e.committed = pre
		e.insertBase = append([]rune(nil), b.t...)
		e.insertAt = b.p
		e.insertCount = 1
		if strings.Contains("iaIA", c.name) && len(c.name) == 1 {
			e.insertCount = c.n()
		}
	} else if string(b.t) != pre.text {
		e.pushUndo(pre)
	}
	return mode
}

// do performs a change command or operator; changed is false when it
// failed (vi would beep) or had nothing to act on.
func (e *Editor) do(b *buf, c command) (Mode, bool) {
	n := c.n()
	ls, le := b.lineStart(b.p), b.lineEnd(b.p)
	switch c.name {
	case "i":
		return Insert, true
	case "a":
		if b.p < le {
			b.p++
		}
		return Insert, true
	case "I":
		b.p = b.firstNonBlank(b.p)
		return Insert, true
	case "A":
		b.p = le
		return Insert, true
	case "o":
		b.insert(le, []rune{'\n'})
		b.p = le + 1
		return Insert, true
	case "O":
		b.insert(ls, []rune{'\n'})
		b.p = ls
		return Insert, true
	case "p", "P":
		return Normal, e.put(b, c.name == "p", n)
	case "J":
		return Normal, b.join(max(2, c.count) - 1)
	case "r":
		if b.p+n > le {
			return Normal, false
		}
		for i := 0; i < n; i++ {
			b.t[b.p+i] = c.arg
		}
		b.p += n - 1
		return Normal, true
	case "~":
		if b.p == le {
			return Normal, false
		}
		end := min(b.p+n, le)
		for i := b.p; i < end; i++ {
			b.t[i] = toggleCase(b.t[i])
		}
		b.p = end
		b.clamp()
		return Normal, true
	}
	return e.operate(b, c)
}

// operate applies c.op over the range c's motion or text object spans.
func (e *Editor) operate(b *buf, c command) (Mode, bool) {
	n := c.n()
	var s, end int
	linewise := false
	switch {
	case c.name == "_":
		first := b.lineOf(b.p)
		last := min(first+n-1, b.lines()-1)
		s, end, linewise = b.posOfLine(first), b.lineEnd(b.posOfLine(last)), true
	case len(c.name) == 2 && (c.name[0] == 'i' || c.name[0] == 'a') && textObject(c.name):
		var ok bool
		s, end, ok = b.object(c.name)
		if !ok {
			return Normal, false
		}
	default:
		name := c.name
		if c.op == "c" && (name == "w" || name == "W") && b.p < len(b.t) && !isSpace(b.t[b.p]) {
			name = map[string]string{"w": "e", "W": "E"}[name] // cw is ce
		}
		target, kind, ok := e.motion(b, name, c.arg, n, c.hasCount(), true)
		if !ok {
			return Normal, false
		}
		s, end = min(b.p, target), max(b.p, target)
		switch kind {
		case linewiseMotion:
			s, end, linewise = b.lineStart(s), b.lineEnd(end), true
		case inclusive:
			end = min(end+1, len(b.t))
		case exclusive:
			// vi's exclusive-motion adjustment: an end at column 0 of
			// a later line pulls back to the end of the line before.
			if end > s && b.lineStart(end) == end && b.lineOf(end) > b.lineOf(s) {
				end--
				if s <= b.firstNonBlank(s) && s == b.lineStart(s) {
					linewise = true
				}
			}
		}
	}
	if s == end && !linewise && c.op != "c" {
		return Normal, false
	}
	text := append([]rune(nil), b.t[s:end]...)
	switch c.op {
	case "y":
		e.reg = register{string(text), linewise}
		if linewise {
			if s < b.lineStart(b.p) {
				_, col := b.rowCol(b.p)
				b.p = b.posAt(b.lineOf(s), col)
			}
		} else {
			b.p = s
		}
		b.clamp()
		return Normal, false
	case "d":
		e.reg = register{string(text), linewise}
		if linewise {
			b.deleteLines(s, end)
			b.p = b.firstNonBlank(b.p)
		} else {
			b.remove(s, end)
			b.p = s
			b.clamp()
		}
		return Normal, true
	case "c":
		e.reg = register{string(text), linewise}
		b.remove(s, end)
		b.p = s
		return Insert, true
	default: // gu gU g~
		for i := s; i < end; i++ {
			switch c.op {
			case "gu":
				b.t[i] = unicode.ToLower(b.t[i])
			case "gU":
				b.t[i] = unicode.ToUpper(b.t[i])
			default:
				b.t[i] = toggleCase(b.t[i])
			}
		}
		b.p = s
		b.clamp()
		return Normal, true
	}
}

// put is p (after) / P (before), n copies.
func (e *Editor) put(b *buf, after bool, n int) bool {
	if e.reg.text == "" {
		return false
	}
	r := []rune(e.reg.text)
	if e.reg.linewise {
		var block []rune
		for i := 0; i < n; i++ {
			block = append(block, r...)
			block = append(block, '\n')
		}
		if after {
			at := b.lineEnd(b.p)
			b.insert(at, append([]rune{'\n'}, block[:len(block)-1]...))
			b.p = b.firstNonBlank(at + 1)
		} else {
			at := b.lineStart(b.p)
			b.insert(at, block)
			b.p = b.firstNonBlank(at)
		}
		return true
	}
	at := b.p
	if after && b.p < b.lineEnd(b.p) {
		at++
	}
	var block []rune
	for i := 0; i < n; i++ {
		block = append(block, r...)
	}
	b.insert(at, block)
	b.p = at + len(block) - 1
	b.clamp()
	return true
}

func (e *Editor) undoStep(b *buf, n int) {
	for i := 0; i < n && len(e.undo) > 0; i++ {
		e.redo = append(e.redo, snapshot{string(b.t), b.p})
		s := e.undo[len(e.undo)-1]
		e.undo = e.undo[:len(e.undo)-1]
		b.t, b.p = []rune(s.text), s.pos
	}
	b.clamp()
}

func (e *Editor) redoStep(b *buf, n int) {
	for i := 0; i < n && len(e.redo) > 0; i++ {
		e.undo = append(e.undo, snapshot{string(b.t), b.p})
		s := e.redo[len(e.redo)-1]
		e.redo = e.redo[:len(e.redo)-1]
		b.t, b.p = []rune(s.text), s.pos
	}
	b.clamp()
}

// repeat is '.': the last change again, with a new count if one is
// given, and the same text typed into any insert it opened.
func (e *Editor) repeat(b *buf, dot command) Mode {
	if e.last == nil {
		return Normal
	}
	ch := *e.last
	c := ch.cmd
	if dot.count > 0 {
		c.count, c.count2 = dot.count, 0
	}
	pre := snapshot{string(b.t), b.p}
	mode, changed := e.do(b, c)
	if mode == Insert {
		typed := []rune(ch.text)
		count := 1
		if strings.Contains("iaIA", c.name) && len(c.name) == 1 {
			count = c.n()
		}
		for i := 0; i < count; i++ {
			b.insert(b.p, typed)
			b.p += len(typed)
		}
		if b.p > b.lineStart(b.p) {
			b.p--
		}
		changed = true
	}
	b.clamp()
	e.wantSet = false
	if changed && string(b.t) != pre.text {
		e.pushUndo(pre)
	}
	return Normal
}

// insertedText returns what was typed at at, if the edit from base to
// cur was a pure insertion there.
func insertedText(base, cur []rune, at int) ([]rune, bool) {
	d := len(cur) - len(base)
	if d < 0 || at > len(base) {
		return nil, false
	}
	if string(cur[:at]) != string(base[:at]) || string(cur[at+d:]) != string(base[at:]) {
		return nil, false
	}
	return append([]rune(nil), cur[at:at+d]...), true
}

func toggleCase(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}
