package matrix

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
)

// Delivery indicators on own messages, for rooms bridged to a network
// that reports them (Signal, Google Messages; not Slack). Such a bridge
// advertises read_receipts in its com.beeper.room_features state, and
// relays a recipient reading a message as a Matrix read receipt from
// that recipient's ghost user. If the bridge also has
// message_status_events enabled, it reports each message's fate in a
// com.beeper.message_send_status event (MSS): success, failure and,
// where the network has them, delivery receipts (delivered_to_users).
//
// Receipts from the bridge bot itself mean only that the bridge handled
// the message (bridgev2's delivery_receipts option), so they don't
// count as read.

// deliveryRoom describes a room that reports delivery, or is nil for
// one that doesn't.
type deliveryRoom struct {
	// bots are the room's bridge bots, whose receipts are not reads
	// and whose MSS events are the only ones trusted.
	bots map[id.UserID]bool
}

// counts reports whether a read receipt from userID means a recipient
// read the message.
func (r *deliveryRoom) counts(userID, me id.UserID) bool {
	return userID != me && !r.bots[userID]
}

// deliveryRoom loads roomID's delivery reporting from its current state.
func (b *Backend) deliveryRoom(ctx context.Context, roomID id.RoomID) *deliveryRoom {
	rows, err := b.cli.DB.Query(ctx, `
		SELECT cs.event_type, e.content FROM current_state cs
		JOIN event e ON e.rowid = cs.event_rowid
		WHERE cs.room_id = $1 AND cs.event_type IN ($2, $3, $4)`,
		roomID, event.StateBeeperRoomFeatures.Type, event.StateBridge.Type, event.StateHalfShotBridge.Type)
	if err != nil {
		b.log.Warn().Err(err).Msg("loading bridge state")
		return nil
	}
	defer rows.Close()
	receipts := false
	room := &deliveryRoom{bots: map[id.UserID]bool{}}
	for rows.Next() {
		var typ string
		var content []byte
		if rows.Scan(&typ, &content) != nil {
			continue
		}
		if typ == event.StateBeeperRoomFeatures.Type {
			var features event.RoomFeatures
			if json.Unmarshal(content, &features) == nil && features.ReadReceipts {
				receipts = true
			}
			continue
		}
		var bridge event.BridgeEventContent
		if json.Unmarshal(content, &bridge) == nil && bridge.BridgeBot != "" {
			room.bots[bridge.BridgeBot] = true
		}
	}
	if !receipts {
		return nil
	}
	return room
}

// readThrough is the origin timestamp (ms) of the latest event a
// recipient has a read receipt on, or 0.
func (b *Backend) readThrough(ctx context.Context, roomID id.RoomID, room *deliveryRoom) int64 {
	rows, err := b.cli.DB.Query(ctx, `
		SELECT r.user_id, e.timestamp FROM receipt r
		JOIN event e ON e.room_id = r.room_id AND e.event_id = r.event_id
		WHERE r.room_id = $1 AND r.receipt_type = $2`,
		roomID, event.ReceiptTypeRead)
	if err != nil {
		b.log.Warn().Err(err).Msg("loading read receipts")
		return 0
	}
	defer rows.Close()
	me := b.UserID()
	var through int64
	for rows.Next() {
		var user id.UserID
		var ts int64
		if rows.Scan(&user, &ts) == nil && room.counts(user, me) {
			through = max(through, ts)
		}
	}
	return through
}

// sendStatuses is the latest MSS-reported status of each of evtIDs that
// has one.
func (b *Backend) sendStatuses(ctx context.Context, roomID id.RoomID, room *deliveryRoom, evtIDs []id.EventID) map[id.EventID]core.Delivery {
	args := []any{roomID, event.BeeperMessageStatus.Type}
	marks := make([]string, len(evtIDs))
	for i, evtID := range evtIDs {
		args = append(args, evtID)
		marks[i] = "$" + strconv.Itoa(len(args))
	}
	rows, err := b.cli.DB.Query(ctx, `
		SELECT sender, relates_to, COALESCE(decrypted, content) FROM event
		WHERE room_id = $1 AND (type = $2 OR decrypted_type = $2)
		  AND relates_to IN (`+strings.Join(marks, ", ")+`)
		ORDER BY rowid`, args...)
	if err != nil {
		b.log.Warn().Err(err).Msg("loading message send statuses")
		return nil
	}
	defer rows.Close()
	out := map[id.EventID]core.Delivery{}
	for rows.Next() {
		var sender id.UserID
		var target id.EventID
		var content []byte
		if rows.Scan(&sender, &target, &content) != nil || !room.bots[sender] {
			continue
		}
		if d := mssDelivery(content); d != core.DeliveryNone {
			out[target] = d
		}
	}
	return out
}

