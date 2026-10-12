package matrix

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/schwagner/mtx/internal/ids"
	"github.com/schwagner/mtx/internal/ui"
)

// An account with no threads must get an empty list, not a panic from
// hicli's multi-row query builder (which cannot handle zero IDs).
func TestFetchThreadsEmpty(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "mtx.db"), make([]byte, 32), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.cli.DB.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	msg, ok := b.fetchThreads(ids.TeamID("@me:example.com")).(ui.ThreadsListLoadedMsg)
	if !ok {
		t.Fatalf("got %T, want ThreadsListLoadedMsg", msg)
	}
	if len(msg.Summaries) != 0 {
		t.Fatalf("got %d summaries, want 0", len(msg.Summaries))
	}
}
