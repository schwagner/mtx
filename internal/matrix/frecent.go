package matrix

import (
	"encoding/json"
	"os"
	"slices"
	"sync"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/emoji"
)

// defaultReactions seed the picker before the user has reacted to anything.
var defaultReactions = []string{"+1", "heart", "joy", "tada", "eyes", "pray", "white_check_mark", "thinking_face"}

const maxRecentEmoji = 30

// recentEmoji is the reaction picker's most-recently-used list, persisted
// as a JSON array of shortcode names.
type recentEmoji struct {
	path string
	mu   sync.Mutex
	list []string
}

func newRecentEmoji(path string) *recentEmoji {
	r := &recentEmoji{path: path}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &r.list)
	}
	return r
}

func (r *recentEmoji) load(limit int) []core.EmojiEntry {
	r.mu.Lock()
	names := slices.Clone(r.list)
	r.mu.Unlock()
	for _, d := range defaultReactions {
		if !slices.Contains(names, d) {
			names = append(names, d)
		}
	}
	codes := emoji.CodeMap()
	var out []core.EmojiEntry
	for _, name := range names {
		if len(out) >= limit {
			break
		}
		if glyph, ok := codes[":"+name+":"]; ok {
			out = append(out, core.EmojiEntry{Name: name, Unicode: glyph})
		}
	}
	return out
}

func (r *recentEmoji) record(name string) {
	r.mu.Lock()
	r.list = slices.DeleteFunc(r.list, func(n string) bool { return n == name })
	r.list = slices.Insert(r.list, 0, name)
	if len(r.list) > maxRecentEmoji {
		r.list = r.list[:maxRecentEmoji]
	}
	data, _ := json.Marshal(r.list)
	r.mu.Unlock()
	if r.path != "" {
		_ = os.WriteFile(r.path, data, 0o600)
	}
}
