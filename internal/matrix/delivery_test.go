package matrix

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"
	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
)

const (
	dlvMe    = id.UserID("@me:example.com")
	dlvGhost = id.UserID("@signal_beth:example.com")
	dlvBot   = id.UserID("@signalbot:example.com")
	dlvRoom  = id.RoomID("!dm:example.com")
)

// deliveryFixture is a Backend over a fresh database holding one room.
type deliveryFixture struct {
	t    *testing.T
	b    *Backend
	ctx  context.Context
	mu   sync.Mutex
	sent []tea.Msg
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	b, err := Open(filepath.Join(t.TempDir(), "mtx.db"), make([]byte, 32), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.cli.DB.Close() })
	ctx := context.Background()
	if err := b.cli.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	b.cli.Account = &database.Account{UserID: dlvMe}
	if err := b.cli.DB.Room.CreateRow(ctx, dlvRoom); err != nil {
		t.Fatal(err)
	}
	f := &deliveryFixture{t: t, b: b, ctx: ctx}
	b.SetSender(func(msg tea.Msg) {
		f.mu.Lock()
		f.sent = append(f.sent, msg)
		f.mu.Unlock()
	})
	return f
}

func (f *deliveryFixture) event(evtID id.EventID, sender id.UserID, typ event.Type, stateKey *string, ts int64, content any) *database.Event {
	f.t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		f.t.Fatal(err)
	}
	evt := database.MautrixToEvent(&event.Event{
		ID: evtID, RoomID: dlvRoom, Sender: sender, Type: typ, StateKey: stateKey,
		Timestamp: ts, Content: event.Content{VeryRaw: raw},
	})
	if evt.RowID, err = f.b.cli.DB.Event.Upsert(f.ctx, evt); err != nil {
		f.t.Fatal(err)
	}
	return evt
}

func (f *deliveryFixture) state(typ event.Type, key string, content any) {
	f.t.Helper()
	evt := f.event(id.EventID("$state-"+typ.Type+key), dlvBot, typ, &key, 1, content)
	if err := f.b.cli.DB.CurrentState.Set(f.ctx, dlvRoom, typ, key, evt.RowID, ""); err != nil {
		f.t.Fatal(err)
	}
}

// bridged makes the room a Signal portal: bridge info naming the bot,
// and room features that advertise read receipts when receipts is set.
func (f *deliveryFixture) bridged(receipts bool) {
	f.state(event.StateBridge, "signal", map[string]any{"bridgebot": dlvBot, "protocol": map[string]any{"id": "signal"}})
	f.state(event.StateBeeperRoomFeatures, "", map[string]any{"read_receipts": receipts})
}

func (f *deliveryFixture) text(evtID id.EventID, sender id.UserID, ts int64) *database.Event {
	return f.event(evtID, sender, event.EventMessage, nil, ts, map[string]any{"msgtype": "m.text", "body": string(evtID)})
}

func (f *deliveryFixture) mss(evtID id.EventID, sender id.UserID, target id.EventID, status event.MessageStatus, deliveredTo ...id.UserID) *database.Event {
	content := map[string]any{
		"status":       status,
		"m.relates_to": map[string]any{"rel_type": "m.reference", "event_id": target},
	}
	if deliveredTo != nil {
		content["delivered_to_users"] = deliveredTo
	}
	return f.event(evtID, sender, event.BeeperMessageStatus, nil, 9000, content)
}

func (f *deliveryFixture) receipt(user id.UserID, target id.EventID) *database.Receipt {
	f.t.Helper()
	r := &database.Receipt{RoomID: dlvRoom, UserID: user, ReceiptType: event.ReceiptTypeRead, EventID: target}
	if err := f.b.cli.DB.Receipt.PutMany(f.ctx, dlvRoom, r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *deliveryFixture) msgs() []tea.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tea.Msg(nil), f.sent...)
}