// mssDelivery is the status a com.beeper.message_send_status event
// reports, or DeliveryNone for one it can't be read from.
func mssDelivery(content json.RawMessage) core.Delivery {
	var mss event.BeeperMessageStatusEventContent
	if json.Unmarshal(content, &mss) != nil {
		return core.DeliveryNone
	}
	switch mss.Status {
	case event.MessageStatusSuccess:
		if mss.DeliveredToUsers != nil && len(*mss.DeliveredToUsers) > 0 {
			return core.DeliveryDelivered
		}
		return core.DeliverySent
	case event.MessageStatusPending:
		return core.DeliverySent
	case event.MessageStatusRetriable, event.MessageStatusFail:
		return core.DeliveryFailed
	}
	return core.DeliveryNone
}

// deliveryStatuses is the delivery status of each own message among
// evts, for a room that reports them; nil otherwise.
func (b *Backend) deliveryStatuses(ctx context.Context, roomID id.RoomID, evts []*database.Event) map[id.EventID]core.Delivery {
	me := b.UserID()
	var own []*database.Event
	for _, evt := range evts {
		if evt.Sender == me && evt.RedactedBy == "" && isTimelineMessage(evt) {
			own = append(own, evt)
		}
	}
	if len(own) == 0 {
		return nil
	}
	room := b.deliveryRoom(ctx, roomID)
	if room == nil {
		return nil
	}
	evtIDs := make([]id.EventID, len(own))
	for i, evt := range own {
		evtIDs[i] = evt.ID
	}
	reported := b.sendStatuses(ctx, roomID, room, evtIDs)
	through := b.readThrough(ctx, roomID, room)
	out := make(map[id.EventID]core.Delivery, len(own))
	for _, evt := range own {
		d := core.DeliverySent
		if r, ok := reported[evt.ID]; ok {
			d = r
		}
		if evt.Timestamp.UnixMilli() <= through {
			d = d.Read()
		}
		out[evt.ID] = d
	}
	return out
}

// handleSendStatus relays a live MSS event about an own message.
func (b *Backend) handleSendStatus(ctx context.Context, roomID id.RoomID, evt *database.Event) {
	if evt.RelatesTo == "" {
		return
	}
	d := mssDelivery(effectiveContent(evt))
	if d == core.DeliveryNone {
		return
	}
	room := b.deliveryRoom(ctx, roomID)
	if room == nil || !room.bots[evt.Sender] {
		return
	}
	target, err := b.cli.DB.Event.GetByID(ctx, roomID, evt.RelatesTo)
	if err != nil || target == nil || target.Sender != b.UserID() {
		return
	}
	b.emit(ui.MessageDeliveryMsg{ChannelID: string(roomID), TS: b.idx.put(target), Delivery: d})
}

// handleReceipts relays a sync's read receipts from recipients as the
// latest message they have read through.
func (b *Backend) handleReceipts(ctx context.Context, roomID id.RoomID, receipts map[id.EventID][]*database.Receipt) {
	me := b.UserID()
	var room *deliveryRoom
	through := ""
	for evtID, list := range receipts {
		for _, r := range list {
			if r.ReceiptType != event.ReceiptTypeRead || r.UserID == me {
				continue
			}
			if room == nil {
				if room = b.deliveryRoom(ctx, roomID); room == nil {
					return
				}
			}
			if !room.counts(r.UserID, me) {
				continue
			}
			if ts, ok := b.tsForEvent(ctx, roomID, evtID); ok && ts > through {
				through = ts
			}
			break
		}
	}
	if through != "" {
		b.emit(ui.MessagesReadMsg{ChannelID: string(roomID), ThroughTS: through})
	}
}
