// Package matrix is mtx's engine: it runs a Matrix client (gomuks' hicli,
// on mautrix-go) and adapts it to the TUI's service ports and messages, the
// way cmd/slk adapted Slack.
package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"
	"go.mau.fi/gomuks/pkg/hicli"
	"go.mau.fi/gomuks/pkg/hicli/database"
	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/messages"
	"github.com/schwagner/mtx/internal/ui/sidebar"
)

// Notifier delivers desktop notifications for highlighted / pushed events.
type Notifier interface {
	Notify(title, body string) error
}

// Backend owns the hicli client and everything the TUI needs from it.
type Backend struct {
	cli *hicli.HiClient
	log zerolog.Logger
	idx *tsIndex

	TimestampFormat string
	Notifier        Notifier
	// DownloadDir is where attachment downloads are saved.
	DownloadDir string
	// RecentEmojiPath persists the reaction picker's recently used list.
	RecentEmojiPath string

	sendMu sync.RWMutex
	send   func(tea.Msg)

	mu      sync.Mutex
	rooms   map[id.RoomID]*database.Room
	invites map[id.RoomID]*database.InvitedRoom
	names   map[id.UserID]string
	// avatars are users' avatar mxc URLs, like names: the first room
	// that states one wins until a member event changes it.
	avatars map[id.UserID]id.ContentURIString
	// activeRoom is the room the user is looking at; its events don't
	// notify.
	activeRoom id.RoomID
	typingSent map[id.RoomID]time.Time
	// spaces are the top-level spaces, in rail order; roomSpaces maps a
	// room to the top-level spaces it is in (none: Home). activeWS is
	// the workspace the TUI shows ("" until a switch: Home). See
	// spaces.go.
	spaces     []id.RoomID
	roomSpaces map[id.RoomID][]id.RoomID
	activeWS   string

	// Sends block until the server echo of the event arrives through
	// sync, so the TUI gets the event's real timestamp-derived TS.
	txnMu   sync.Mutex
	waiters map[string]chan sendResult
	echoed  map[string]*database.Event
	sending atomic.Int32

	syncedOnce sync.Once
	synced     chan struct{}
}

type sendResult struct {
	evt *database.Event
	err error
}

// Open opens (creating if needed) the client database at dbPath. pickleKey
// encrypts the Olm/Megolm secrets at rest.
func Open(dbPath string, pickleKey []byte, log zerolog.Logger) (*Backend, error) {
	rawDB, err := openDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	b := &Backend{
		log:     log,
		idx:     newTSIndex(),
		rooms:   make(map[id.RoomID]*database.Room),
		invites: make(map[id.RoomID]*database.InvitedRoom),
		names:   make(map[id.UserID]string),
		avatars: make(map[id.UserID]id.ContentURIString),
		waiters: make(map[string]chan sendResult),
		echoed:  make(map[string]*database.Event),
		synced:  make(chan struct{}),
	}
	hicli.InitialDeviceDisplayName = "mtx"
	b.cli = hicli.New(rawDB, nil, log, pickleKey, b.handleEvent)
	return b, nil
}

