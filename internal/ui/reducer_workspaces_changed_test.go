package ui

import (
	"testing"

	"github.com/schwagner/mtx/internal/ui/workspace"
)

// WorkspacesChangedMsg replaces the rail (and the number-key and finder
// lists that follow it) without moving the selection off the active
// workspace, even when entries before it are added.
func TestWorkspacesChangedKeepsActiveSelected(t *testing.T) {
	a := newTestApp(t,
		withWorkspaces(workspace.WorkspaceItem{ID: "T1", Name: "One"}, workspace.WorkspaceItem{ID: "T2", Name: "Two"}),
		withActiveTeam("T2"),
	)
	a.workspaceRail.SelectByID("T2")

	a.Update(WorkspacesChangedMsg{Items: []workspace.WorkspaceItem{
		{ID: "T0", Name: "Zero"}, {ID: "T1", Name: "One"}, {ID: "T2", Name: "Two"}, {ID: "T3", Name: "Three"},
	}})

	if got := a.workspaceRail.SelectedID(); got != "T2" {
		t.Errorf("rail selection = %q, want T2", got)
	}
	if len(a.workspaceItems) != 4 || a.workspaceItems[3].ID != "T3" {
		t.Errorf("workspaceItems = %v, want T0..T3", a.workspaceItems)
	}
	if got := a.workspaceRail.NameByID("T3"); got != "Three" {
		t.Errorf("rail NameByID(T3) = %q", got)
	}
}
