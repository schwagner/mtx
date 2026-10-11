package matrix

import (
	"context"
	"sort"
	"strings"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ids"
	"github.com/schwagner/mtx/internal/ui"
)

// The TUI's Threads and Activity views were built on Slack's
// subscriptions.thread and activity.feed APIs. Matrix has neither, so both
// are answered from the local database: Threads lists the threads the user
// started or replied to, and Activity lists events that highlighted the
// user (mentions and keyword pushes, as evaluated by hicli's push rules).

const viewLimit = 100

func (b *Backend) wireViews(app *ui.App) {
	app.SetActivityService(core.NewActivityService(b.fetchActivity, b.hydrateActivity))
}

// summaryText renders an event as a single line of TUI text.
func (b *Backend) summaryText(ctx context.Context, evt *database.Event) string {
	items := b.convert(ctx, evt.RoomID, []*database.Event{evt})
	if len(items) == 0 {
		return ""
	}
	return items[0].Text
}

func (b *Backend) fetchThreads(teamID ids.TeamID) core.Msg {
	ctx := b.ctx()
	me := b.UserID()
	// Thread roots the user started or replied in, newest activity first.
	rows, err := b.cli.DB.Query(ctx, `
		SELECT root.rowid, MAX(reply.timestamp) AS last_ts
		FROM event reply
		JOIN event root ON root.room_id = reply.room_id AND root.event_id = reply.relates_to
		WHERE reply.relation_type = 'm.thread' AND reply.redacted_by IS NULL
		  AND (root.sender = $1 OR EXISTS (
		      SELECT 1 FROM event mine
		      WHERE mine.room_id = reply.room_id AND mine.relates_to = reply.relates_to
		        AND mine.relation_type = 'm.thread' AND mine.sender = $1))
		GROUP BY root.rowid
		ORDER BY last_ts DESC
		LIMIT $2`, me, viewLimit)
	if err != nil {
		return ui.ToastMsg{Text: "Loading threads failed: " + err.Error()}
	}
	var roots []database.EventRowID
	for rows.Next() {
		var rowID database.EventRowID
		var last int64
		if rows.Scan(&rowID, &last) == nil {
			roots = append(roots, rowID)
		}
	}
	rows.Close()
	rootEvts, err := b.cli.DB.Event.GetByRowIDs(ctx, roots...)
	if err != nil {
		return ui.ToastMsg{Text: "Loading threads failed: " + err.Error()}
	}
	order := make(map[database.EventRowID]int, len(roots))
	for i, r := range roots {
		order[r] = i
	}
	sort.Slice(rootEvts, func(i, j int) bool { return order[rootEvts[i].RowID] < order[rootEvts[j].RowID] })

	var summaries []core.ThreadSummary
	for _, root := range rootEvts {
		room := b.room(root.RoomID)
		if room == nil {
			continue
		}
		replies, err := b.cli.DB.Event.GetRelatedEvents(ctx, root.RoomID, root.ID, "m.thread", "")
		if err != nil || len(replies) == 0 {
			continue
		}
		sort.Slice(replies, func(i, j int) bool { return replies[i].Timestamp.Before(replies[j].Timestamp.Time) })
		last := replies[len(replies)-1]
		rootTS := b.idx.put(root)
		lastTS := b.idx.put(last)
		summaries = append(summaries, core.ThreadSummary{
			ChannelID:    string(root.RoomID),
			ChannelName:  roomName(room),
			ChannelType:  roomType(room),
			ThreadTS:     rootTS,
			ParentUserID: string(root.Sender),
			ParentText:   b.summaryText(ctx, root),
			ParentTS:     rootTS,
			ReplyCount:   len(replies),
			LastReplyTS:  lastTS,
			LastReplyBy:  string(last.Sender),
			Unread:       last.Sender != me && lastTS > b.lastReadTS(ctx, root.RoomID),
		})
	}
	return ui.ThreadsListLoadedMsg{TeamID: string(teamID), Summaries: summaries, SubscriptionsAvailable: true}
}

func (b *Backend) fetchActivity(teamID ids.TeamID, limit int, unreadOnly bool) core.Msg {
	ctx := b.ctx()
	if limit <= 0 || limit > viewLimit {
		limit = viewLimit
	}
	evts, err := b.cli.DB.Event.GetMentions(ctx, time.Now().Add(time.Minute), database.UnreadTypeHighlight, limit, "")
	if err != nil {
		return ui.ToastMsg{Text: "Loading activity failed: " + err.Error()}
	}
	me := b.UserID()
	lastRead := map[id.RoomID]string{}
	var items []core.ActivityItem
	for _, evt := range evts {
		if evt.Sender == me || !isTimelineMessage(evt) {
			continue
		}
		ts := b.idx.put(evt)
		lr, ok := lastRead[evt.RoomID]
		if !ok {
			lr = b.lastReadTS(ctx, evt.RoomID)
			lastRead[evt.RoomID] = lr
		}
		unread := ts > lr
		if unreadOnly && !unread {
			continue
		}
		item := core.ActivityItem{
			Key:       string(evt.ID),
			Type:      "at_user",
			IsUnread:  unread,
			FeedTS:    ts,
			ChannelID: string(evt.RoomID),
			TS:        ts,
			AuthorID:  string(evt.Sender),
		}
		if root := threadRoot(evt); root != "" {
			if rootTS, ok := b.tsForEvent(ctx, evt.RoomID, root); ok {
				item.ThreadTS = rootTS
			}
		}
		items = append(items, item)
	}
	return ui.ActivityListLoadedMsg{TeamID: string(teamID), Items: items}
}

func (b *Backend) hydrateActivity(teamID ids.TeamID, refs map[string][]string) core.Msg {
	ctx := b.ctx()
	bodies := map[string]core.ActivityMessage{}
	for channelID, tses := range refs {
		for _, ts := range tses {
			evt, err := b.eventForTS(ctx, id.RoomID(channelID), ts)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(b.summaryText(ctx, evt))
			bodies[core.ActivityMsgKey(channelID, ts)] = core.ActivityMessage{Text: text, UserID: string(evt.Sender)}
		}
	}
	return ui.ActivityBodiesLoadedMsg{TeamID: string(teamID), Bodies: bodies}
}