// Load loads the stored account, if any. It returns the logged-in user ID
// or "" when no account is stored.
func (b *Backend) Load(ctx context.Context) (id.UserID, error) {
	if err := b.cli.DB.Upgrade(ctx); err != nil {
		return "", fmt.Errorf("upgrading database: %w", err)
	}
	userID, err := b.cli.DB.Account.GetFirstUserID(ctx)
	if err != nil {
		return "", err
	}
	if err := b.cli.Load(ctx, userID); err != nil {
		return "", err
	}
	// Start begins syncing only once the device is verified; until then
	// it just marks the client initialized.
	if err := b.cli.Start(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

// Client exposes the hicli client for login and verification flows.
func (b *Backend) Client() *hicli.HiClient { return b.cli }

// UserID is the logged-in user.
func (b *Backend) UserID() id.UserID {
	if b.cli.Account == nil {
		return ""
	}
	return b.cli.Account.UserID
}

// Stop stops syncing and closes the database.
func (b *Backend) Stop() {
	b.cli.Stop()
	_ = b.cli.DB.Close()
}

// SetSender installs the function that delivers messages to the TUI.
// Messages produced before it is set are dropped; the TUI loads current
// state on its own when the workspace becomes ready.
func (b *Backend) SetSender(send func(tea.Msg)) {
	b.sendMu.Lock()
	b.send = send
	b.sendMu.Unlock()
}

func (b *Backend) emit(msg tea.Msg) {
	b.sendMu.RLock()
	send := b.send
	b.sendMu.RUnlock()
	if send != nil && msg != nil {
		send(msg)
	}
}

// HasRooms reports whether the database already holds rooms, i.e. this
// is not the first start after login.
func (b *Backend) HasRooms(ctx context.Context) bool {
	var exists bool
	err := b.cli.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM room)`).Scan(&exists)
	return err == nil && exists
}

// WaitSynced blocks until the first sync after startup has been
// processed, or ctx ends.
func (b *Backend) WaitSynced(ctx context.Context) error {
	select {
	case <-b.synced:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Backend) ctx() context.Context {
	return b.log.WithContext(context.Background())
}

// ---------------------------------------------------------------------------
// hicli event handling

func (b *Backend) handleEvent(evt any) {
	switch e := evt.(type) {
	case *jsoncmd.SyncComplete:
		b.handleSync(e)
	case *jsoncmd.EventsDecrypted:
		b.handleDecrypted(e)
	case *jsoncmd.Typing:
		me := b.UserID()
		for _, u := range e.UserIDs {
			if u != me {
				b.emit(ui.UserTypingMsg{ChannelID: string(e.RoomID), UserID: string(u), WorkspaceID: string(me)})
			}
		}
	case *jsoncmd.SendComplete:
		if e.Event != nil && e.Error != nil {
			b.finishSend(e.Event.TransactionID, sendResult{err: e.Error})
		}
	case *jsoncmd.SyncStatus:
		switch e.Type {
		case jsoncmd.SyncStatusOK:
			b.emit(ui.ConnectionStateMsg{State: 1})
		case jsoncmd.SyncStatusErroring, jsoncmd.SyncStatusFailed:
			b.emit(ui.ConnectionStateMsg{State: 2})
		}
	}
}

func (b *Backend) handleSync(sc *jsoncmd.SyncComplete) {
	ctx := b.ctx()
	me := b.UserID()
	var newRooms, changedRooms []*database.Room
	readChanged, listChanged := false, false

	b.mu.Lock()
	railBefore := b.railItemsLocked()
	for _, roomID := range sc.LeftRooms {
		delete(b.rooms, roomID)
		delete(b.invites, roomID)
	}
	var newInvites []*database.InvitedRoom
	for _, inv := range sc.InvitedRooms {
		if _, seen := b.invites[inv.ID]; !seen {
			newInvites = append(newInvites, inv)
		}
		b.invites[inv.ID] = inv
	}
	for roomID, sr := range sc.Rooms {
		if sr.Meta == nil {
			continue
		}
		delete(b.invites, roomID)
		old, existed := b.rooms[roomID]
		b.rooms[roomID] = sr.Meta
		if !existed {
			newRooms = append(newRooms, sr.Meta)
			continue
		}
		if old.UnreadCounts != sr.Meta.UnreadCounts || !ptrEq(old.MarkedUnread, sr.Meta.MarkedUnread) {
			readChanged = true
		}
		oldItem, oldOK := b.channelItem(old)
		newItem, newOK := b.channelItem(sr.Meta)
		if oldOK != newOK || oldItem != newItem {
			// Renamed, became a DM, got tombstoned...: the sidebar
			// upserts by ID, and a full refresh drops vanished rows.
			if newOK && oldOK {
				changedRooms = append(changedRooms, sr.Meta)
			} else {
				listChanged = true
			}
		}
	}
	if len(sc.LeftRooms) > 0 {
		listChanged = true
	}
	b.mu.Unlock()

	for roomID, sr := range sc.Rooms {
		byRow := make(map[database.EventRowID]*database.Event, len(sr.Events))
		for _, evt := range sr.Events {
			byRow[evt.RowID] = evt
			if evt.Type == event.StateMember.Type && evt.StateKey != nil {
				b.learnMember(evt)
			}
		}
		for _, tuple := range sr.Timeline {
			evt := byRow[tuple.Event]
			if evt == nil {
				continue
			}
			evt.TimelineRowID = tuple.Timeline
			b.handleTimelineEvent(ctx, roomID, evt)
		}
	}

	for n := range sc.Notifications {
		if n.Event == nil || n.Event.Sender == me {
			continue
		}
		b.notify(n)
	}

	for _, inv := range newInvites {
		name, inviter := b.inviteInfo(inv)
		b.emit(ui.ToastMsg{Text: fmt.Sprintf("%s invited you to %s. Ctrl+T, search for it and press Enter to join.", inviter, name)})
	}
	// hicli reports space graph changes (and new top-level spaces) with
	// the sync that caused them; a joined or left room may also be a
	// space or in one.
	if len(sc.SpaceEdges) > 0 || len(sc.TopLevelSpaces) > 0 || len(newRooms) > 0 || len(sc.LeftRooms) > 0 {
		if _, roomsChanged, err := b.loadSpaces(ctx); err != nil {
			b.log.Warn().Err(err).Msg("loading spaces")
		} else if roomsChanged {
			listChanged = true
		}
	}
	b.mu.Lock()
	rail := b.railItemsLocked()
	active := b.activeWorkspaceLocked()
	activeGone := !b.knownWorkspaceLocked(active)
	b.mu.Unlock()
	if !slices.Equal(rail, railBefore) {
		b.emit(ui.WorkspacesChangedMsg{Items: rail})
	}
	if activeGone {
		// The user left the space they were looking at.
		b.emit(b.switchWorkspace(b.homeWorkspace()))
	} else {
		for _, room := range append(newRooms, changedRooms...) {
			if item, ok := b.channelItem(room); ok && b.inWorkspace(room.ID, active) {
				b.emit(ui.ConversationOpenedMsg{
					TeamID:     active,
					Item:       item,
					FinderItem: finderItem(item, true),
				})
			}
		}
		if listChanged {
			b.emit(ui.SectionsRefreshedMsg{TeamID: active, Channels: b.channelList(active)})
		}
	}
	if readChanged || len(newRooms) > 0 {
		b.emit(ui.ReadStateChangedMsg{WorkspaceID: string(me)})
	}
	b.syncedOnce.Do(func() {
		close(b.synced)
		go b.retryStoredSessions(ctx)
	})
}

func (b *Backend) learnMember(evt *database.Event) {
	var content event.MemberEventContent
	if json.Unmarshal(evt.Content, &content) != nil {
		return
	}
	userID := id.UserID(*evt.StateKey)
	if content.AvatarURL != "" {
		b.mu.Lock()
		b.avatars[userID] = content.AvatarURL
		b.mu.Unlock()
	}
	if content.Displayname == "" {
		return
	}
	b.mu.Lock()
	old, known := b.names[userID]
	b.names[userID] = content.Displayname
	b.mu.Unlock()
	if !known || old != content.Displayname {
		// Names are account-wide; the TUI takes them for its active
		// workspace.
		b.emit(ui.UserResolvedMsg{TeamID: b.activeWorkspace(), UserID: string(userID), DisplayName: content.Displayname})
	}
}

// handleTimelineEvent turns one live timeline event into TUI messages.
func (b *Backend) handleTimelineEvent(ctx context.Context, roomID id.RoomID, evt *database.Event) {
	channelID := string(roomID)
	switch effectiveType(evt) {
	case event.EventReaction.Type:
		if evt.RelatesTo == "" {
			return
		}
		key := reactionKeyOf(evt)
		ts, ok := b.tsForEvent(ctx, roomID, evt.RelatesTo)
		if key == "" || !ok {
			return
		}
		b.emit(ui.ReactionAddedMsg{ChannelID: channelID, MessageTS: ts, UserID: string(evt.Sender), Emoji: reactionName(key)})
		return
	case event.EventRedaction.Type:
		b.handleRedaction(ctx, roomID, evt)
		return
	}
	if evt.RelationType == event.RelReplace {
		b.emitEdit(ctx, roomID, evt)
		return
	}
	if !isTimelineMessage(evt) {
		return
	}
	if evt.TransactionID != "" && evt.Sender == b.UserID() {
		if b.finishSend(evt.TransactionID, sendResult{evt: evt}) {
			return
		}
	}
	items := b.convert(ctx, roomID, []*database.Event{evt})
	if len(items) == 0 {
		return
	}
	b.emit(ui.NewMessageMsg{ChannelID: channelID, Message: items[0]})
}

func reactionKeyOf(evt *database.Event) string {
	var content event.ReactionEventContent
	if json.Unmarshal(effectiveContent(evt), &content) != nil {
		return ""
	}
	return content.RelatesTo.Key
}

func (b *Backend) handleRedaction(ctx context.Context, roomID id.RoomID, evt *database.Event) {
	var content event.RedactionEventContent
	_ = json.Unmarshal(effectiveContent(evt), &content)
	redacts := content.Redacts
	if redacts == "" {
		var top struct {
			Redacts id.EventID `json:"redacts"`
		}
		_ = json.Unmarshal(evt.Content, &top)
		redacts = top.Redacts
	}
	if redacts == "" {
		return
	}
	target, err := b.cli.DB.Event.GetByID(ctx, roomID, redacts)
	if err != nil || target == nil {
		return
	}
	if effectiveType(target) == event.EventReaction.Type {
		key := reactionKeyOf(target)
		if ts, ok := b.tsForEvent(ctx, roomID, target.RelatesTo); ok && key != "" {
			b.emit(ui.ReactionRemovedMsg{ChannelID: string(roomID), MessageTS: ts, UserID: string(target.Sender), Emoji: reactionName(key)})
		}
		return
	}
	b.emit(ui.WSMessageDeletedMsg{ChannelID: string(roomID), TS: makeTS(target)})
}

func (b *Backend) emitEdit(ctx context.Context, roomID id.RoomID, edit *database.Event) {
	target, err := b.cli.DB.Event.GetByID(ctx, roomID, edit.RelatesTo)
	if err != nil || target == nil || target.Sender != edit.Sender {
		// Only the original sender may edit a message.
		return
	}
	content := parseMessage(edit)
	if content == nil {
		return
	}
	item := messages.MessageItem{
		TS:       b.idx.put(target),
		UserID:   string(target.Sender),
		Text:     messageText(content, b.displayName(target.Sender), b.displayName),
		IsEdited: true,
	}
	b.emit(ui.NewMessageMsg{ChannelID: string(roomID), Message: item})
}

func (b *Backend) handleDecrypted(ed *jsoncmd.EventsDecrypted) {
	ctx := b.ctx()
	for _, evt := range ed.Events {
		switch {
		case effectiveType(evt) == event.EventReaction.Type:
			b.handleTimelineEvent(ctx, ed.RoomID, evt)
		case evt.RelationType == event.RelReplace:
			b.emitEdit(ctx, ed.RoomID, evt)
		case isTimelineMessage(evt):
			// Re-render the placeholder row in place.
			items := b.convert(ctx, ed.RoomID, []*database.Event{evt})
			if len(items) == 0 {
				continue
			}
			item := items[0]
			item.IsEdited = true
			b.emit(ui.NewMessageMsg{ChannelID: string(ed.RoomID), Message: item})
		}
	}
}

// retryStoredSessions re-runs decryption for undecryptable events whose
// megolm session is already in the crypto store. hicli retries only at
// the moment a session arrives; if that retry is lost (the process exits
// first, or the save fails) the events stay undecryptable for good.
// Handing each such session back to hicli's received-session hook redoes
// it, and the result arrives as EventsDecrypted like any other.
func (b *Backend) retryStoredSessions(ctx context.Context) {
	if b.cli.Crypto == nil {
		return
	}
	type pending struct {
		room    id.RoomID
		session id.SessionID
	}
	rows, err := b.cli.DB.Query(ctx, `
		SELECT DISTINCT e.room_id, e.megolm_session_id FROM event e
		JOIN crypto_megolm_inbound_session s
		  ON s.room_id = e.room_id AND s.session_id = e.megolm_session_id
		WHERE e.decryption_error IS NOT NULL AND s.session IS NOT NULL`)
	if err != nil {
		b.log.Warn().Err(err).Msg("finding undecrypted events with stored sessions")
		return
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if rows.Scan(&p.room, &p.session) == nil {
			todo = append(todo, p)
		}
	}
	rows.Close()
	for _, p := range todo {
		sess, err := b.cli.Crypto.CryptoStore.GetGroupSession(ctx, p.room, p.session)
		if err != nil || sess == nil {
			continue
		}
		b.cli.Crypto.MarkSessionReceived(ctx, p.room, p.session, sess.Internal.FirstKnownIndex())
	}
	if len(todo) > 0 {
		b.log.Info().Int("sessions", len(todo)).Msg("retried decryption with stored sessions")
	}
}

func (b *Backend) notify(n jsoncmd.SyncNotification) {
	if b.Notifier == nil {
		return
	}
	b.mu.Lock()
	active := b.activeRoom
	room := b.rooms[n.Event.RoomID]
	b.mu.Unlock()
	if n.Event.RoomID == active {
		return
	}
	title := b.displayName(n.Event.Sender)
	if room != nil && room.DMUserID == nil {
		title += " in " + roomName(room)
	}
	body := "New message"
	if content := parseMessage(n.Event); content != nil && effectiveType(n.Event) == event.EventMessage.Type {
		if content.NewContent != nil {
			content = content.NewContent
		}
		content.RemoveReplyFallback()
		body = content.Body
	}
	_ = b.Notifier.Notify(title, body)
}

// ---------------------------------------------------------------------------
// conversion

// DisplayName is userID's display name, or its localpart when it has
// none.
func (b *Backend) DisplayName(userID string) string {
	return b.displayName(id.UserID(userID))
}

func (b *Backend) displayName(userID id.UserID) string {
	b.mu.Lock()
	name, ok := b.names[userID]
	b.mu.Unlock()
	if ok && name != "" {
		return name
	}
	if local, _, err := userID.Parse(); err == nil && local != "" {
		return local
	}
	return string(userID)
}

// tsForEvent returns the TS of an event by ID, consulting the index and
// then the database.
func (b *Backend) tsForEvent(ctx context.Context, roomID id.RoomID, evtID id.EventID) (string, bool) {
	if ts, ok := b.idx.ts(evtID); ok {
		return ts, true
	}
	evt, err := b.cli.DB.Event.GetByID(ctx, roomID, evtID)
	if err != nil || evt == nil {
		return "", false
	}
	return b.idx.put(evt), true
}

const eventColumns = `rowid, -1, room_id, event_id, sender, type, state_key, timestamp, content, decrypted, decrypted_type,
	unsigned, local_content, transaction_id, redacted_by, relates_to, relation_type,
	megolm_session_id, decryption_error, send_error, reactions, last_edit_rowid, unread_type, sticky_duration`

// eventForTS resolves a TS back to its event.
func (b *Backend) eventForTS(ctx context.Context, roomID id.RoomID, ts string) (*database.Event, error) {
	if evtID, ok := b.idx.event(roomID, ts); ok {
		evt, err := b.cli.DB.Event.GetByID(ctx, roomID, evtID)
		if err != nil || evt != nil {
			return evt, err
		}
	}
	ms, rowMod, ok := parseTS(ts)
	if !ok {
		return nil, fmt.Errorf("malformed message id %q", ts)
	}
	evt, err := b.cli.DB.Event.QueryOne(ctx,
		`SELECT `+eventColumns+` FROM event WHERE room_id = $1 AND timestamp = $2 AND rowid % 1000 = $3`,
		roomID, ms, rowMod)
	if err != nil {
		return nil, err
	} else if evt == nil {
		return nil, fmt.Errorf("unknown message %s", ts)
	}
	b.idx.put(evt)
	return evt, nil
}

// convert turns timeline events into TUI rows, batching the lookups for
// reply counts, reactions and edits. Events that aren't shown as rows are
// skipped. Order is preserved.
func (b *Backend) convert(ctx context.Context, roomID id.RoomID, evts []*database.Event) []messages.MessageItem {
	me := b.UserID()
	var ids []id.EventID
	for _, evt := range evts {
		if isTimelineMessage(evt) {
			ids = append(ids, evt.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	replyCounts := b.threadReplyCounts(ctx, roomID, ids)
	reactions, err := b.cli.DB.Event.GetReactions(ctx, roomID, ids...)
	if err != nil {
		b.log.Warn().Err(err).Msg("loading reactions")
	}

	items := make([]messages.MessageItem, 0, len(ids))
	for _, evt := range evts {
		if !isTimelineMessage(evt) {
			continue
		}
		ts := b.idx.put(evt)
		clock, date := formatTime(evt.Timestamp.Time, b.TimestampFormat)
		item := messages.MessageItem{
			TS:        ts,
			UserID:    string(evt.Sender),
			UserName:  b.displayName(evt.Sender),
			Timestamp: clock,
			DateStr:   date,
		}
		b.fillBody(ctx, roomID, evt, &item)
		if root := threadRoot(evt); root != "" {
			if rootTS, ok := b.tsForEvent(ctx, roomID, root); ok {
				item.ThreadTS = rootTS
			}
		} else if n := replyCounts[evt.ID]; n > 0 {
			item.ReplyCount = n
			item.ThreadTS = ts
		}
		if r := reactions[evt.ID]; r != nil {
			item.Reactions = reactionItems(r, me)
		}
		items = append(items, item)
	}
	return items
}

func (b *Backend) fillBody(ctx context.Context, roomID id.RoomID, evt *database.Event, item *messages.MessageItem) {
	if evt.RedactedBy != "" {
		item.Text = "_(message deleted)_"
		return
	}
	if effectiveType(evt) == event.EventEncrypted.Type {
		reason := "waiting for keys"
		if evt.DecryptionError != "" {
			reason = evt.DecryptionError
		}
		item.Text = "_🔒 Unable to decrypt message (" + slackEscaper.Replace(reason) + ")_"
		return
	}
	content := parseMessage(evt)
	if content == nil {
		item.Text = "_(unreadable message)_"
		return
	}
	shown := content
	if evt.LastEditRowID != nil {
		if edit, err := b.cli.DB.Event.GetByRowID(ctx, *evt.LastEditRowID); err == nil && edit != nil && edit.Sender == evt.Sender {
			if ec := parseMessage(edit); ec != nil && ec.NewContent != nil {
				shown = ec.NewContent
				item.IsEdited = true
			}
		}
	}
	text := messageText(shown, item.UserName, b.displayName)
	rel := content.RelatesTo
	if rel != nil && rel.InReplyTo != nil && !rel.IsFallingBack {
		text = b.replyQuote(ctx, roomID, rel.InReplyTo.EventID) + text
	}
	item.Text = text
	item.Attachments = attachmentsFor(evt, shown)
}

// replyQuote renders the "in reply to" line shown above a reply.
func (b *Backend) replyQuote(ctx context.Context, roomID id.RoomID, target id.EventID) string {
	evt, err := b.cli.DB.Event.GetByID(ctx, roomID, target)
	if err != nil || evt == nil {
		return "> _in reply to an earlier message_\n"
	}
	snippet := "…"
	if c := parseMessage(evt); c != nil {
		c.RemoveReplyFallback()
		snippet = strings.Join(strings.Fields(c.Body), " ")
		if r := []rune(snippet); len(r) > 80 {
			snippet = string(r[:80]) + "…"
		}
	}
	return "> *" + slackEscaper.Replace(b.displayName(evt.Sender)) + "*: " + slackEscaper.Replace(snippet) + "\n"
}

func reactionItems(r *database.GetReactionsResult, me id.UserID) []messages.ReactionItem {
	byKey := map[string]*messages.ReactionItem{}
	var order []string
	for _, evt := range r.Events {
		if evt.RedactedBy != "" {
			continue
		}
		key := reactionKeyOf(evt)
		if key == "" {
			continue
		}
		ri := byKey[key]
		if ri == nil {
			ri = &messages.ReactionItem{Emoji: reactionName(key)}
			byKey[key] = ri
			order = append(order, key)
		}
		if slices.Contains(ri.UserIDs, string(evt.Sender)) {
			continue
		}
		ri.Count++
		ri.UserIDs = append(ri.UserIDs, string(evt.Sender))
		if evt.Sender == me {
			ri.HasReacted = true
		}
	}
	out := make([]messages.ReactionItem, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

func (b *Backend) threadReplyCounts(ctx context.Context, roomID id.RoomID, roots []id.EventID) map[id.EventID]int {
	out := make(map[id.EventID]int)
	if len(roots) == 0 {
		return out
	}
	placeholders := make([]string, len(roots))
	args := make([]any, 0, len(roots)+1)
	args = append(args, roomID)
	for i, r := range roots {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, r)
	}
	rows, err := b.cli.DB.Query(ctx, `
		SELECT relates_to, COUNT(*) FROM event
		WHERE room_id = $1 AND relation_type = 'm.thread' AND redacted_by IS NULL
		  AND relates_to IN (`+strings.Join(placeholders, ",")+`)
		GROUP BY relates_to`, args...)
	if err != nil {
		b.log.Warn().Err(err).Msg("counting thread replies")
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var root id.EventID
		var n int
		if rows.Scan(&root, &n) == nil {
			out[root] = n
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// rooms

func roomName(r *database.Room) string {
	if r.Name != nil && *r.Name != "" {
		return *r.Name
	}
	if r.CanonicalAlias != nil {
		return string(*r.CanonicalAlias)
	}
	return string(r.ID)
}

func isSpace(r *database.Room) bool {
	return r.CreationContent != nil && r.CreationContent.Type == event.RoomTypeSpace
}

func roomType(r *database.Room) string {
	switch {
	case r.DMUserID != nil:
		return "dm"
	case r.EncryptionEvent != nil:
		return "private"
	default:
		return "channel"
	}
}

func (b *Backend) channelItem(r *database.Room) (sidebar.ChannelItem, bool) {
	if isSpace(r) || r.Tombstone != nil {
		return sidebar.ChannelItem{}, false
	}
	item := sidebar.ChannelItem{
		ID:   string(r.ID),
		Name: roomName(r),
		Type: roomType(r),
	}
	if r.DMUserID != nil {
		item.DMUserID = string(*r.DMUserID)
	}
	return item, true
}

func finderItem(item sidebar.ChannelItem, joined bool) core.ChannelFinderItem {
	return core.ChannelFinderItem{ID: item.ID, Name: item.Name, Type: item.Type, Joined: joined}
}

// loadRooms reads every joined room and member name from the database.
func (b *Backend) loadRooms(ctx context.Context) error {
	rooms := map[id.RoomID]*database.Room{}
	for sc := range b.cli.GetInitialSync(ctx, 100, 0) {
		for roomID, sr := range sc.Rooms {
			if sr.Meta != nil {
				rooms[roomID] = sr.Meta
			}
		}
	}
	names := map[id.UserID]string{}
	avatars := map[id.UserID]id.ContentURIString{}
	rows, err := b.cli.DB.Query(ctx, `
		SELECT cs.state_key, json_extract(e.content, '$.displayname'),
			json_extract(e.content, '$.avatar_url')
		FROM current_state cs JOIN event e ON e.rowid = cs.event_rowid
		WHERE cs.event_type = 'm.room.member' AND cs.membership = 'join'`)
	if err != nil {
		return fmt.Errorf("loading member names: %w", err)
	}
	for rows.Next() {
		var userID id.UserID
		var name, avatar *string
		if rows.Scan(&userID, &name, &avatar) != nil {
			continue
		}
		if _, seen := names[userID]; !seen && name != nil && *name != "" {
			names[userID] = *name
		}
		if _, seen := avatars[userID]; !seen && avatar != nil && *avatar != "" {
			avatars[userID] = id.ContentURIString(*avatar)
		}
	}
	rows.Close()
	invites, err := b.cli.DB.InvitedRoom.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("loading invites: %w", err)
	}
	b.mu.Lock()
	for k, v := range rooms {
		b.rooms[k] = v
	}
	for k, v := range names {
		if _, ok := b.names[k]; !ok {
			b.names[k] = v
		}
	}
	for k, v := range avatars {
		if _, ok := b.avatars[k]; !ok {
			b.avatars[k] = v
		}
	}
	for _, inv := range invites {
		b.invites[inv.ID] = inv
	}
	b.mu.Unlock()
	return nil
}

// channelList returns workspace ws's sidebar rows, most recently active
// first. ws "" lists every room.
func (b *Backend) channelList(ws string) []sidebar.ChannelItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.channelListLocked(ws)
}

func (b *Backend) channelListLocked(ws string) []sidebar.ChannelItem {
	rooms := make([]*database.Room, 0, len(b.rooms))
	for _, r := range b.rooms {
		if ws == "" || b.inWorkspaceLocked(r.ID, ws) {
			rooms = append(rooms, r)
		}
	}
	sort.Slice(rooms, func(i, j int) bool {
		return rooms[i].SortingTimestamp.After(rooms[j].SortingTimestamp.Time)
	})
	var items []sidebar.ChannelItem
	for _, r := range rooms {
		if item, ok := b.channelItem(r); ok {
			items = append(items, item)
		}
	}
	return items
}

func ptrEq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Workspace builds the TUI's initial workspace snapshot.
func (b *Backend) Workspace(ctx context.Context) (ui.WorkspaceReadyMsg, error) {
	if err := b.loadRooms(ctx); err != nil {
		return ui.WorkspaceReadyMsg{}, err
	}
	if _, _, err := b.loadSpaces(ctx); err != nil {
		return ui.WorkspaceReadyMsg{}, fmt.Errorf("loading spaces: %w", err)
	}
	ws := b.initialWorkspace()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeWS = ws
	channels := b.channelListLocked(ws)
	var lastChannel string
	if len(channels) > 0 {
		lastChannel = channels[0].ID
	}
	_, server, _ := b.UserID().Parse()
	return ui.WorkspaceReadyMsg{
		TeamID: ws,
		// TeamName names the startup overlay's entry, which is the
		// homeserver whichever workspace opens first.
		TeamName:      server,
		Channels:      channels,
		FinderItems:   b.finderItemsLocked(),
		UserNames:     b.userNamesLocked(),
		UserID:        string(b.UserID()),
		InitialActive: true,
		LastChannelID: lastChannel,
	}, nil
}

// ReadStates reports per-room unread state for the sidebar.
func (b *Backend) ReadStates() map[string]core.ReadState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]core.ReadState, len(b.rooms))
	now := time.Now()
	for roomID, r := range b.rooms {
		mentions := r.UnreadHighlights
		if r.DMUserID != nil {
			mentions = r.UnreadNotifications
		}
		// The sidebar hides conversations whose LastReadTS is older than
		// hide_inactive_after_days, and treats an empty one on a DM as a
		// closed Slack conversation. Matrix has no closed DMs, so report
		// the room's last activity: a read room was last read no earlier
		// than that, and an unread one is never hidden.
		last := r.SortingTimestamp.Time
		if r.SortingTimestamp.UnixMilli() <= 0 {
			last = now // no events yet: a new room, not an abandoned one
		}
		out[string(roomID)] = core.ReadState{
			HasUnread:    roomUnread(r),
			MentionCount: mentions,
			LastReadTS:   fmt.Sprintf("%d.000000", last.Unix()),
		}
	}
	return out
}

// roomUnread reports whether a room shows as unread.
func roomUnread(r *database.Room) bool {
	return r.UnreadMessages > 0 || (r.MarkedUnread != nil && *r.MarkedUnread)
}

// directInviter returns who invited the user to roomID if the invite is
// marked as a direct chat.
func (b *Backend) directInviter(roomID id.RoomID) (id.UserID, bool) {
	b.mu.Lock()
	inv := b.invites[roomID]
	b.mu.Unlock()
	if inv == nil {
		return "", false
	}
	me := string(b.UserID())
	for _, st := range inv.InviteState {
		if st.Type == event.StateMember && st.StateKey != nil && *st.StateKey == me {
			if d, ok := st.Content.Raw["is_direct"].(bool); ok && d {
				return st.Sender, true
			}
		}
	}
	return "", false
}

// inviteInfo returns an invite's display name and the inviter's name.
// Callers may hold b.mu.
func (b *Backend) inviteInfo(inv *database.InvitedRoom) (name, inviter string) {
	me := string(b.UserID())
	isDirect := false
	for _, st := range inv.InviteState {
		switch {
		case st.Type == event.StateRoomName:
			if n, ok := st.Content.Raw["name"].(string); ok {
				name = n
			}
		case st.Type == event.StateMember && st.StateKey != nil && *st.StateKey == me:
			inviter = string(st.Sender)
			if d, ok := st.Content.Raw["is_direct"].(bool); ok && d {
				isDirect = true
			}
		}
	}
	if inviter != "" {
		if n, ok := b.names[id.UserID(inviter)]; ok && n != "" {
			inviter = n
		} else if local, _, err := id.UserID(inviter).Parse(); err == nil {
			inviter = local
		}
	} else {
		inviter = "Someone"
	}
	if name == "" {
		if isDirect {
			name = "a direct chat with " + inviter
		} else {
			name = string(inv.ID)
		}
	}
	return name, inviter
}

func (b *Backend) inviteFinderItem(inv *database.InvitedRoom) core.ChannelFinderItem {
	name, inviter := b.inviteInfo(inv)
	return core.ChannelFinderItem{ID: string(inv.ID), Name: name + " (invite from " + inviter + ")", Type: "channel"}
}

// pendingInvites returns finder items for invites matching query.
func (b *Backend) pendingInvites(query string) []core.ChannelFinderItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := strings.ToLower(query)
	var out []core.ChannelFinderItem
	for _, inv := range b.invites {
		item := b.inviteFinderItem(inv)
		if q == "" || strings.Contains(strings.ToLower(item.Name), q) {
			out = append(out, item)
		}
	}
	return out
}

func (b *Backend) hasInvite(roomID id.RoomID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.invites[roomID]
	return ok
}

// memberJoined reports whether the local state shows userID joined.
func (b *Backend) memberJoined(ctx context.Context, roomID id.RoomID, userID id.UserID) bool {
	var n int
	err := b.cli.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM current_state
		WHERE room_id = $1 AND event_type = 'm.room.member' AND state_key = $2 AND membership = 'join'`,
		roomID, userID).Scan(&n)
	return err == nil && n > 0
}

