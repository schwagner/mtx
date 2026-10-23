package matrix

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/workspace"
)

// The TUI's workspace rail lists the user's top-level Matrix spaces, the
// way Cinny's does: bridges such as mautrix-slack and mautrix-signal put
// their rooms in a space per login, so the rail reads "Slack (Work)",
// "Signal". Rooms in no space are listed under Home, the first entry,
// whose workspace ID is the user ID. A room in a subspace belongs to the
// top-level space above it; a room in two spaces is listed in both.
//
// hicli maintains the space graph (its space_edge table); the engine only
// maps each room to its top-level spaces.

// resolveSpaces maps each room reachable from a top-level space to those
// top-level spaces, in top's order.
func resolveSpaces(top []id.RoomID, graph map[id.RoomID][]*database.SpaceEdge) map[id.RoomID][]id.RoomID {
	out := map[id.RoomID][]id.RoomID{}
	for _, space := range top {
		seen := map[id.RoomID]bool{space: true}
		queue := []id.RoomID{space}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			for _, edge := range graph[parent] {
				child := edge.ChildID
				if seen[child] {
					continue
				}
				seen[child] = true
				out[child] = append(out[child], space)
				queue = append(queue, child)
			}
		}
	}
	return out
}

// spaceInitials is the rail label for a space name: the first letters of
// its first two words, ignoring punctuation and emoji ("Slack (Work)" is
// "SW"), or the first two letters of a single word.
func spaceInitials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	switch len(words) {
	case 0:
		return "?"
	case 1:
		r := []rune(words[0])
		return strings.ToUpper(string(r[:min(2, len(r))]))
	default:
		return strings.ToUpper(string([]rune(words[0])[0]) + string([]rune(words[1])[0]))
	}
}

// loadSpaces re-reads the space graph from hicli's database. It reports
// whether the rail's spaces and whether any room's spaces changed.
func (b *Backend) loadSpaces(ctx context.Context) (railChanged, roomsChanged bool, err error) {
	top, err := b.cli.DB.SpaceEdge.GetTopLevelIDs(ctx, b.UserID())
	if err != nil {
		return false, false, err
	}
	graph, err := b.cli.DB.SpaceEdge.GetAll(ctx, "")
	if err != nil {
		return false, false, err
	}
	railChanged, roomsChanged = b.setSpaces(top, resolveSpaces(top, graph))
	return railChanged, roomsChanged, nil
}

// setSpaces installs the top-level spaces and the room → spaces map,
// reporting what changed.
func (b *Backend) setSpaces(top []id.RoomID, roomSpaces map[id.RoomID][]id.RoomID) (railChanged, roomsChanged bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	railChanged = !slices.Equal(b.spaces, top)
	roomsChanged = !maps.EqualFunc(b.roomSpaces, roomSpaces, slices.Equal)
	b.spaces = slices.Clone(top)
	b.roomSpaces = roomSpaces
	return railChanged, roomsChanged
}

func (b *Backend) homeWorkspace() string { return string(b.UserID()) }

// activeWorkspace is the workspace the TUI shows: Home until the user
// switches.
func (b *Backend) activeWorkspace() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeWorkspaceLocked()
}

func (b *Backend) activeWorkspaceLocked() string {
	if b.activeWS == "" {
		return b.homeWorkspace()
	}
	return b.activeWS
}

// workspacesOfLocked returns the workspaces roomID is listed in.
func (b *Backend) workspacesOfLocked(roomID id.RoomID) []string {
	spaces := b.roomSpaces[roomID]
	if len(spaces) == 0 {
		return []string{b.homeWorkspace()}
	}
	out := make([]string, len(spaces))
	for i, s := range spaces {
		out[i] = string(s)
	}
	return out
}

func (b *Backend) inWorkspaceLocked(roomID id.RoomID, ws string) bool {
	return slices.Contains(b.workspacesOfLocked(roomID), ws)
}

// inWorkspace reports whether roomID is listed in workspace ws.
func (b *Backend) inWorkspace(roomID id.RoomID, ws string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inWorkspaceLocked(roomID, ws)
}

func (b *Backend) knownWorkspaceLocked(ws string) bool {
	return ws == b.homeWorkspace() || slices.Contains(b.spaces, id.RoomID(ws))
}

// workspaceNameLocked is a workspace's rail name: the homeserver for
// Home, the space's name otherwise.
func (b *Backend) workspaceNameLocked(ws string) string {
	if ws == b.homeWorkspace() {
		_, server, _ := b.UserID().Parse()
		return server
	}
	if r := b.rooms[id.RoomID(ws)]; r != nil {
		return roomName(r)
	}
	return ws
}

// RailItems lists the workspace rail: Home, then the top-level spaces.
func (b *Backend) RailItems() []workspace.WorkspaceItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.railItemsLocked()
}

func (b *Backend) railItemsLocked() []workspace.WorkspaceItem {
	home := b.homeWorkspace()
	name := b.workspaceNameLocked(home)
	items := []workspace.WorkspaceItem{{ID: home, Name: name, Initials: workspace.WorkspaceInitials(name)}}
	for _, s := range b.spaces {
		name := b.workspaceNameLocked(string(s))
		items = append(items, workspace.WorkspaceItem{ID: string(s), Name: name, Initials: spaceInitials(name)})
	}
	return items
}

// UnreadWorkspaces returns the workspaces with an unread room that is
// not muted (what the sidebar shows as unread), for the rail's dots.
func (b *Backend) UnreadWorkspaces() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	set := map[string]bool{}
	for roomID, r := range b.rooms {
		if _, ok := b.channelItem(r); !ok || !roomUnread(r) || b.mutedLocked(roomID, now) {
			continue
		}
		for _, ws := range b.workspacesOfLocked(roomID) {
			set[ws] = true
		}
	}
	return slices.Collect(maps.Keys(set))
}

// initialWorkspace is the workspace of the most recently active room, so
// startup lands where the conversation is.
func (b *Backend) initialWorkspace() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var latest *database.Room
	for _, r := range b.rooms {
		if _, ok := b.channelItem(r); !ok {
			continue
		}
		if latest == nil || r.SortingTimestamp.After(latest.SortingTimestamp.Time) {
			latest = r
		}
	}
	if latest == nil {
		return b.homeWorkspace()
	}
	return b.workspacesOfLocked(latest.ID)[0]
}

// finderItemsLocked lists every room and invite for the channel finder,
// which searches all of them whichever workspace is active.
func (b *Backend) finderItemsLocked() []core.ChannelFinderItem {
	var finder []core.ChannelFinderItem
	for _, item := range b.channelListLocked("") {
		finder = append(finder, finderItem(item, true))
	}
	for _, inv := range b.invites {
		finder = append(finder, b.inviteFinderItem(inv))
	}
	return finder
}

func (b *Backend) userNamesLocked() map[string]string {
	userNames := make(map[string]string, len(b.names))
	for k, v := range b.names {
		userNames[string(k)] = v
	}
	return userNames
}

// switchWorkspace makes ws the active workspace and returns the TUI's
// WorkspaceSwitchedMsg for it, or nil for an unknown workspace.
func (b *Backend) switchWorkspace(ws string) core.Msg {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.knownWorkspaceLocked(ws) {
		return nil
	}
	b.activeWS = ws
	return ui.WorkspaceSwitchedMsg{
		TeamID:      ws,
		TeamName:    b.workspaceNameLocked(ws),
		Channels:    b.channelListLocked(ws),
		FinderItems: b.finderItemsLocked(),
		UserNames:   b.userNamesLocked(),
		UserID:      string(b.UserID()),
	}
}
