package demo

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/schwagner/mtx/internal/core"
)

var testNow = time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local)

func fixedClock() time.Time { return testNow }

// tinyWorld is a two-team world small enough to reason about by hand.
func tinyWorld() *World {
	return newWorld([]teamSpec{{
		id: "T1", name: "Tiny Team", domain: "tiny", theme: "dark", selfID: "U1", responder: "U2", firstChannel: "C1",
		users: []user{{id: "U1", name: "Me Self"}, {id: "U2", name: "Other Person", presence: "active"}},
		channels: []channelSpec{
			{id: "C1", name: "one", typ: "channel", section: "S", unread: 1, mentions: 1, msgs: []msgSpec{
				{user: "U2", ago: 2 * time.Hour, text: "old news", replies: []msgSpec{{user: "U1", ago: 90 * time.Minute, text: "a reply"}}},
				{user: "U2", ago: time.Hour, text: "hey <@U1> NEW stuff", reactions: []reactSpec{{"eyes", []string{"U2"}}}},
			}},
			{id: "D1", name: "Other Person", typ: "dm", dmUserID: "U2"},
		},
	}, {
		id: "T2", name: "Second", domain: "second", theme: "nord", selfID: "U9", responder: "U8", firstChannel: "C9",
		users: []user{{id: "U9", name: "Me Again"}, {id: "U8", name: "Eight"}},
		channels: []channelSpec{
			{id: "C9", name: "nine", typ: "channel", unread: 1, msgs: []msgSpec{{user: "U8", ago: time.Minute, text: "hi"}}},
		},
	}}, testNow, fixedClock)
}

func TestNewWorldBuildsThreadsAndReadState(t *testing.T) {
	w := tinyWorld()
	msgs, lastRead := w.messages("C1")
	if len(msgs) != 2 {
		t.Fatalf("C1 has %d messages, want 2", len(msgs))
	}
	if msgs[0].ReplyCount != 1 || msgs[0].ThreadTS != msgs[0].TS {
		t.Errorf("thread parent = %+v, want ReplyCount 1 and ThreadTS == TS", msgs[0])
	}
	if lastRead != msgs[0].TS {
		t.Errorf("lastRead = %q, want the first message %q (one unread)", lastRead, msgs[0].TS)
	}
	if msgs[0].Timestamp != testNow.Add(-2*time.Hour).Format(TimestampFormat) {
		t.Errorf("Timestamp = %q", msgs[0].Timestamp)
	}
	if msgs[1].UserName != "Other Person" || msgs[1].Reactions[0].HasReacted {
		t.Errorf("message 2 = %+v, want Other Person's, with a reaction that is not the user's own", msgs[1])
	}
	rs := w.replies("C1", msgs[0].TS)
	if len(rs) != 1 || rs[0].ThreadTS != msgs[0].TS || rs[0].TS <= msgs[0].TS {
		t.Errorf("replies = %+v", rs)
	}
	st := w.readStates()
	if !st["C1"].HasUnread || st["C1"].MentionCount != 1 {
		t.Errorf("C1 read state = %+v, want unread with one mention", st["C1"])
	}
	if st["D1"].HasUnread {
		t.Errorf("D1 should be read: %+v", st["D1"])
	}
}

func TestMarkReadNeverMovesBackwards(t *testing.T) {
	w := tinyWorld()
	msgs, _ := w.messages("C1")
	latest := msgs[len(msgs)-1].TS
	w.markRead("C1", latest)
	if st := w.readStates()["C1"]; st.HasUnread || st.MentionCount != 0 {
		t.Fatalf("after markRead(latest): %+v", st)
	}
	w.markRead("C1", msgs[0].TS)
	if _, lastRead := w.messages("C1"); lastRead != latest {
		t.Fatalf("a late markRead moved the cursor back to %q", lastRead)
	}
	w.setLastRead("C1", msgs[0].TS)
	if !w.readStates()["C1"].HasUnread {
		t.Fatal("setLastRead (mark unread) must move the cursor back")
	}
}

