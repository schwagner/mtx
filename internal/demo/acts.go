package demo

import (
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
)

// here stands for the triggering event's channel in an act's arguments.
const here = ""

// anyChannel makes on() match an event kind in every channel.
const anyChannel = ""

// on matches events of kind in ch (or anywhere, with anyChannel).
func on(kind EventKind, ch string) func(Event) bool {
	return func(ev Event) bool { return ev.Kind == kind && (ch == anyChannel || ev.ChannelID == ch) }
}

func (s *Scene) channel(ch string) string {
	if ch == here {
		return s.event.ChannelID
	}
	return ch
}

// missing reports an act that referenced something the World lacks. It
// shows as a toast, so a broken scenario is visible in the recording
// instead of silently leaving a gap.
func missing(what, id string) []tea.Msg {
	return []tea.Msg{ui.ToastMsg{Text: "demo: unknown " + what + " " + id}}
}

// typing shows userID typing in ch.
func typing(userID, ch string) Act {
	return func(s *Scene) []tea.Msg {
		c := s.channel(ch)
		team := s.world.teamOf(c)
		if team == "" {
			return missing("channel", c)
		}
		return []tea.Msg{ui.UserTypingMsg{ChannelID: c, UserID: userID, WorkspaceID: team}}
	}
}

// post has userID send txt to ch.
func post(userID, ch, txt string) Act {
	return func(s *Scene) []tea.Msg { return s.post(userID, s.channel(ch), "", txt) }
}

// postImage has userID send txt to ch with att attached.
func postImage(userID, ch, txt string, att core.Attachment) Act {
	return func(s *Scene) []tea.Msg { return s.post(userID, s.channel(ch), "", txt, att) }
}

// reply has userID answer in the triggering event's thread.
func reply(userID, txt string) Act {
	return func(s *Scene) []tea.Msg { return s.post(userID, s.event.ChannelID, s.event.ThreadTS, txt) }
}

func (s *Scene) post(userID, ch, threadTS, txt string, atts ...core.Attachment) []tea.Msg {
	m, ok := s.world.post(ch, threadTS, userID, txt, false, atts...)
	if !ok {
		return missing("conversation or author", ch+"/"+threadTS+"/"+userID)
	}
	s.lastChannel, s.lastTS = ch, m.TS
	return []tea.Msg{ui.NewMessageMsg{ChannelID: ch, Message: m}}
}

// react has userID react to the message this rule last posted.
func react(userID, emoji string) Act {
	return func(s *Scene) []tea.Msg { return s.react(userID, s.lastChannel, s.lastTS, emoji) }
}

// reactLatest has userID react to ch's newest message.
func reactLatest(userID, ch, emoji string) Act {
	return func(s *Scene) []tea.Msg { return s.react(userID, ch, s.world.latestTS(ch), emoji) }
}

func (s *Scene) react(userID, ch, ts, emoji string) []tea.Msg {
	if !s.world.react(ch, ts, userID, emoji, false) {
		return missing("message", ch+"/"+ts)
	}
	return []tea.Msg{ui.ReactionAddedMsg{ChannelID: ch, MessageTS: ts, UserID: userID, Emoji: emoji}}
}

// readStateChanged makes the sidebar and rail re-read unread state, as
// cmd/slk does after writing read state to the cache.
func readStateChanged(s *Scene) []tea.Msg {
	return []tea.Msg{ui.ReadStateChangedMsg{WorkspaceID: s.event.TeamID, ChannelID: s.event.ChannelID}}
}

// responderTyping shows whoever answers in the event's channel typing.
func responderTyping(s *Scene) []tea.Msg {
	return typing(s.world.responder(s.event.ChannelID), here)(s)
}

// responderReply has whoever answers in the event's channel reply, in its
// thread if it has one, cycling through texts.
func responderReply(texts []string) Act {
	var n atomic.Int64
	return func(s *Scene) []tea.Msg {
		txt := texts[int(n.Add(1)-1)%len(texts)]
		return s.post(s.world.responder(s.event.ChannelID), s.event.ChannelID, s.event.ThreadTS, txt)
	}
}
