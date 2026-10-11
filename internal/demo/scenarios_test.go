package demo

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
)

// threadIn returns the TS of the first thread parent in channelID.
func threadIn(t *testing.T, w *World, channelID string) string {
	t.Helper()
	msgs, _ := w.messages(channelID)
	i := slices.IndexFunc(msgs, func(m core.MessageItem) bool { return m.ReplyCount > 0 })
	if i < 0 {
		t.Fatalf("no thread in %s", channelID)
	}
	return msgs[i].TS
}

func TestHeroEngineeringRule(t *testing.T) {
	w := fixtureWorld()
	d := newDirector(w, heroRules()[:1])
	dl := &delays{}
	d.after = dl.instant
	rec := &recorder{}
	start(t, d, rec.send)

	d.observe(Event{Kind: EventChannelOpened, TeamID: teamLumen, ChannelID: chEngineering})
	got := rec.waitFor(t, 3)

	typing, ok := got[0].(ui.UserTypingMsg)
	if !ok || typing.UserID != uPriya || typing.ChannelID != chEngineering || typing.WorkspaceID != teamLumen {
		t.Fatalf("step 1 = %#v, want Priya typing in #engineering", got[0])
	}
	posted, ok := got[1].(ui.NewMessageMsg)
	if !ok || posted.ChannelID != chEngineering || posted.Message.UserID != uPriya {
		t.Fatalf("step 2 = %#v, want Priya's message", got[1])
	}
	if atts := posted.Message.Attachments; len(atts) != 1 || atts[0].FileID != memeFileID {
		t.Fatalf("Priya's message attachments = %+v, want the meme", atts)
	}
	reaction, ok := got[2].(ui.ReactionAddedMsg)
	if !ok || reaction.UserID != uSam || reaction.Emoji != "joy" || reaction.MessageTS != posted.Message.TS {
		t.Fatalf("step 3 = %#v, want Sam's joy on Priya's message", got[2])
	}
	if want := []time.Duration{1500 * time.Millisecond, 2500 * time.Millisecond, 1200 * time.Millisecond}; !slices.Equal(dl.all(), want) {
		t.Errorf("delays = %v, want %v", dl.all(), want)
	}
	msgs, _ := w.messages(chEngineering)
	if last := msgs[len(msgs)-1]; last.TS != posted.Message.TS || len(last.Reactions) != 1 {
		t.Errorf("World does not hold the scripted message and reaction: %+v", last)
	}
}

func TestAnswerTheUserPicksTheRightResponder(t *testing.T) {
	w := fixtureWorld()
	for _, tc := range []struct {
		channel, thread, want string
	}{
		{chEngineering, threadIn(t, w, chEngineering), uPriya},
		{dmSam, "", uSam},
		{chContributors, "", uRuth},
	} {
		d := newDirector(w, []Rule{answerTheUser()})
		d.after = (&delays{}).instant
		rec := &recorder{}
		stop := start(t, d, rec.send)
		d.observe(Event{Kind: EventMessageSent, TeamID: w.teamOf(tc.channel), ChannelID: tc.channel, ThreadTS: tc.thread})
		got := rec.waitFor(t, 2)
		stop()

		if typing, ok := got[0].(ui.UserTypingMsg); !ok || typing.UserID != tc.want || typing.ChannelID != tc.channel {
			t.Errorf("%s: step 1 = %#v, want %s typing", tc.channel, got[0], tc.want)
		}
		answer, ok := got[1].(ui.NewMessageMsg)
		if !ok || answer.Message.UserID != tc.want || answer.Message.ThreadTS != tc.thread {
			t.Errorf("%s: step 2 = %#v, want %s answering in thread %q", tc.channel, got[1], tc.want, tc.thread)
		}
	}
}

func TestEveryScenarioPlaysAgainstTheFixtures(t *testing.T) {
	for _, name := range ScenarioNames() {
		t.Run(name, func(t *testing.T) {
			w := fixtureWorld()
			rules, err := rulesFor(name)
			if err != nil {
				t.Fatal(err)
			}
			d := newDirector(w, rules)
			d.after = (&delays{}).instant
			rec := &recorder{}
			start(t, d, rec.send)
			for _, ev := range []Event{
				{Kind: EventChannelOpened, TeamID: teamLumen, ChannelID: chGeneral},
				{Kind: EventChannelOpened, TeamID: teamLumen, ChannelID: chEngineering},
				{Kind: EventThreadOpened, TeamID: teamLumen, ChannelID: chDeploys, ThreadTS: threadIn(t, w, chDeploys)},
				{Kind: EventMessageSent, TeamID: teamLumen, ChannelID: dmSam},
				{Kind: EventWorkspaceSwitched, TeamID: teamDriftwood},
			} {
				d.observe(ev)
			}
			lively := 0
			for _, m := range rec.settle() {
				switch m := m.(type) {
				case ui.ToastMsg:
					t.Errorf("scenario references something missing: %s", m.Text)
				case ui.NewMessageMsg, ui.ReactionAddedMsg:
					lively++
				}
			}
			if name != "themes" && lively == 0 {
				t.Error("scenario produced no live activity")
			}
		})
	}
}

func TestRulesForUnknownScenarioListsTheChoices(t *testing.T) {
	_, err := rulesFor("nope")
	if err == nil {
		t.Fatal("unknown scenario accepted")
	}
	for _, name := range ScenarioNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not offer %q", err, name)
		}
	}
	if got := ScenarioNames(); !slices.Equal(got, []string{"compose", "hero", "reactions", "themes", "threads", "workspaces"}) {
		t.Errorf("ScenarioNames = %v", got)
	}
}
