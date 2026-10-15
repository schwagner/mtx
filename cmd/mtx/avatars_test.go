package main

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/schwagner/mtx/internal/avatar"
)

func TestInitial(t *testing.T) {
	for _, tc := range []struct{ name, userID, want string }{
		{"beth", "@b:example.com", "B"},
		{"  (Émile)", "@e:example.com", "É"},
		{"", "@zed:example.com", "Z"},
		{"🎉", "@_99:example.com", "9"},
		{"", "@_:example.com", "?"},
	} {
		if got := initial(tc.name, tc.userID); got != tc.want {
			t.Errorf("initial(%q, %q) = %q; want %q", tc.name, tc.userID, got, tc.want)
		}
	}
}

// The tile fills the same 4x2 cells as a real avatar, so a row does not
// reflow when the image replaces it.
func TestInitialTileFillsAvatarSlot(t *testing.T) {
	tile := initialTile("@beth:example.com", "Beth")
	lines := strings.Split(tile, "\n")
	if len(lines) != avatar.AvatarRows {
		t.Fatalf("tile has %d rows; want %d", len(lines), avatar.AvatarRows)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != avatar.AvatarCols {
			t.Errorf("row %d is %d cols wide; want %d", i, w, avatar.AvatarCols)
		}
	}
	if !strings.Contains(lines[0], "B") {
		t.Errorf("first row %q lacks the initial", lines[0])
	}
}

type fakeAvatars struct{ url, name string }

func (f fakeAvatars) AvatarURL(string) string   { return f.url }
func (f fakeAvatars) DisplayName(string) string { return f.name }

// With images off there is no cache: every author gets a tile.
func TestLazyAvatarWithoutImages(t *testing.T) {
	got := lazyAvatar(nil, fakeAvatars{url: "mxc://example.com/a", name: "Beth"}, "@beth:example.com")
	if got != initialTile("@beth:example.com", "Beth") {
		t.Errorf("lazyAvatar = %q; want the initial tile", got)
	}
}
