package matrix

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.mau.fi/gomuks/pkg/hicli/database"
	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/pushrules"

	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/sidebar"
)

func notifyActions() pushrules.PushActionArray {
	return pushrules.PushActionArray{{Action: pushrules.ActionNotify}}
}

func roomRules(rules ...*pushrules.PushRule) *pushrules.PushRuleset {
	return &pushrules.PushRuleset{Room: pushrules.PushRuleArray(rules).SetTypeAndMap(pushrules.RoomRule)}
}

func overrideMute(roomID id.RoomID, actions pushrules.PushActionArray) *pushrules.PushRuleset {
	return &pushrules.PushRuleset{Override: pushrules.PushRuleArray{{
		RuleID:  string(roomID),
		Enabled: true,
		Actions: actions,
		Conditions: []*pushrules.PushCondition{
			{Kind: pushrules.KindEventMatch, Key: "room_id", Pattern: string(roomID)},
		},
	}}}
}

// A room is muted by a room rule (bridgev2, gomuks) or an override rule
// on its room ID (Element) that does not notify.
func TestPushRuleMuted(t *testing.T) {
	const room = id.RoomID("!r")
	for name, tc := range map[string]struct {
		rs   *pushrules.PushRuleset
		want bool
	}{
		"no rules":         {nil, false},
		"room rule":        {roomRules(&pushrules.PushRule{RuleID: string(room), Enabled: true}), true},
		"disabled":         {roomRules(&pushrules.PushRule{RuleID: string(room)}), false},
		"notifying rule":   {roomRules(&pushrules.PushRule{RuleID: string(room), Enabled: true, Actions: notifyActions()}), false},
		"another room":     {roomRules(&pushrules.PushRule{RuleID: "!other", Enabled: true}), false},
		"override":         {overrideMute(room, nil), true},
		"notify override":  {overrideMute(room, notifyActions()), false},
		"other room's one": {overrideMute("!other", nil), false},
	} {
		if got := pushRuleMuted(tc.rs, room); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// Muted rooms carry IsMuted in the sidebar and do not light the rail.
func TestMutedChannels(t *testing.T) {
	b := spacesBackend(t)
	now := time.Now()
	b.mutes["!general"] = -1                             // forever
	b.mutes["!home"] = now.Add(-time.Minute).UnixMilli() // run out
	b.cli.PushRules.Store(roomRules(&pushrules.PushRule{RuleID: "!dm", Enabled: true}))

	muted := map[string]bool{}
	for _, ws := range []string{string(testMe), "!slack", "!signal"} {
		for _, it := range b.channelList(ws) {
			muted[it.ID] = it.IsMuted
		}
	}
	for room, want := range map[string]bool{"!general": true, "!home": false, "!shared": false, "!dm": true} {
		if muted[room] != want {
			t.Errorf("%s: IsMuted = %v, want %v", room, muted[room], want)
		}
	}

	b.rooms["!general"].UnreadMessages = 1
	b.rooms["!dm"].UnreadMessages = 1
	if got := b.UnreadWorkspaces(); len(got) != 0 {
		t.Errorf("only muted rooms unread: got %v", got)
	}
	b.rooms["!shared"].UnreadMessages = 1
	got := b.UnreadWorkspaces()
	slices.Sort(got)
	if want := []string{"!signal", "!slack"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A muted DM counts only mentions: hicli's push rules do not know
// com.beeper.mute, so its notification count still counts every message.
func TestReadStatesMutedDM(t *testing.T) {
	peer := id.UserID("@beth:example.com")
	dm := &database.Room{ID: "!dm", DMUserID: &peer, SortingTimestamp: jsontime.UM(time.Now())}
	dm.UnreadNotifications, dm.UnreadHighlights, dm.UnreadMessages = 3, 1, 3
	b := &Backend{rooms: map[id.RoomID]*database.Room{"!dm": dm}}
	if got := b.ReadStates()["!dm"].MentionCount; got != 3 {
		t.Errorf("unmuted: %d mentions, want 3", got)
	}
	b.mutes = map[id.RoomID]int64{"!dm": -1}
	if got := b.ReadStates()["!dm"].MentionCount; got != 1 {
		t.Errorf("muted: %d mentions, want 1", got)
	}
}

type fakeNotifier struct{ titles []string }

func (f *fakeNotifier) Notify(title, _ string) error {
	f.titles = append(f.titles, title)
	return nil
}

// Muted rooms notify only for mentions.
func TestNotifyMuted(t *testing.T) {
	n := &fakeNotifier{}
	b := &Backend{
		Notifier: n,
		names:    map[id.UserID]string{"@beth:example.com": "beth"},
		mutes:    map[id.RoomID]int64{"!muted": -1},
	}
	for _, tc := range []struct {
		room      id.RoomID
		highlight bool
	}{{"!muted", false}, {"!muted", true}, {"!open", false}} {
		b.notify(jsoncmd.SyncNotification{
			Highlight: tc.highlight,
			Event:     &database.Event{RoomID: tc.room, Sender: "@beth:example.com"},
		})
	}
	if len(n.titles) != 2 {
		t.Errorf("got %d notifications, want 2 (the mention and the open room)", len(n.titles))
	}
}

// Mutes stored in the database load at startup, and a sync that mutes
// or unmutes a room refreshes the sidebar.
func TestMuteLoadAndSync(t *testing.T) {
	b := spacesBackend(t)
	ctx := context.Background()
	if err := b.cli.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.cli.DB.Close() })
	content := func(until int64) json.RawMessage {
		raw, _ := json.Marshal(event.BeeperMuteEventContent{MutedUntil: until})
		return raw
	}
	if err := b.cli.DB.Room.CreateRow(ctx, "!home"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.cli.DB.AccountData.PutRoom(ctx, testMe, "!home", event.AccountDataBeeperMute, content(-1)); err != nil {
		t.Fatal(err)
	}
	if err := b.loadMutes(ctx); err != nil {
		t.Fatal(err)
	}
	if !b.muted("!home") {
		t.Fatal("stored mute not loaded")
	}

	var mu sync.Mutex
	var sent []tea.Msg
	b.SetSender(func(msg tea.Msg) {
		mu.Lock()
		sent = append(sent, msg)
		mu.Unlock()
	})
	syncMute := func(until int64) []sidebar.ChannelItem {
		t.Helper()
		mu.Lock()
		sent = nil
		mu.Unlock()
		b.handleSync(&jsoncmd.SyncComplete{Rooms: map[id.RoomID]*jsoncmd.SyncRoom{
			"!home": {
				Meta: b.rooms["!home"],
				AccountData: map[event.Type]*database.AccountData{
					event.AccountDataBeeperMute: {RoomID: "!home", Type: event.AccountDataBeeperMute.Type, Content: content(until)},
				},
			},
		}})
		mu.Lock()
		defer mu.Unlock()
		for _, m := range sent {
			if r, ok := m.(ui.SectionsRefreshedMsg); ok {
				return r.Channels
			}
		}
		return nil
	}
	if got := syncMute(-1); got != nil {
		t.Errorf("unchanged mute refreshed the sidebar: %v", got)
	}
	got := syncMute(0)
	if len(got) != 1 || got[0].ID != "!home" || got[0].IsMuted {
		t.Errorf("unmute: refreshed with %+v, want !home unmuted", got)
	}
	got = syncMute(time.Now().Add(time.Hour).UnixMilli())
	if len(got) != 1 || !got[0].IsMuted {
		t.Errorf("timed mute: refreshed with %+v, want !home muted", got)
	}
}
