package matrix

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ids"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/messages"
)

// pageSize is how many timeline events one history page asks for. Many
// events (reactions, edits, state) aren't rows, so a page is retried until
// it yields enough rows or history runs out.
const (
	pageSize    = 60
	minPageRows = 25
	maxPageTry  = 6
)

// Wire installs the backend's services on the App.
func (b *Backend) Wire(app *ui.App) {
	app.SetChannelService(core.NewChannelService(core.ChannelServiceFuncs{
		Fetch:       b.fetchChannel,
		FetchOlder:  b.fetchOlder,
		ReadCache:   b.readCache,
		MarkRead:    b.markRead,
		Lookup:      b.lookup,
		Join:        b.join,
		RecordVisit: b.recordVisit,
	}))
	app.SetMessageService(core.NewMessageService(core.MessageServiceFuncs{
		Send:   b.sendMessage,
		Edit:   b.editMessage,
		Delete: b.deleteMessage,
	}))
	app.SetThreadService(core.NewThreadService(core.ThreadServiceFuncs{
		Fetch:     b.fetchThread,
		CacheRead: b.threadCache,
		SendReply: b.sendReply,
	}))
	app.SetReactionService(core.NewReactionService(b.addReaction, b.removeReaction, nil, nil))
	app.SetPresenceService(core.NewPresenceService(nil, b.sendTyping))
	app.SetUnreadService(core.NewUnreadService(b.ReadStates, nil))
	app.SetSearchService(core.NewSearchService(core.SearchServiceFuncs{
		SearchChannel: b.searchChannel,
	}))
	app.SetFileService(core.NewFileService(b.upload, b.download))
}

// ---------------------------------------------------------------------------
// history

// page collects up to minPageRows channel rows (thread replies excluded)
// older than before (0 = newest), oldest first.
func (b *Backend) page(ctx context.Context, roomID id.RoomID, before database.TimelineRowID) ([]*database.Event, error) {
	var collected []*database.Event
	rows := 0
	for try := 0; try < maxPageTry && rows < minPageRows; try++ {
		resp, err := b.cli.Paginate(ctx, roomID, before, pageSize, false)
		if err != nil {
			return nil, err
		}
		if len(resp.Events) == 0 {
			break
		}
		// resp.Events is newest first.
		for _, evt := range resp.Events {
			if evt.TimelineRowID != 0 && (before == 0 || evt.TimelineRowID < before) {
				before = evt.TimelineRowID
			}
			collected = append(collected, evt)
			if isTimelineMessage(evt) && threadRoot(evt) == "" {
				rows++
			}
		}
		if !resp.HasMore {
			break
		}
	}
	slices.Reverse(collected)
	var out []*database.Event
	for _, evt := range collected {
		if threadRoot(evt) == "" {
			out = append(out, evt)
		}
	}
	return out, nil
}

func (b *Backend) fetchChannel(channelID ids.ChannelID, _ string) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	b.mu.Lock()
	b.activeRoom = roomID
	b.mu.Unlock()
	evts, err := b.page(ctx, roomID, 0)
	if err != nil {
		return ui.ToastMsg{Text: "Loading messages failed: " + err.Error()}
	}
	items := b.convert(ctx, roomID, evts)
	if items == nil {
		items = []messages.MessageItem{}
	}
	lastRead := b.lastReadTS(ctx, roomID)
	var marked string
	if n := len(items); n > 0 {
		marked = items[n-1].TS
		go b.markRead(channelID, ids.MessageTS(marked))
	}
	return ui.MessagesLoadedMsg{ChannelID: string(channelID), Messages: items, LastReadTS: lastRead, MarkedTS: marked}
}

func (b *Backend) fetchOlder(channelID ids.ChannelID, oldestTS ids.MessageTS) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	row, ok := b.idx.timelineRow(roomID, string(oldestTS))
	if !ok {
		evt, err := b.eventForTS(ctx, roomID, string(oldestTS))
		if err != nil {
			return ui.OlderMessagesLoadedMsg{ChannelID: string(channelID), AnchorTS: string(oldestTS)}
		}
		err = b.cli.DB.QueryRow(ctx, `SELECT rowid FROM timeline WHERE room_id = $1 AND event_rowid = $2`, roomID, evt.RowID).Scan(&row)
		if err != nil {
			return ui.OlderMessagesLoadedMsg{ChannelID: string(channelID), AnchorTS: string(oldestTS)}
		}
	}
	evts, err := b.page(ctx, roomID, row)
	if err != nil {
		return ui.ToastMsg{Text: "Loading history failed: " + err.Error()}
	}
	return ui.OlderMessagesLoadedMsg{
		ChannelID: string(channelID),
		AnchorTS:  string(oldestTS),
		Messages:  b.convert(ctx, roomID, evts),
	}
}

// readCache serves the latest page straight from the local database so a
// channel switch paints before Fetch returns.
func (b *Backend) readCache(channelID ids.ChannelID) []core.MessageItem {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	evts, err := b.cli.DB.Timeline.Get(ctx, roomID, pageSize, 0)
	if err != nil || len(evts) == 0 {
		return nil
	}
	slices.Reverse(evts)
	var rows []*database.Event
	for _, evt := range evts {
		if threadRoot(evt) == "" {
			rows = append(rows, evt)
		}
	}
	return b.convert(ctx, roomID, rows)
}

