package matrix

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/gomuks/pkg/hicli/database"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/sidebar"
)

func edges(children ...id.RoomID) []*database.SpaceEdge {
	out := make([]*database.SpaceEdge, len(children))
	for i, c := range children {
		out[i] = &database.SpaceEdge{ChildID: c}
	}
	return out
}

// Rooms belong to the top-level spaces they descend from; subspaces fold
// into their top-level space, a room in two spaces belongs to both, and a
// cycle in the space graph terminates.
func TestResolveSpaces(t *testing.T) {
	top := []id.RoomID{"!slack", "!signal"}
	graph := map[id.RoomID][]*database.SpaceEdge{
		"!slack":  edges("!general", "!sub", "!shared"),
		"!sub":    edges("!deep", "!slack"), // cycle back to the top
		"!signal": edges("!dm", "!shared"),
		"!orphan": edges("!unreached"), // a space the user isn't in
	}
	got := resolveSpaces(top, graph)
	want := map[id.RoomID][]id.RoomID{
		"!general": {"!slack"},
		"!sub":     {"!slack"},
		"!deep":    {"!slack"},
		"!shared":  {"!slack", "!signal"},
		"!dm":      {"!signal"},
	}
	if len(got) != len(want) {
		t.Errorf("got %d rooms, want %d: %v", len(got), len(want), got)
	}
	for room, spaces := range want {
		if !slices.Equal(got[room], spaces) {
			t.Errorf("%s: got %v, want %v", room, got[room], spaces)
		}
	}
}

func TestSpaceInitials(t *testing.T) {
	for name, want := range map[string]string{
		"Slack (Techs)": "ST",
		"Signal":        "SI",
		"🙂 Fun Club":    "FC",
		"Équipe":        "ÉQ",
		"x":             "X",
		"(( ))":         "?",
		"":              "?",
	} {
		if got := spaceInitials(name); got != want {
			t.Errorf("spaceInitials(%q) = %q, want %q", name, got, want)
		}
	}
}

const testMe = id.UserID("@me:example.com")

// spacesBackend is a Backend with a home room, rooms in a Slack and a
// Signal space, and one room in both.
func spacesBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := Open(filepath.Join(t.TempDir(), "mtx.db"), make([]byte, 32), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	b.cli.Account = &database.Account{UserID: testMe}
	now := time.Now()
	room := func(roomID id.RoomID, name string, age time.Duration) *database.Room {
		return &database.Room{ID: roomID, Name: &name, SortingTimestamp: jsontime.UM(now.Add(-age))}
	}
	space := func(roomID id.RoomID, name string) *database.Room {
		r := room(roomID, name, time.Hour)
		r.CreationContent = &event.CreateEventContent{Type: event.RoomTypeSpace}
		return r
	}
	for _, r := range []*database.Room{
		room("!home", "home", 4*time.Minute),
		room("!general", "general", 3*time.Minute),
		room("!shared", "shared", 2*time.Minute),
		room("!dm", "beth", time.Minute),
		space("!slack", "Slack (Techs)"),
		space("!signal", "Signal"),
	} {
		b.rooms[r.ID] = r
	}
	b.setSpaces([]id.RoomID{"!slack", "!signal"}, map[id.RoomID][]id.RoomID{
		"!general": {"!slack"},
		"!shared":  {"!slack", "!signal"},
		"!dm":      {"!signal"},
	})
	return b
}

func channelIDs(items []sidebar.ChannelItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestWorkspaceChannels(t *testing.T) {
	b := spacesBackend(t)
	for ws, want := range map[string][]string{
		string(testMe): {"!home"},
		"!slack":       {"!shared", "!general"},
		"!signal":      {"!dm", "!shared"},
	} {
		if got := channelIDs(b.channelList(ws)); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", ws, got, want)
		}
	}
}

func TestRailItems(t *testing.T) {
	b := spacesBackend(t)
	items := b.RailItems()
	var got []string
	for _, it := range items {
		got = append(got, it.ID+"="+it.Name+"/"+it.Initials)
	}
	want := []string{string(testMe) + "=example.com/EX", "!slack=Slack (Techs)/ST", "!signal=Signal/SI"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUnreadWorkspaces(t *testing.T) {
	b := spacesBackend(t)
	if got := b.UnreadWorkspaces(); len(got) != 0 {
		t.Fatalf("nothing unread: got %v", got)
	}
	b.rooms["!shared"].UnreadMessages = 1
	b.rooms["!home"].UnreadMessages = 1
	got := b.UnreadWorkspaces()
	slices.Sort(got)
	want := []string{"!signal", "!slack", string(testMe)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSwitchWorkspace(t *testing.T) {
	b := spacesBackend(t)
	msg, ok := b.switchWorkspace("!signal").(ui.WorkspaceSwitchedMsg)
	if !ok {
		t.Fatalf("got %T, want WorkspaceSwitchedMsg", msg)
	}
	if msg.TeamID != "!signal" || msg.TeamName != "Signal" || msg.UserID != string(testMe) {
		t.Errorf("got team %q %q user %q", msg.TeamID, msg.TeamName, msg.UserID)
	}
	if got := channelIDs(msg.Channels); !slices.Equal(got, []string{"!dm", "!shared"}) {
		t.Errorf("channels %v", got)
	}
	// The finder searches every room, whichever space is active.
	if len(msg.FinderItems) != 4 {
		t.Errorf("finder has %d items, want 4", len(msg.FinderItems))
	}
	if b.activeWorkspace() != "!signal" {
		t.Errorf("active workspace %q", b.activeWorkspace())
	}
	if b.switchWorkspace("!nope") != nil {
		t.Error("switching to an unknown workspace should do nothing")
	}
	if b.activeWorkspace() != "!signal" {
		t.Errorf("unknown switch changed the active workspace to %q", b.activeWorkspace())
	}
}

// setSpaces reports what changed so the sync handler knows to refresh the
// rail and the sidebar.
func TestSetSpacesReportsChanges(t *testing.T) {
	b := spacesBackend(t)
	top := []id.RoomID{"!slack", "!signal"}
	// Each call gets a fresh map, as resolveSpaces returns one.
	members := func(extra ...id.RoomID) map[id.RoomID][]id.RoomID {
		m := map[id.RoomID][]id.RoomID{
			"!general": {"!slack"},
			"!shared":  {"!slack", "!signal"},
			"!dm":      {"!signal"},
		}
		for _, r := range extra {
			m[r] = []id.RoomID{"!slack"}
		}
		return m
	}
	if rail, rooms := b.setSpaces(top, members()); rail || rooms {
		t.Errorf("unchanged: rail=%v rooms=%v", rail, rooms)
	}
	if rail, rooms := b.setSpaces(top, members("!home")); rail || !rooms {
		t.Errorf("room moved: rail=%v rooms=%v", rail, rooms)
	}
	if rail, _ := b.setSpaces(top[:1], members("!home")); !rail {
		t.Error("space left: rail unchanged")
	}
}

// The initial workspace is the one holding the most recently active room.
func TestInitialWorkspace(t *testing.T) {
	b := spacesBackend(t)
	if got := b.initialWorkspace(); got != "!signal" {
		t.Errorf("got %q, want !signal (holds !dm)", got)
	}
	b.rooms["!home"].SortingTimestamp = jsontime.UM(time.Now())
	if got := b.initialWorkspace(); got != string(testMe) {
		t.Errorf("got %q, want home", got)
	}
}
