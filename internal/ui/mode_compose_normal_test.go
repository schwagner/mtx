package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// vimApp is an App with compose.vim_mode on, in insert mode on the
// channel composer.
func vimApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t, insertOpts()...)
	a.SetComposeVimMode(true)
	a.handleKey(keyPress('i'))
	if a.mode != ModeInsert {
		t.Fatalf("precondition: mode = %v, want INSERT", a.mode)
	}
	return a
}

// typeKeys feeds keys through handleKey; "<esc>" and "<cr>" are special.
func typeKeys(a *App, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		switch k {
		case "<esc>":
			cmd = a.handleKey(keyCode(tea.KeyEscape))
		case "<cr>":
			cmd = a.handleKey(keyCode(tea.KeyEnter))
		default:
			for _, r := range k {
				cmd = a.handleKey(keyPress(r))
			}
		}
	}
	return cmd
}

func TestComposeNormal_EscKeepsComposerAndEditsWithVi(t *testing.T) {
	a := vimApp(t)
	a.compose.SetValue("one\ntwo\nthree")
	a.compose.MoveCursorToEnd()

	typeKeys(a, "<esc>")
	if a.mode != ModeComposeNormal {
		t.Fatalf("Esc with text: mode = %v, want COMPOSE", a.mode)
	}
	typeKeys(a, "k", "dd")
	if got := a.compose.Value(); got != "one\nthree" {
		t.Fatalf("after kdd: %q, want %q", got, "one\nthree")
	}
	typeKeys(a, "u")
	if got := a.compose.Value(); got != "one\ntwo\nthree" {
		t.Fatalf("after u: %q", got)
	}
	typeKeys(a, "<esc>")
	if a.mode != ModeNormal {
		t.Fatalf("second Esc: mode = %v, want NORMAL", a.mode)
	}
}

func TestComposeNormal_EscOnEmptyComposerLeaves(t *testing.T) {
	a := vimApp(t)
	typeKeys(a, "<esc>")
	if a.mode != ModeNormal {
		t.Fatalf("Esc on empty composer: mode = %v, want NORMAL", a.mode)
	}
}

func TestComposeNormal_OffKeepsSlkEsc(t *testing.T) {
	a := newTestApp(t, insertOpts()...)
	typeKeys(a, "i", "hello", "<esc>")
	if a.mode != ModeNormal {
		t.Fatalf("vim_mode off: mode = %v, want NORMAL", a.mode)
	}
}

func TestComposeNormal_InsertCommandsReturnToInsert(t *testing.T) {
	a := vimApp(t)
	typeKeys(a, "foo bar", "<esc>", "0", "cw")
	if a.mode != ModeInsert {
		t.Fatalf("cw: mode = %v, want INSERT", a.mode)
	}
	typeKeys(a, "baz", "<esc>")
	if got := a.compose.Value(); got != "baz bar" {
		t.Fatalf("cw + baz = %q, want %q", got, "baz bar")
	}
	if a.mode != ModeComposeNormal {
		t.Fatalf("mode = %v, want COMPOSE", a.mode)
	}
}

func TestComposeNormal_PendingEscCancelsCommandOnly(t *testing.T) {
	a := vimApp(t)
	typeKeys(a, "abc", "<esc>", "d", "<esc>")
	if a.mode != ModeComposeNormal {
		t.Fatalf("Esc after d: mode = %v, want COMPOSE (cancels the operator)", a.mode)
	}
	typeKeys(a, "x")
	if got := a.compose.Value(); got != "ab" {
		t.Fatalf("x after cancelled d = %q, want ab", got)
	}
}

func TestComposeNormal_EnterSends(t *testing.T) {
	a := vimApp(t)
	cmd := typeKeys(a, "hello", "<esc>", "<cr>")
	if cmd == nil {
		t.Fatal("Enter: cmd = nil, want a send")
	}
	msg, ok := cmd().(SendMessageMsg)
	if !ok || msg.Text != "hello" || msg.ChannelID != "C1" {
		t.Fatalf("Enter sent %#v", cmd())
	}
	if a.mode != ModeNormal || a.compose.Value() != "" {
		t.Fatalf("after send: mode %v, value %q", a.mode, a.compose.Value())
	}
}

func TestComposeNormal_EditEscStopsInNormalThenCancels(t *testing.T) {
	a := vimApp(t)
	beginEdit(a, PanelMessages, "stashed draft")(t, a)
	a.compose.SetValue("edited text")

	typeKeys(a, "<esc>")
	if a.mode != ModeComposeNormal || !a.editing.IsActive() {
		t.Fatalf("first Esc: mode %v, editing %v; want COMPOSE, still editing", a.mode, a.editing.IsActive())
	}
	typeKeys(a, "<esc>")
	if a.editing.IsActive() || a.mode != ModeNormal {
		t.Fatalf("second Esc: mode %v, editing %v; want NORMAL, edit cancelled", a.mode, a.editing.IsActive())
	}
	if got := a.compose.Value(); got != "stashed draft" {
		t.Fatalf("cancel restored %q, want the stashed draft", got)
	}
}

func TestComposeNormal_TargetsThreadComposer(t *testing.T) {
	a := vimApp(t)
	a.focusedPanel = PanelThread
	a.threadVisible = true
	_ = a.threadCompose.Focus()
	a.compose.SetValue("channel")
	typeKeys(a, "reply", "<esc>", "x")
	if got := a.threadCompose.Value(); got != "repl" {
		t.Fatalf("thread compose = %q, want repl", got)
	}
	if got := a.compose.Value(); got != "channel" {
		t.Fatalf("channel compose touched: %q", got)
	}
}

func TestComposeNormal_ViewKeepsComposerFocused(t *testing.T) {
	a := vimApp(t)
	typeKeys(a, "abc", "<esc>")
	if !a.mode.composing() {
		t.Fatal("ModeComposeNormal must count as composing for the views")
	}
	if ModeComposeNormal.String() != "COMPOSE" {
		t.Fatalf("status label = %q", ModeComposeNormal.String())
	}
}