// lastReadTS is the TS of the event the user's own read receipt points at.
func (b *Backend) lastReadTS(ctx context.Context, roomID id.RoomID) string {
	var evtID id.EventID
	err := b.cli.DB.QueryRow(ctx, `
		SELECT event_id FROM receipt
		WHERE room_id = $1 AND user_id = $2 AND receipt_type = 'm.read' AND thread_id IN ('', 'main')
		ORDER BY timestamp DESC LIMIT 1`, roomID, b.UserID()).Scan(&evtID)
	if err != nil || evtID == "" {
		return ""
	}
	ts, _ := b.tsForEvent(ctx, roomID, evtID)
	return ts
}

func (b *Backend) markRead(channelID ids.ChannelID, ts ids.MessageTS) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	evt, err := b.eventForTS(ctx, roomID, string(ts))
	if err != nil {
		return nil
	}
	if err := b.cli.MarkRead(ctx, roomID, evt.ID, event.ReceiptTypeRead); err != nil {
		b.log.Warn().Err(err).Msg("marking room read")
		return nil
	}
	b.mu.Lock()
	if r := b.rooms[roomID]; r != nil {
		cp := *r
		cp.UnreadCounts = database.UnreadCounts{}
		b.rooms[roomID] = &cp
	}
	b.mu.Unlock()
	b.emit(ui.ReadStateChangedMsg{WorkspaceID: string(b.UserID()), ChannelID: string(channelID)})
	return ui.ChannelMarkedReadMsg{ChannelID: string(channelID)}
}

func (b *Backend) lookup(channelID ids.ChannelID) (string, string, bool) {
	r := b.room(id.RoomID(channelID))
	if r == nil {
		return "", "", false
	}
	return roomName(r), roomType(r), true
}

func (b *Backend) recordVisit(channelID ids.ChannelID) {
	b.mu.Lock()
	b.activeRoom = id.RoomID(channelID)
	b.mu.Unlock()
}

// join accepts an invite (or joins a public room by ID).
func (b *Backend) join(channelID ids.ChannelID, name string) core.Msg {
	ctx := b.ctx()
	_, err := b.cli.Client.JoinRoomByID(ctx, id.RoomID(channelID))
	if err != nil {
		return ui.ChannelJoinFailedMsg{ID: string(channelID), Name: name, Err: err}
	}
	return ui.ChannelJoinedMsg{ID: string(channelID), Name: strings.TrimSuffix(name, " (invite)")}
}

// ---------------------------------------------------------------------------
// threads

func (b *Backend) threadEvents(ctx context.Context, roomID id.RoomID, threadTS string) (*database.Event, []*database.Event, error) {
	root, err := b.eventForTS(ctx, roomID, threadTS)
	if err != nil {
		return nil, nil, err
	}
	replies, err := b.cli.DB.Event.GetRelatedEvents(ctx, roomID, root.ID, event.RelThread, "")
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(replies, func(a, c *database.Event) int {
		return a.Timestamp.Compare(c.Timestamp.Time)
	})
	return root, replies, nil
}

func (b *Backend) fetchThread(channelID ids.ChannelID, threadTS ids.ThreadTS) core.Msg {
	ctx := b.ctx()
	_, replies, err := b.threadEvents(ctx, id.RoomID(channelID), string(threadTS))
	if err != nil {
		return ui.ThreadRepliesLoadedMsg{ThreadTS: string(threadTS)}
	}
	items := b.convert(ctx, id.RoomID(channelID), replies)
	if items == nil {
		items = []messages.MessageItem{}
	}
	return ui.ThreadRepliesLoadedMsg{ThreadTS: string(threadTS), Replies: items}
}

func (b *Backend) threadCache(channelID ids.ChannelID, threadTS ids.ThreadTS) []core.MessageItem {
	ctx := b.ctx()
	root, replies, err := b.threadEvents(ctx, id.RoomID(channelID), string(threadTS))
	if err != nil {
		return nil
	}
	return b.convert(ctx, id.RoomID(channelID), append([]*database.Event{root}, replies...))
}

// ---------------------------------------------------------------------------
// sending

// slackMentionRe matches the <@USER> tokens the composer substitutes for
// picked mentions.
var slackMentionRe = regexp.MustCompile(`<@(@[^>\s]+:[^>\s]+)>`)

// prepareText converts composer markup into Markdown for hicli, returning
// the mentioned users.
func (b *Backend) prepareText(text string) (string, *event.Mentions) {
	mentions := &event.Mentions{}
	text = slackMentionRe.ReplaceAllStringFunc(text, func(m string) string {
		userID := id.UserID(slackMentionRe.FindStringSubmatch(m)[1])
		mentions.Add(userID)
		return fmt.Sprintf("[%s](%s)", b.displayName(userID), userID.URI().MatrixToURL())
	})
	for _, special := range []string{"<!here>", "<!channel>", "<!everyone>"} {
		if strings.Contains(text, special) {
			mentions.Room = true
			text = strings.ReplaceAll(text, special, "@room")
		}
	}
	// A leading slash is a command to hicli; sending text that merely
	// starts with one needs the escape.
	if strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "/me ") && !strings.HasPrefix(text, "/notice ") {
		text = "/" + text
	}
	return text, mentions
}

