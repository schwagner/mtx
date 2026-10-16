package ui

import (
	"testing"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui/messages"
)

// Delivery reports reach the channel's messages and the open thread,
// and only when they are about that channel.
func TestReduceDelivery(t *testing.T) {
	mine := func(ts string) messages.MessageItem {
		return messages.MessageItem{TS: ts, UserID: "U-ME", UserName: "me", Text: ts, Delivery: core.DeliverySent}
	}
	a := newTestApp(t,
		withMessages(mine("1.000001"), mine("2.000002"), mine("3.000003")),
		withActiveChannel("C1"),
	)
	a.threadPanel.SetThread(mine("2.000002"), []messages.MessageItem{mine("2.500001")}, "C1", "2.000002")

	updateAndRender(t, a, MessageDeliveryMsg{ChannelID: "C2", TS: "3.000003", Delivery: core.DeliveryFailed})
	updateAndRender(t, a, MessageDeliveryMsg{ChannelID: "C1", TS: "3.000003", Delivery: core.DeliveryDelivered})
	updateAndRender(t, a, MessageDeliveryMsg{ChannelID: "C1", TS: "2.500001", Delivery: core.DeliveryDelivered})
	updateAndRender(t, a, MessagesReadMsg{ChannelID: "C1", ThroughTS: "2.000002"})

	want := []core.Delivery{core.DeliveryRead, core.DeliveryRead, core.DeliveryDelivered}
	for i, msg := range a.messagepane.Messages() {
		if msg.Delivery != want[i] {
			t.Errorf("message %s: delivery %d, want %d", msg.TS, msg.Delivery, want[i])
		}
	}
	if d := a.threadPanel.ParentMsg().Delivery; d != core.DeliveryRead {
		t.Errorf("thread parent: delivery %d, want read", d)
	}
	if d := a.threadPanel.Replies()[0].Delivery; d != core.DeliveryDelivered {
		t.Errorf("thread reply: delivery %d, want delivered", d)
	}
}