func TestPostAppendsThreadsAndTracksUnread(t *testing.T) {
	w := tinyWorld()
	w.markRead("C1", w.latestTS("C1"))

	m, ok := w.post("C1", "", "U2", "ping <@U1>", false)
	if !ok || m.UserName != "Other Person" || m.Timestamp != testNow.Format(TimestampFormat) {
		t.Fatalf("post = %+v, %v", m, ok)
	}
	if st := w.readStates()["C1"]; !st.HasUnread || st.MentionCount != 1 {
		t.Errorf("after someone mentions me: %+v", st)
	}

	mine, _ := w.post("C1", "", "U1", "on it", false)
	if st := w.readStates()["C1"]; st.HasUnread || st.LastReadTS != mine.TS || st.MentionCount != 0 {
		t.Errorf("my own message must leave the channel read: %+v", st)
	}

	msgs, _ := w.messages("C1")
	parent := msgs[0]
	r, ok := w.post("C1", parent.TS, "U2", "threaded", false)
	if !ok || r.ThreadTS != parent.TS {
		t.Fatalf("reply = %+v, %v", r, ok)
	}
	after, _ := w.messages("C1")
	if after[0].ReplyCount != 2 || len(after) != len(msgs) {
		t.Errorf("reply must bump the parent and stay out of the feed: parent=%+v feed=%d", after[0], len(after))
	}
	if b, _ := w.post("C1", parent.TS, "U2", "broadcast", true); b.Subtype != "thread_broadcast" {
		t.Errorf("broadcast subtype = %q", b.Subtype)
	}
	if feed, _ := w.messages("C1"); len(feed) != len(msgs)+1 {
		t.Error("a broadcast reply must also land in the feed")
	}
	if _, ok := w.post("CNOPE", "", "U2", "x", false); ok {
		t.Error("post to an unknown channel succeeded")
	}
	if _, ok := w.post("C1", "1.000000", "U2", "x", false); ok {
		t.Error("reply to an unknown thread succeeded")
	}
	if _, ok := w.post("C1", "", "UNOPE", "x", false); ok {
		t.Error("post by an author outside the channel's team succeeded")
	}

	att := core.Attachment{Kind: "image", FileID: "F1", Thumbs: []core.ThumbSpec{{URL: "demo://f1", W: 4, H: 2}}}
	img, ok := w.post("C1", "", "U2", "look", false, att)
	if !ok || len(img.Attachments) != 1 || img.Attachments[0].FileID != "F1" {
		t.Fatalf("post with an attachment = %+v, %v", img, ok)
	}
	if feed, _ := w.messages("C1"); len(feed[len(feed)-1].Attachments) != 1 {
		t.Error("the World did not keep the attachment")
	}
}

func TestReactTogglesAndReportsTheUsersOwn(t *testing.T) {
	w := tinyWorld()
	ts := w.latestTS("C1")
	if !w.react("C1", ts, "U1", "eyes", false) {
		t.Fatal("react failed")
	}
	msgs, _ := w.messages("C1")
	if r := msgs[1].Reactions[0]; r.Count != 2 || !r.HasReacted {
		t.Fatalf("after my eyes: %+v", r)
	}
	w.react("C1", ts, "U1", "eyes", false)
	if msgs, _ = w.messages("C1"); msgs[1].Reactions[0].Count != 2 {
		t.Fatal("reacting twice must be idempotent")
	}
	w.react("C1", ts, "U1", "eyes", true)
	w.react("C1", ts, "U2", "eyes", true)
	if msgs, _ = w.messages("C1"); len(msgs[1].Reactions) != 0 {
		t.Fatalf("empty pill left behind: %+v", msgs[1].Reactions)
	}
	reply := w.replies("C1", msgs[0].TS)[0]
	if !w.react("C1", reply.TS, "U2", "tada", false) {
		t.Fatal("reacting to a thread reply failed")
	}
	if w.react("C1", "1.000000", "U2", "tada", false) {
		t.Fatal("reacting to an unknown message succeeded")
	}
}