func (b *Backend) room(roomID id.RoomID) *database.Room {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rooms[roomID]
}

// ---------------------------------------------------------------------------
// sending

var errSendTimeout = errors.New("timed out waiting for the server to confirm the message")

// sendAndWait runs send (which must return the pending local event) and
// waits for its echo from the server.
func (b *Backend) sendAndWait(send func() (*database.Event, error)) (*database.Event, error) {
	b.sending.Add(1)
	defer b.sending.Add(-1)
	pending, err := send()
	if err != nil {
		return nil, err
	}
	if pending == nil || pending.TransactionID == "" {
		if pending != nil && pending.LocalContent != nil && pending.LocalContent.SanitizedHTML != "" {
			return nil, errors.New(pending.LocalContent.SanitizedHTML)
		}
		return nil, errors.New("message was not sent")
	}
	txn := pending.TransactionID
	ch := make(chan sendResult, 1)
	b.txnMu.Lock()
	if evt, ok := b.echoed[txn]; ok {
		delete(b.echoed, txn)
		b.txnMu.Unlock()
		return evt, nil
	}
	b.waiters[txn] = ch
	b.txnMu.Unlock()
	select {
	case res := <-ch:
		return res.evt, res.err
	case <-time.After(60 * time.Second):
		b.txnMu.Lock()
		delete(b.waiters, txn)
		b.txnMu.Unlock()
		return nil, errSendTimeout
	}
}

// finishSend delivers a send's result to its waiter. It reports whether a
// send owns the event, in which case the event must not also be delivered
// as a new message.
//
// Transaction IDs are only echoed to the device that sent the event, so an
// own event carrying one was sent by this process (or a previous run).
// While a send is in flight, an echo with no waiter yet is stashed for
// sendAndWait to pick up: it can beat the waiter's registration.
func (b *Backend) finishSend(txn string, res sendResult) bool {
	if txn == "" {
		return false
	}
	b.txnMu.Lock()
	defer b.txnMu.Unlock()
	if ch, ok := b.waiters[txn]; ok {
		delete(b.waiters, txn)
		ch <- res
		return true
	}
	if res.evt != nil && b.sending.Load() > 0 {
		b.echoed[txn] = res.evt
		return true
	}
	return false
}
