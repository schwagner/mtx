package main

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

	"github.com/schwagner/mtx/internal/avatar"
	"github.com/schwagner/mtx/internal/core"
	imgpkg "github.com/schwagner/mtx/internal/image"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/styles"
)

// avatarSource is what the avatars beside message authors need from the
// engine.
type avatarSource interface {
	// AvatarURL is the user's avatar for the image fetcher, or "".
	AvatarURL(userID string) string
	DisplayName(userID string) string
}

// setupAvatars wires the avatars beside message authors into app and
// returns the cache, whose SetOnReady the caller points at the program
// once it exists. fetcher is nil when images are off; every author then
// gets an initial tile. Kitty terminals get sharp avatars; everything
// else half-block, as in slk (sixel per avatar per redraw costs too
// much).
func setupAvatars(app *ui.App, src avatarSource, fetcher *imgpkg.Fetcher, proto imgpkg.Protocol) *avatar.Cache {
	var cache *avatar.Cache
	if fetcher != nil {
		cache = avatar.NewCache(fetcher, imgpkg.KittyRendererInstance(), proto == imgpkg.ProtoKitty)
	}
	app.SetAvatarService(core.NewAvatarService(func(userID string) string {
		return lazyAvatar(cache, src, userID)
	}))
	return cache
}

// lazyAvatar is the AvatarFunc behind every author row, called on the
// bubbletea goroutine. A hit is a map lookup. On a miss it starts a
// background fetch of the user's avatar and shows an initial tile
// meanwhile (and for good, for a user with no avatar), so a row's
// layout does not shift when the image lands; AvatarReadyMsg re-renders
// the row then.
func lazyAvatar(cache *avatar.Cache, src avatarSource, userID string) string {
	if cache != nil {
		if rendered := cache.Get(userID); rendered != "" {
			return rendered
		}
		if url := src.AvatarURL(userID); url != "" {
			cache.Preload(userID, url)
		}
	}
	return initialTile(userID, src.DisplayName(userID))
}

// initialTile is a stand-in avatar: the name's initial on the user's
// color, filling the avatar's 4x2-cell slot.
func initialTile(userID, name string) string {
	style := lipgloss.NewStyle().
		Width(avatar.AvatarCols).
		Align(lipgloss.Center).
		Background(styles.UserColor(userID)).
		Foreground(styles.Background).
		Bold(true)
	rows := make([]string, avatar.AvatarRows)
	for i := range rows {
		rows[i] = style.Render("")
	}
	rows[0] = style.Render(initial(name, userID))
	return strings.Join(rows, "\n")
}

// initial is the first letter or digit of name, or else of the user
// ID's localpart, upper-cased; "?" when neither has one.
func initial(name, userID string) string {
	local, _, _ := strings.Cut(strings.TrimPrefix(userID, "@"), ":")
	for _, s := range []string{name, local} {
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return string(unicode.ToUpper(r))
			}
		}
	}
	return "?"
}
