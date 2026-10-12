package matrix

import (
	"testing"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/ui/sidebar"
)

// The sidebar's hide-inactive filter treats a DM without a LastReadTS as
// closed. Read Matrix DMs must report their last activity so they stay
// listed until they really are inactive.
func TestReadStatesKeepActiveDMsVisible(t *testing.T) {
	now := time.Now()
	peer := id.UserID("@beth:example.com")
	dm := func(roomID id.RoomID, last time.Time) *database.Room {
		return &database.Room{ID: roomID, DMUserID: &peer, SortingTimestamp: jsontime.UM(last)}
	}
	b := &Backend{rooms: map[id.RoomID]*database.Room{
		"!recent": dm("!recent", now.Add(-48*time.Hour)),
		"!old":    dm("!old", now.Add(-60*24*time.Hour)),
		"!empty":  dm("!empty", time.UnixMilli(0)),
	}}
	states := b.ReadStates()
	for roomID, wantStale := range map[string]bool{"!recent": false, "!old": true, "!empty": false} {
		item := sidebar.ChannelItem{ID: roomID, Type: "dm"}
		st := states[roomID]
		if got := sidebar.IsStale(item, st.HasUnread, st.LastReadTS, 30*24*time.Hour, now); got != wantStale {
			t.Errorf("%s: stale = %v, want %v (LastReadTS %q)", roomID, got, wantStale, st.LastReadTS)
		}
	}
}