// Own messages in a portal whose bridge reports read receipts get a
// status: read up to the latest event a recipient has a receipt on,
// else what the bridge's message status events last said, else sent.
// The bridge bot's receipts, and status events from anyone but the bot,
// are not evidence of anything.
func TestDeliveryStatuses(t *testing.T) {
	f := newDeliveryFixture(t)
	f.bridged(true)
	evts := []*database.Event{
		f.text("$m1", dlvMe, 1000),
		f.text("$m2", dlvMe, 2000),
		f.text("$g", dlvGhost, 2500),
		f.text("$m3", dlvMe, 3000),
		f.text("$m4", dlvMe, 4000),
		f.text("$m5", dlvMe, 5000),
	}
	f.mss("$s2", dlvBot, "$m2", event.MessageStatusSuccess, dlvGhost)
	f.mss("$s3a", dlvBot, "$m3", event.MessageStatusSuccess)
	f.mss("$s3b", dlvBot, "$m3", event.MessageStatusFail)
	f.mss("$s4", dlvBot, "$m4", event.MessageStatusSuccess, dlvGhost)
	f.mss("$s5", dlvGhost, "$m5", event.MessageStatusFail)
	f.receipt(dlvGhost, "$g")
	f.receipt(dlvBot, "$m5")

	got := f.b.deliveryStatuses(f.ctx, dlvRoom, evts)
	want := map[id.EventID]core.Delivery{
		"$m1": core.DeliveryRead,
		"$m2": core.DeliveryRead,
		"$m3": core.DeliveryFailed,
		"$m4": core.DeliveryDelivered,
		"$m5": core.DeliverySent,
	}
	if len(got) != len(want) {
		t.Errorf("got %d statuses, want %d: %v", len(got), len(want), got)
	}
	for evtID, w := range want {
		if got[evtID] != w {
			t.Errorf("%s: delivery = %d, want %d", evtID, got[evtID], w)
		}
	}
	items := f.b.convert(f.ctx, dlvRoom, evts)
	if items[0].Delivery != core.DeliveryRead || items[2].Delivery != core.DeliveryNone {
		t.Errorf("convert: deliveries %d, %d; want read on own, none on the ghost's", items[0].Delivery, items[2].Delivery)
	}
}

// A room whose bridge doesn't report read receipts (Slack), or that
// isn't bridged at all, shows no indicators.
func TestDeliveryStatusesUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*deliveryFixture)
	}{
		{"plain Matrix room", func(*deliveryFixture) {}},
		{"bridge without read receipts", func(f *deliveryFixture) { f.bridged(false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeliveryFixture(t)
			tc.prepare(f)
			evts := []*database.Event{f.text("$m1", dlvMe, 1000)}
			if got := f.b.deliveryStatuses(f.ctx, dlvRoom, evts); got != nil {
				t.Errorf("got %v, want none", got)
			}
		})
	}
}

// Live updates: a recipient's receipt marks the room read through the
// receipt's event, and the bot's status events update one message.
func TestDeliveryLive(t *testing.T) {
	f := newDeliveryFixture(t)
	f.bridged(true)
	f.text("$m1", dlvMe, 1000)
	g := f.text("$g", dlvGhost, 2000)

	f.b.handleReceipts(f.ctx, dlvRoom, map[id.EventID][]*database.Receipt{
		"$m1": {f.receipt(dlvBot, "$m1")},
	})
	if msgs := f.msgs(); len(msgs) != 0 {
		t.Fatalf("bot receipt: got %v, want nothing", msgs)
	}
	f.b.handleReceipts(f.ctx, dlvRoom, map[id.EventID][]*database.Receipt{
		"$g": {f.receipt(dlvGhost, "$g")},
	})
	want := ui.MessagesReadMsg{ChannelID: string(dlvRoom), ThroughTS: makeTS(g)}
	if msgs := f.msgs(); len(msgs) != 1 || msgs[0] != want {
		t.Fatalf("ghost receipt: got %v, want [%v]", msgs, want)
	}

	f.b.handleSendStatus(f.ctx, dlvRoom, f.mss("$fake", dlvGhost, "$m1", event.MessageStatusFail))
	f.b.handleSendStatus(f.ctx, dlvRoom, f.mss("$theirs", dlvBot, "$g", event.MessageStatusFail))
	f.b.handleSendStatus(f.ctx, dlvRoom, f.mss("$s1", dlvBot, "$m1", event.MessageStatusSuccess, dlvGhost))
	msgs := f.msgs()[1:]
	if len(msgs) != 1 {
		t.Fatalf("status events: got %v, want one update", msgs)
	}
	if m, ok := msgs[0].(ui.MessageDeliveryMsg); !ok || m.Delivery != core.DeliveryDelivered || m.ChannelID != string(dlvRoom) {
		t.Errorf("status event: got %#v, want $m1 delivered", msgs[0])
	}
}
