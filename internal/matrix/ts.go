package matrix

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/id"
)

// The TUI identifies messages by Slack-style timestamps ("1700000000.123456"):
// it sorts them as strings, compares them to find the unread boundary, and
// parses the seconds for date headers. Matrix event IDs are opaque, so every
// event gets a synthetic TS:
//
//	<unix seconds>.<milliseconds (3 digits)><event rowid mod 1000 (3 digits)>
//
// The seconds and milliseconds come from origin_server_ts, so the TS orders
// messages by time and parses to the right date. The low three digits make
// it unique within a room for any realistic burst of same-millisecond events.
// It is derived only from stored data, so it is stable across restarts.
func makeTS(evt *database.Event) string {
	ms := evt.Timestamp.UnixMilli()
	return fmt.Sprintf("%d.%03d%03d", ms/1000, ms%1000, int64(evt.RowID)%1000)
}

// parseTS splits a synthetic TS back into its millisecond timestamp and
// rowid residue.
func parseTS(ts string) (ms int64, rowMod int64, ok bool) {
	secStr, frac, found := strings.Cut(ts, ".")
	if !found || len(frac) != 6 {
		return 0, 0, false
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	msPart, err1 := strconv.ParseInt(frac[:3], 10, 64)
	rowPart, err2 := strconv.ParseInt(frac[3:], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return sec*1000 + msPart, rowPart, true
}

type tsKey struct {
	room id.RoomID
	ts   string
}

// tsIndex remembers the TS<->event mapping for every event handed to the
// UI, so the service calls that come back keyed by TS (react, edit, open
// thread, paginate) don't need a database round trip.
type tsIndex struct {
	mu       sync.RWMutex
	byTS     map[tsKey]id.EventID
	byEvent  map[id.EventID]string
	timeline map[tsKey]database.TimelineRowID
}

func newTSIndex() *tsIndex {
	return &tsIndex{
		byTS:     make(map[tsKey]id.EventID),
		byEvent:  make(map[id.EventID]string),
		timeline: make(map[tsKey]database.TimelineRowID),
	}
}

func (x *tsIndex) put(evt *database.Event) string {
	ts := makeTS(evt)
	x.mu.Lock()
	defer x.mu.Unlock()
	k := tsKey{evt.RoomID, ts}
	x.byTS[k] = evt.ID
	x.byEvent[evt.ID] = ts
	if evt.TimelineRowID != 0 {
		x.timeline[k] = evt.TimelineRowID
	}
	return ts
}

func (x *tsIndex) setTimeline(room id.RoomID, ts string, rowID database.TimelineRowID) {
	x.mu.Lock()
	x.timeline[tsKey{room, ts}] = rowID
	x.mu.Unlock()
}

func (x *tsIndex) event(room id.RoomID, ts string) (id.EventID, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	evtID, ok := x.byTS[tsKey{room, ts}]
	return evtID, ok
}

func (x *tsIndex) ts(evtID id.EventID) (string, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	ts, ok := x.byEvent[evtID]
	return ts, ok
}

func (x *tsIndex) timelineRow(room id.RoomID, ts string) (database.TimelineRowID, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	row, ok := x.timeline[tsKey{room, ts}]
	return row, ok
}
