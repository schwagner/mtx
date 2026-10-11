package matrix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/ids"
	"github.com/schwagner/mtx/internal/ui"
)

// The integration test runs two clients against a real homeserver. It is
// skipped unless MTX_TEST_HOMESERVER names one with two existing accounts:
//
//	MTX_TEST_HOMESERVER=http://127.0.0.1:18008
//	MTX_TEST_USER1=@alice:localhost MTX_TEST_USER2=@bob:localhost
//	MTX_TEST_PASSWORD=testpass123   (shared by both)
//
// Both accounts must be fresh (no cross-signing yet) on the first run;
// later runs verify with the recovery keys saved next to the test DBs
// in MTX_TEST_STATE (default: a temp dir, i.e. fresh accounts every run).

type peer struct {
	t    *testing.T
	b    *Backend
	mu   sync.Mutex
	msgs []tea.Msg
}

func (p *peer) record(msg tea.Msg) {
	p.mu.Lock()
	p.msgs = append(p.msgs, msg)
	p.mu.Unlock()
}

// await waits for a recorded message matching match.
func (p *peer) await(what string, match func(tea.Msg) bool) tea.Msg {
	p.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		for _, m := range p.msgs {
			if match(m) {
				p.mu.Unlock()
				return m
			}
		}
		p.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	p.t.Fatalf("%s: timed out waiting for %s", p.b.UserID(), what)
	return nil
}

func env(t *testing.T, key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if def == "" {
		t.Skipf("%s not set", key)
	}
	return def
}

