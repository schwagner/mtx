package compose

import "github.com/schwagner/mtx/internal/ui/vimedit"

// VimKey runs one vi normal-mode key against the composer's text and
// reports the mode it leaves (Insert after i, a, o, c...).
func (m *Model) VimKey(key string) vimedit.Result {
	r := m.vim.Key(key, m.input.Value(), m.input.Line(), m.input.Column())
	m.applyVim(r)
	return r
}

// VimEndInsert leaves insert mode the vi way: the cursor steps back
// onto the last typed character and the insert becomes one undo step.
func (m *Model) VimEndInsert() {
	m.closePickers()
	m.applyVim(m.vim.EndInsert(m.input.Value(), m.input.Line(), m.input.Column()))
}

// VimPending reports whether a vi command is partly typed ("d", "2f").
func (m *Model) VimPending() bool { return m.vim.Pending() }

// SetVimNormal shows a steady cursor in vi normal mode and the usual
// blinking one in insert mode.
func (m *Model) SetVimNormal(on bool) {
	s := m.input.Styles()
	if s.Cursor.Blink == !on {
		return
	}
	s.Cursor.Blink = !on
	m.input.SetStyles(s)
	m.dirty()
}

func (m *Model) applyVim(r vimedit.Result) {
	if r.Text != m.input.Value() {
		m.input.SetValue(r.Text)
	}
	m.setCursor(r.Row, r.Col)
	m.autoGrow()
	m.dirty()
}

// setCursor puts the cursor at a logical row and rune column. The
// textarea moves down by wrapped rows, so step until the row is reached.
func (m *Model) setCursor(row, col int) {
	if m.input.Line() != row {
		m.input.MoveToBegin()
		for guard := m.input.Length() + m.input.LineCount(); m.input.Line() < row && guard > 0; guard-- {
			m.input.CursorDown()
		}
	}
	m.input.SetCursorColumn(col)
}
