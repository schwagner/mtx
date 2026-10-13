// internal/ui/mode_compose_normal.go
//
// Compose-normal key handler: vi normal mode inside a composer, on
// when compose.vim_mode is set.
//
// Esc in insert mode lands here instead of leaving the composer (an
// empty composer still leaves directly). Keys are vi commands run by
// the composer's vimedit engine — dd, cw, x, p, u, '.', counts, text
// objects — and the ones that start an insert (i a o c...) return to
// ModeInsert. It also owns:
//
//   - Esc (no command pending) -> leave the composer for navigation,
//     cancelling an in-progress message edit.
//   - Enter / Alt+Enter         -> send (or save the edit), exactly as
//     in insert mode.
//   - Ctrl+E                    -> edit the draft in $EDITOR.
package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/schwagner/mtx/internal/ui/compose"
	"github.com/schwagner/mtx/internal/ui/vimedit"
)

func handleComposeNormalMode(a *App, msg tea.KeyMsg) tea.Cmd {
	if a.compose.Uploading() || a.threadCompose.Uploading() {
		return a.uploadToastCmd("Upload in progress", 2*time.Second)
	}
	target := a.activeComposer()
	k := msg.Key()
	mod := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock)
	if !target.VimPending() {
		switch {
		case k.Code == tea.KeyEscape:
			a.leaveComposeNormal()
			return nil
		case k.Code == tea.KeyEnter && !mod.Contains(tea.ModShift):
			return handleInsertMode(a, msg)
		case k.Code == 'e' && mod == tea.ModCtrl:
			return a.openComposeInEditor()
		}
	}
	if r := target.VimKey(vimKeyName(msg)); r.Mode == vimedit.Insert {
		a.SetMode(ModeInsert)
	}
	return nil
}

// activeComposer is the composer keys go to: the thread's when the
// thread pane has focus, else the channel's.
func (a *App) activeComposer() *compose.Model {
	if a.focusedPanel == PanelThread && a.threadVisible {
		return &a.threadCompose
	}
	return &a.compose
}

// enterComposeNormal is Esc from insert mode with vi mode on.
func (a *App) enterComposeNormal() {
	a.activeComposer().VimEndInsert()
	a.SetMode(ModeComposeNormal)
}

// leaveComposeNormal is the second Esc: back to navigation, as Esc
// from insert mode does without vi mode.
func (a *App) leaveComposeNormal() {
	if a.editing.IsActive() {
		a.cancelEdit()
		return
	}
	a.SetMode(ModeNormal)
	a.compose.Blur()
	a.threadCompose.Blur()
}

// vimKeyName is the key as vimedit reads it: the typed character, or
// the key's name ("esc", "ctrl+r", "left") when it types none.
func vimKeyName(msg tea.KeyMsg) string {
	k := msg.Key()
	mod := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock | tea.ModShift)
	if k.Text != "" && mod == 0 {
		return k.Text
	}
	return msg.String()
}