func newPeer(t *testing.T, stateDir, hs string, user id.UserID, password string) *peer {
	t.Helper()
	ctx := context.Background()
	local, _, _ := user.Parse()
	logFile, err := os.Create(filepath.Join(stateDir, local+".log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })
	log := zerolog.New(logFile).Level(zerolog.DebugLevel).With().Timestamp().Logger()
	b, err := Open(filepath.Join(stateDir, local+".db"), []byte("0123456789abcdef0123456789abcdef"), log)
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{t: t, b: b}
	b.SetSender(p.record)
	loaded, err := b.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(stateDir, local+".recovery")
	if loaded == "" {
		if err := b.Login(ctx, hs, user, password); err != nil {
			t.Fatalf("login %s: %v", user, err)
		}
	}
	if !b.Verification().IsVerified {
		if saved, err := os.ReadFile(keyPath); err == nil {
			if err := b.Verify(ctx, string(saved)); err != nil {
				t.Fatalf("verify %s: %v", user, err)
			}
		} else {
			if b.Verification().HasSSSS {
				t.Fatalf("%s already has secret storage but no saved recovery key", user)
			}
			key, err := b.Bootstrap(ctx, password)
			if err != nil {
				t.Fatalf("bootstrap %s: %v", user, err)
			}
			_ = os.WriteFile(keyPath, []byte(key), 0o600)
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := b.WaitSynced(waitCtx); err != nil {
		t.Fatalf("%s: first sync: %v", user, err)
	}
	return p
}

func TestIntegration(t *testing.T) {
	hs := env(t, "MTX_TEST_HOMESERVER", "")
	u1 := id.UserID(env(t, "MTX_TEST_USER1", "@alice:localhost"))
	u2 := id.UserID(env(t, "MTX_TEST_USER2", "@bob:localhost"))
	pw := env(t, "MTX_TEST_PASSWORD", "testpass123")
	stateDir := os.Getenv("MTX_TEST_STATE")
	if stateDir == "" {
		stateDir = t.TempDir()
	}
	ctx := context.Background()

	alice := newPeer(t, stateDir, hs, u1, pw)
	bob := newPeer(t, stateDir, hs, u2, pw)

	// alice creates an encrypted room and invites bob.
	resp, err := alice.b.cli.Client.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Name:   "mtx test " + time.Now().Format("15:04:05"),
		Preset: "private_chat",
		Invite: []id.UserID{u2},
		InitialState: []*event.Event{{
			Type:    event.StateEncryption,
			Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}},
		}},
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	room := resp.RoomID
	ch := ids.ChannelID(room)

	// bob sees the invite and accepts it through the channel service.
	bob.await("invite", func(m tea.Msg) bool { return bob.b.hasInvite(room) })
	if msg := bob.b.join(ch, "test"); msg == nil {
		t.Fatal("join returned nil")
	} else if f, ok := msg.(ui.ChannelJoinFailedMsg); ok {
		t.Fatalf("join: %v", f.Err)
	}
	alice.await("bob joined room list", func(m tea.Msg) bool { return alice.b.room(room) != nil })
	bob.await("room in bob's list", func(m tea.Msg) bool {
		o, ok := m.(ui.ConversationOpenedMsg)
		return ok && o.Item.ID == string(room)
	})
	// Let alice's client learn bob's membership (and device) before
	// sending, so the megolm session is shared with him.
	alice.await("bob's membership", func(m tea.Msg) bool {
		return alice.b.memberJoined(ctx, room, u2)
	})

	// --- send + receive (encrypted) ---
	sent := alice.b.sendMessage(ch, "hello **world** <@"+string(u2)+">")
	ms, ok := sent.(ui.MessageSentMsg)
	if !ok {
		t.Fatalf("send: got %#v", sent)
	}
	aRoot := ms.Message.TS // alice's ID for the root; bob's differs (rowids are per client)
	if !strings.Contains(ms.Message.Text, "*world*") {
		t.Errorf("sent text = %q, want bold world", ms.Message.Text)
	}
	got := bob.await("hello message", func(m tea.Msg) bool {
		n, ok := m.(ui.NewMessageMsg)
		return ok && n.ChannelID == string(room) && n.Message.UserID == string(u1) && !n.Message.IsEdited
	}).(ui.NewMessageMsg)
	rootTS := got.Message.TS // bob's ID for the root
	if got.Message.Text == "" || strings.Contains(got.Message.Text, "Unable to decrypt") {
		// The key may arrive just after the event: wait for the
		// in-place re-render.
		got = bob.await("decrypted hello", func(m tea.Msg) bool {
			n, ok := m.(ui.NewMessageMsg)
			return ok && n.Message.TS == rootTS && strings.Contains(n.Message.Text, "world")
		}).(ui.NewMessageMsg)
	}
	if !strings.Contains(got.Message.Text, "*world*") {
		t.Errorf("bob got %q", got.Message.Text)
	}
	if !strings.Contains(got.Message.Text, "*@bob*") && !strings.Contains(got.Message.Text, "*@") {
		t.Errorf("mention pill not rendered: %q", got.Message.Text)
	}

	// --- thread reply ---
	rep := bob.b.sendReply(ch, ids.ThreadTS(rootTS), "in a thread", false)
	if _, ok := rep.(ui.ThreadReplySentMsg); !ok {
		t.Fatalf("thread reply: %#v", rep)
	}
	alice.await("thread reply", func(m tea.Msg) bool {
		n, ok := m.(ui.NewMessageMsg)
		return ok && n.Message.ThreadTS == aRoot && n.Message.TS != aRoot && strings.Contains(n.Message.Text, "in a thread")
	})

	// --- reaction ---
	if err := bob.b.addReaction(ch, ids.MessageTS(rootTS), "+1"); err != nil {
		t.Fatalf("react: %v", err)
	}
	alice.await("reaction", func(m tea.Msg) bool {
		r, ok := m.(ui.ReactionAddedMsg)
		return ok && r.MessageTS == aRoot && r.UserID == string(u2) && r.Emoji == "+1"
	})

	// --- edit ---
	if e, _ := alice.b.editMessage(ch, ids.MessageTS(aRoot), "hello edited").(ui.MessageEditedMsg); e.Err != nil {
		t.Fatalf("edit: %v", e.Err)
	}
	bob.await("edit", func(m tea.Msg) bool {
		n, ok := m.(ui.NewMessageMsg)
		return ok && n.Message.IsEdited && n.Message.TS == rootTS && n.Message.Text == "hello edited"
	})

	// --- history: root shows the edit, reply count and reaction ---
	loaded, ok := bob.b.fetchChannel(ch, "").(ui.MessagesLoadedMsg)
	if !ok {
		t.Fatal("fetchChannel did not return MessagesLoadedMsg")
	}
	var root *struct{}
	for _, item := range loaded.Messages {
		if item.TS != rootTS {
			if item.ThreadTS != "" && item.ThreadTS != item.TS {
				t.Errorf("thread reply leaked into channel history: %+v", item)
			}
			continue
		}
		root = &struct{}{}
		if item.Text != "hello edited" || !item.IsEdited {
			t.Errorf("history root text = %q edited=%v", item.Text, item.IsEdited)
		}
		if item.ReplyCount != 1 || item.ThreadTS != rootTS {
			t.Errorf("history root reply count = %d thread=%q", item.ReplyCount, item.ThreadTS)
		}
		if len(item.Reactions) != 1 || item.Reactions[0].Emoji != "+1" || !item.Reactions[0].HasReacted {
			t.Errorf("history root reactions = %+v", item.Reactions)
		}
	}
	if root == nil {
		t.Fatalf("root message missing from history (%d msgs)", len(loaded.Messages))
	}
	thread := bob.b.fetchThread(ch, ids.ThreadTS(rootTS)).(ui.ThreadRepliesLoadedMsg)
	if len(thread.Replies) != 1 || thread.Replies[0].Text != "in a thread" {
		t.Errorf("thread replies = %+v", thread.Replies)
	}

	// --- reaction removal ---
	if err := bob.b.removeReaction(ch, ids.MessageTS(rootTS), "+1"); err != nil {
		t.Fatalf("unreact: %v", err)
	}
	alice.await("reaction removed", func(m tea.Msg) bool {
		r, ok := m.(ui.ReactionRemovedMsg)
		return ok && r.MessageTS == aRoot && r.Emoji == "+1"
	})

	// --- local search over decrypted text ---
	res := bob.b.searchChannel(ch, "hello").(ui.ChannelSearchResultsMsg)
	if res.Err != nil || len(res.TSes) == 0 {
		t.Errorf("search: err=%v tses=%v", res.Err, res.TSes)
	}

	// --- delete ---
	extra := alice.b.sendMessage(ch, "delete me").(ui.MessageSentMsg)
	bobExtra := bob.await("delete-me", func(m tea.Msg) bool {
		n, ok := m.(ui.NewMessageMsg)
		return ok && strings.Contains(n.Message.Text, "delete me")
	}).(ui.NewMessageMsg).Message.TS
	if d := alice.b.deleteMessage(ch, ids.MessageTS(extra.Message.TS)).(ui.MessageDeletedMsg); d.Err != nil {
		t.Fatalf("delete: %v", d.Err)
	}
	bob.await("deletion", func(m tea.Msg) bool {
		d, ok := m.(ui.WSMessageDeletedMsg)
		return ok && d.TS == bobExtra
	})

	// --- workspace snapshot and read state ---
	ws, err := bob.b.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range ws.Channels {
		if c.ID == string(room) {
			found = true
			if c.Type != "private" {
				t.Errorf("encrypted room type = %q", c.Type)
			}
		}
	}
	if !found {
		t.Errorf("room missing from workspace channels")
	}
	if ws.UserNames[string(u1)] == "" {
		t.Errorf("alice's name missing from user names")
	}

	// --- threads and activity views ---
	tl := bob.b.fetchThreads(ids.TeamID(u2)).(ui.ThreadsListLoadedMsg)
	if len(tl.Summaries) != 1 || tl.Summaries[0].ThreadTS != rootTS || tl.Summaries[0].ReplyCount != 1 {
		t.Errorf("threads list = %+v", tl.Summaries)
	}
	act := bob.b.fetchActivity(ids.TeamID(u2), 50, false).(ui.ActivityListLoadedMsg)
	if len(act.Items) == 0 || act.Items[0].TS != rootTS {
		t.Errorf("activity (mentions) = %+v", act.Items)
	}

	// --- direct messages ---
	opened := alice.b.openConversation([]string{string(u2)}, 7)().(ui.NewMessageOpenedMsg)
	if opened.AlreadyOpen || opened.ChannelID == "" {
		t.Fatalf("open DM: %+v", opened)
	}
	dm := id.RoomID(opened.ChannelID)
	alice.await("alice's DM marked direct", func(tea.Msg) bool {
		r := alice.b.room(dm)
		return r != nil && roomType(r) == "dm"
	})
	bob.await("DM invite", func(tea.Msg) bool { return bob.b.hasInvite(dm) })
	if inv := bob.b.pendingInvites(""); len(inv) != 1 || inv[0].ID != string(dm) {
		t.Errorf("pending invites = %+v", inv)
	}
	if j, ok := bob.b.join(ids.ChannelID(dm), "").(ui.ChannelJoinedMsg); !ok {
		t.Fatalf("join DM failed")
	} else if j.Name == "" {
		t.Errorf("joined DM has no name")
	}
	bob.await("DM marked direct", func(tea.Msg) bool {
		r := bob.b.room(dm)
		return r != nil && roomType(r) == "dm"
	})
	again := alice.b.openConversation([]string{string(u2)}, 8)().(ui.NewMessageOpenedMsg)
	if !again.AlreadyOpen || again.ChannelID != string(dm) {
		t.Errorf("reopening DM created a new room: %+v", again)
	}
}
