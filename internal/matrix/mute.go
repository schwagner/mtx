package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/pushrules"
)

// Muted rooms. A conversation muted on the remote network (Slack's
// muted_channels pref) reaches Matrix through the bridge's double
// puppet in one of two forms (bridgev2's MuteRoom): com.beeper.mute
// room account data where the homeserver supports it (Beeper), else a
// room push rule with no actions. Matrix clients mute with that room
// rule (gomuks) or an override rule matching the room ID (Element).
// Any of them marks the room muted.
//
// hicli evaluates push rules, so a push-rule mute already keeps its
// events out of notifications and unread notification counts; it does
// not know com.beeper.mute, so notify and ReadStates filter those.
// Mentions pierce a mute, as in Slack.

// mutedLocked reports whether roomID is muted at now.
func (b *Backend) mutedLocked(roomID id.RoomID, now time.Time) bool {
	if until, ok := b.mutes[roomID]; ok && (until < 0 || until > now.UnixMilli()) {
		return true
	}
	return pushRuleMuted(b.pushRules(), roomID)
}

func (b *Backend) muted(roomID id.RoomID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mutedLocked(roomID, time.Now())
}

// mutedSetLocked is the set of muted rooms, to tell whether a sync
// changed any.
func (b *Backend) mutedSetLocked() map[id.RoomID]bool {
	now := time.Now()
	out := map[id.RoomID]bool{}
	for roomID := range b.rooms {
		if b.mutedLocked(roomID, now) {
			out[roomID] = true
		}
	}
	return out
}

func (b *Backend) pushRules() *pushrules.PushRuleset {
	if b.cli == nil {
		return nil
	}
	return b.cli.PushRules.Load()
}

// pushRuleMuted reports whether rs mutes roomID: an enabled room rule,
// or an override rule on the room ID alone, that does not notify.
func pushRuleMuted(rs *pushrules.PushRuleset, roomID id.RoomID) bool {
	if rs == nil {
		return false
	}
	if rule := rs.Room.Map[string(roomID)]; rule != nil && rule.Enabled && !rule.Actions.Should().Notify {
		return true
	}
	for _, rule := range rs.Override {
		if !rule.Enabled || rule.Actions.Should().Notify || len(rule.Conditions) != 1 {
			continue
		}
		c := rule.Conditions[0]
		if c.Kind == pushrules.KindEventMatch && c.Key == "room_id" && c.Pattern == string(roomID) {
			return true
		}
	}
	return false
}

// parseMute is com.beeper.mute's muted_until: -1 forever, else the
// unix ms it ends at (0 or past: unmuted).
func parseMute(content json.RawMessage) int64 {
	var c event.BeeperMuteEventContent
	if json.Unmarshal(content, &c) != nil {
		return 0
	}
	return c.MutedUntil
}

// loadMutes reads every room's com.beeper.mute from the database.
func (b *Backend) loadMutes(ctx context.Context) error {
	rows, err := b.cli.DB.Query(ctx,
		`SELECT room_id, content FROM room_account_data WHERE user_id = $1 AND type = $2`,
		b.UserID(), event.AccountDataBeeperMute.Type)
	if err != nil {
		return fmt.Errorf("loading mutes: %w", err)
	}
	defer rows.Close()
	mutes := map[id.RoomID]int64{}
	for rows.Next() {
		var roomID id.RoomID
		var content []byte
		if rows.Scan(&roomID, &content) == nil {
			mutes[roomID] = parseMute(content)
		}
	}
	b.mu.Lock()
	for k, v := range mutes {
		b.mutes[k] = v
	}
	b.mu.Unlock()
	return rows.Err()
}

// learnMutesLocked records the com.beeper.mute changes a sync carries.
func (b *Backend) learnMutesLocked(sc *jsoncmd.SyncComplete) {
	for roomID, sr := range sc.Rooms {
		if ad := sr.AccountData[event.AccountDataBeeperMute]; ad != nil {
			b.mutes[roomID] = parseMute(ad.Content)
		}
	}
}
