package matrix

import (
	"encoding/json"
	"testing"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/id"
)

// Member events carry avatars, and a later one replaces an earlier one,
// whether or not it also states a display name.
func TestLearnMemberAvatar(t *testing.T) {
	b := &Backend{
		names:    map[id.UserID]string{},
		avatars:  map[id.UserID]id.ContentURIString{},
		activeWS: "!space",
	}
	member := func(content string) *database.Event {
		key := "@beth:example.com"
		return &database.Event{StateKey: &key, Content: json.RawMessage(content)}
	}
	if got := b.AvatarURL("@beth:example.com"); got != "" {
		t.Fatalf("AvatarURL before any member event = %q; want empty", got)
	}
	b.learnMember(member(`{"membership":"join","displayname":"Beth","avatar_url":"mxc://example.com/a"}`))
	if got, want := b.AvatarURL("@beth:example.com"), "mxc://example.com/a"; got != want {
		t.Errorf("AvatarURL = %q; want %q", got, want)
	}
	b.learnMember(member(`{"membership":"join","avatar_url":"mxc://example.com/b"}`))
	if got, want := b.AvatarURL("@beth:example.com"), "mxc://example.com/b"; got != want {
		t.Errorf("AvatarURL after a nameless change = %q; want %q", got, want)
	}
	if got := b.DisplayName("@beth:example.com"); got != "Beth" {
		t.Errorf("DisplayName = %q; want Beth", got)
	}
}
