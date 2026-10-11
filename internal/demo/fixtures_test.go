package demo

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/emoji"
	"github.com/schwagner/mtx/internal/ui/styles"
)

func fixtureWorld() *World { return newWorld(fixtureTeams(), testNow, fixedClock) }

var (
	shortcodeRe = regexp.MustCompile(`:([a-z0-9_+-]+):`)
	mentionRe   = regexp.MustCompile(`<@([A-Z0-9]+)>`)
)

// eachMessage visits every fixture message and reply with its team.
func eachMessage(visit func(ts teamSpec, cs channelSpec, m msgSpec)) {
	for _, ts := range fixtureTeams() {
		for _, cs := range ts.channels {
			for _, m := range cs.msgs {
				visit(ts, cs, m)
				for _, r := range m.replies {
					visit(ts, cs, r)
				}
			}
		}
	}
}

func TestFixturesAreConsistent(t *testing.T) {
	themes := map[string]bool{}
	for _, n := range styles.ThemeNames() {
		themes[strings.ToLower(n)] = true
	}
	channelIDs := map[string]bool{}
	for _, ts := range fixtureTeams() {
		users := map[string]bool{}
		for _, u := range ts.users {
			users[u.id] = true
		}
		for _, id := range []string{ts.selfID, ts.responder} {
			if !users[id] {
				t.Errorf("%s: %s is not a user", ts.name, id)
			}
		}
		if !themes[ts.theme] {
			t.Errorf("%s: theme %q does not exist", ts.name, ts.theme)
		}
		first := false
		for _, cs := range ts.channels {
			if channelIDs[cs.id] {
				t.Errorf("duplicate channel ID %s", cs.id)
			}
			channelIDs[cs.id] = true
			first = first || cs.id == ts.firstChannel
			if cs.dmUserID != "" && !users[cs.dmUserID] {
				t.Errorf("%s: DM peer %s is not a user", cs.name, cs.dmUserID)
			}
		}
		if !first {
			t.Errorf("%s: first channel %s is not one of its channels", ts.name, ts.firstChannel)
		}
		eachMessage(func(team teamSpec, cs channelSpec, m msgSpec) {
			if team.id != ts.id {
				return
			}
			if !users[m.user] {
				t.Errorf("%s #%s: author %s is not a user", ts.name, cs.name, m.user)
			}
			for _, r := range m.reactions {
				for _, u := range r.users {
					if !users[u] {
						t.Errorf("%s #%s: reactor %s is not a user", ts.name, cs.name, u)
					}
				}
			}
			for _, sub := range mentionRe.FindAllStringSubmatch(m.text, -1) {
				if !users[sub[1]] {
					t.Errorf("%s #%s: mentions unknown user %s", ts.name, cs.name, sub[1])
				}
			}
		})
	}
}

func TestFixtureTimestampsAreOrdered(t *testing.T) {
	w := fixtureWorld()
	for id := range w.byID {
		msgs, _ := w.messages(id)
		for i := 1; i < len(msgs); i++ {
			if msgs[i].TS <= msgs[i-1].TS {
				t.Errorf("%s: message %d is not after message %d", id, i, i-1)
			}
		}
		for _, p := range msgs {
			prev := p.TS
			for _, r := range w.replies(id, p.TS) {
				if r.TS <= prev {
					t.Errorf("%s: reply %s is not after %s", id, r.TS, prev)
				}
				prev = r.TS
			}
		}
	}
}

func TestFixtureEmojiExist(t *testing.T) {
	codes := emoji.CodeMap()
	check := func(where, name string) {
		if _, ok := codes[":"+name+":"]; !ok {
			t.Errorf("%s: unknown emoji %q", where, name)
		}
	}
	eachMessage(func(ts teamSpec, cs channelSpec, m msgSpec) {
		for _, sub := range shortcodeRe.FindAllStringSubmatch(m.text, -1) {
			check(ts.name+" #"+cs.name, sub[1])
		}
		for _, r := range m.reactions {
			check(ts.name+" #"+cs.name, r.emoji)
		}
	})
	for _, s := range fixtureTeams() {
		for _, u := range s.users {
			if u.status.Emoji != "" {
				check("status of "+u.name, strings.Trim(u.status.Emoji, ":"))
			}
		}
	}
	scripted := append(slices.Clone(replyTexts), scriptedTexts...)
	for _, s := range scripted {
		for _, sub := range shortcodeRe.FindAllStringSubmatch(s, -1) {
			check("scenario text", sub[1])
		}
	}
	for _, name := range scriptedEmoji {
		check("scenario reaction", name)
	}
}

// The recordings rely on these being present.
func TestFixturesHaveTheRecordingProps(t *testing.T) {
	w := fixtureWorld()
	deploys, _ := w.messages(chDeploys)
	if i := slices.IndexFunc(deploys, func(m core.MessageItem) bool { return m.ReplyCount >= 6 }); i < 0 {
		t.Error("#deploys needs a thread with at least 6 replies")
	}
	if !slices.ContainsFunc(deploys, func(m core.MessageItem) bool { return len(m.LegacyAttachments) > 0 }) {
		t.Error("#deploys needs a bot message with a legacy attachment")
	}
	eng, _ := w.messages(chEngineering)
	if !slices.ContainsFunc(eng, func(m core.MessageItem) bool { return strings.Contains(m.Text, "```") }) {
		t.Error("#engineering needs a code block")
	}
	design, _ := w.messages(chDesign)
	if !slices.ContainsFunc(design, func(m core.MessageItem) bool {
		return len(m.Attachments) > 0 && m.Attachments[0].FileID == chartFileID
	}) {
		t.Error("#design needs the chart")
	}
	general, _ := w.messages(chGeneral)
	if !strings.Contains(general[len(general)-1].Text, "Lunch order goes out") {
		t.Error("cmd/slk's smoke test expects #general to end with the lunch message")
	}
	if !slices.Contains(w.unreadTeams(), teamDriftwood) {
		t.Error("Driftwood OSS must start unread so its rail badge shows")
	}
	if got := w.threadSummaries(teamLumen); len(got) == 0 {
		t.Error("the user must be in at least one Lumen thread")
	}
}