func TestMessagesReturnsCopies(t *testing.T) {
	w := tinyWorld()
	held, _ := w.messages("C1")
	held[1].Reactions[0].UserIDs[0] = "MUTATED"
	held[1].Reactions[0].Count = 99
	w.react("C1", held[1].TS, "U1", "eyes", false)
	fresh, _ := w.messages("C1")
	if got := fresh[1].Reactions[0]; got.UserIDs[0] != "U2" || got.Count != 2 {
		t.Fatalf("World shares memory with a returned message: %+v", got)
	}
	if held[1].Reactions[0].Count != 99 {
		t.Fatal("World mutated a message the caller already held")
	}
}

func TestActiveTeamScopesReadStatesAndLookup(t *testing.T) {
	w := tinyWorld()
	if got := w.unreadTeams(); !slices.Equal(got, []string{"T1", "T2"}) {
		t.Errorf("unreadTeams = %v", got)
	}
	if _, ok := w.readStates()["C9"]; ok {
		t.Error("readStates leaked another team's channel")
	}
	if _, _, ok := w.lookup("C9"); ok {
		t.Error("lookup found a channel outside the active team")
	}
	if !w.setActive("T2") || w.activeTeam() != "T2" {
		t.Fatal("setActive(T2) failed")
	}
	if _, ok := w.readStates()["C9"]; !ok {
		t.Error("readStates missing the new active team")
	}
	if name, typ, ok := w.lookup("C9"); !ok || name != "nine" || typ != "channel" {
		t.Errorf("lookup(C9) = %q %q %v", name, typ, ok)
	}
	if w.setActive("TNOPE") || w.activeTeam() != "T2" {
		t.Error("setActive accepted an unknown team")
	}
}

func TestSearchIsNewestFirstAndCaseInsensitive(t *testing.T) {
	w := tinyWorld()
	if got := w.search("C1", "stuff"); len(got) != 1 || got[0] != w.latestTS("C1") {
		t.Errorf("search(stuff) = %v", got)
	}
	w.post("C1", "", "U2", "more Stuff here", false)
	if got := w.search("C1", "STUFF"); len(got) != 2 || got[0] != w.latestTS("C1") {
		t.Errorf("search must be case-insensitive and newest first: %v", got)
	}
	if got := w.search("C1", "  "); got != nil {
		t.Errorf("blank query = %v, want nil", got)
	}
}

func TestThreadSummariesListOnlyThreadsTheUserIsIn(t *testing.T) {
	w := tinyWorld()
	got := w.threadSummaries("T1")
	if len(got) != 1 || got[0].ChannelID != "C1" || got[0].ReplyCount != 1 || got[0].LastReplyBy != "U1" {
		t.Fatalf("summaries = %+v", got)
	}
	if got := w.threadSummaries("T2"); len(got) != 0 {
		t.Errorf("T2 summaries = %+v", got)
	}
}

func TestSnapshotShapesSidebarRows(t *testing.T) {
	s, ok := tinyWorld().snapshot("T1")
	if !ok || s.name != "Tiny Team" || s.selfID != "U1" || s.firstChannel != "C1" {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(s.channels) != 2 || s.channels[1].Presence != "active" || s.channels[1].DMUserID != "U2" {
		t.Errorf("channels = %+v", s.channels)
	}
	if len(s.finder) != 2 || !s.finder[0].Joined {
		t.Errorf("finder = %+v", s.finder)
	}
	if s.userNames["U2"] != "Other Person" {
		t.Errorf("userNames = %v", s.userNames)
	}
	if _, ok := tinyWorld().snapshot("TNOPE"); ok {
		t.Error("snapshot of an unknown team succeeded")
	}
}

func TestWorldIsSafeForConcurrentUse(t *testing.T) {
	w := tinyWorld()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m, _ := w.post("C1", "", "U2", "load", false)
				w.react("C1", m.TS, "U1", "eyes", j%2 == 0)
				w.messages("C1")
				w.readStates()
				w.unreadTeams()
				w.markRead("C1", m.TS)
				w.snapshots()
				w.threadSummaries("T1")
			}
		}()
	}
	wg.Wait()
}