func (b *Backend) sendText(ctx context.Context, roomID id.RoomID, text string, rel *event.RelatesTo) (messages.MessageItem, error) {
	body, mentions := b.prepareText(text)
	evt, err := b.sendAndWait(func() (*database.Event, error) {
		return b.cli.SendMessage(ctx, roomID, nil, nil, body, rel, mentions, nil)
	})
	if err != nil {
		return messages.MessageItem{}, err
	}
	items := b.convert(ctx, roomID, []*database.Event{evt})
	if len(items) == 0 {
		return messages.MessageItem{}, fmt.Errorf("sent event is not displayable")
	}
	return items[0], nil
}

func (b *Backend) sendMessage(channelID ids.ChannelID, text string) core.Msg {
	item, err := b.sendText(b.ctx(), id.RoomID(channelID), text, nil)
	if err != nil {
		return ui.MessageSendFailedMsg{ChannelID: string(channelID), Reason: err.Error()}
	}
	return ui.MessageSentMsg{ChannelID: string(channelID), Message: item}
}

func (b *Backend) sendReply(channelID ids.ChannelID, threadTS ids.ThreadTS, text string, _ bool) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	root, replies, err := b.threadEvents(ctx, roomID, string(threadTS))
	if err != nil {
		return ui.ThreadReplySendFailedMsg{ChannelID: string(channelID), ThreadTS: string(threadTS), Reason: err.Error()}
	}
	// Thread replies carry a fallback reply to the latest event in the
	// thread for clients without thread support.
	latest := root.ID
	if n := len(replies); n > 0 {
		latest = replies[n-1].ID
	}
	rel := (&event.RelatesTo{}).SetThread(root.ID, latest)
	item, err := b.sendText(ctx, roomID, text, rel)
	if err != nil {
		return ui.ThreadReplySendFailedMsg{ChannelID: string(channelID), ThreadTS: string(threadTS), Reason: err.Error()}
	}
	return ui.ThreadReplySentMsg{ChannelID: string(channelID), ThreadTS: string(threadTS), Message: item}
}

func (b *Backend) editMessage(channelID ids.ChannelID, ts ids.MessageTS, text string) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	target, err := b.eventForTS(ctx, roomID, string(ts))
	if err == nil {
		body, mentions := b.prepareText(text)
		_, err = b.cli.SendMessage(ctx, roomID, nil, nil, body, (&event.RelatesTo{}).SetReplace(target.ID), mentions, nil)
	}
	return ui.MessageEditedMsg{ChannelID: string(channelID), TS: string(ts), Err: err}
}

func (b *Backend) deleteMessage(channelID ids.ChannelID, ts ids.MessageTS) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	target, err := b.eventForTS(ctx, roomID, string(ts))
	if err == nil {
		_, err = b.cli.Client.RedactEvent(ctx, roomID, target.ID)
	}
	return ui.MessageDeletedMsg{ChannelID: string(channelID), TS: string(ts), Err: err}
}

func (b *Backend) addReaction(channelID ids.ChannelID, ts ids.MessageTS, emojiName string) error {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	target, err := b.eventForTS(ctx, roomID, string(ts))
	if err != nil {
		return err
	}
	content := &event.ReactionEventContent{RelatesTo: event.RelatesTo{
		Type:    event.RelAnnotation,
		EventID: target.ID,
		Key:     reactionKey(emojiName),
	}}
	_, err = b.cli.Send(ctx, roomID, event.EventReaction, content, false, true)
	return err
}

func (b *Backend) removeReaction(channelID ids.ChannelID, ts ids.MessageTS, emojiName string) error {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	target, err := b.eventForTS(ctx, roomID, string(ts))
	if err != nil {
		return err
	}
	res, err := b.cli.DB.Event.GetReactions(ctx, roomID, target.ID)
	if err != nil {
		return err
	}
	me := b.UserID()
	for _, evt := range res[target.ID].Events {
		if evt.Sender == me && evt.RedactedBy == "" && reactionName(reactionKeyOf(evt)) == emojiName {
			_, err = b.cli.Client.RedactEvent(ctx, roomID, evt.ID)
			return err
		}
	}
	return fmt.Errorf("no reaction to remove")
}

// typingSent throttles typing notifications to one per few seconds per room.
func (b *Backend) sendTyping(channelID string) {
	b.mu.Lock()
	if b.typingSent == nil {
		b.typingSent = map[id.RoomID]time.Time{}
	}
	roomID := id.RoomID(channelID)
	if time.Since(b.typingSent[roomID]) < 4*time.Second {
		b.mu.Unlock()
		return
	}
	b.typingSent[roomID] = time.Now()
	b.mu.Unlock()
	go func() {
		_ = b.cli.SetTyping(b.ctx(), roomID, 6*time.Second)
	}()
}
