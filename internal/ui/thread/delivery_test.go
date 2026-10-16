package thread

import (
	"strings"
	"testing"

	"github.com/schwagner/mtx/internal/config"
	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui/messages"
	"github.com/schwagner/mtx/internal/ui/styles"
)

// The thread pane shows and updates delivery indicators like the
// message pane, on the parent and on replies.
func TestThreadDelivery(t *testing.T) {
	styles.Apply("dark", config.Theme{})
	m := New()
	parent := messages.MessageItem{TS: "1700000001.000001", UserID: "U-ME", UserName: "me", Text: "parent", Timestamp: "1:01 PM", Delivery: core.DeliverySent}
	replies := []messages.MessageItem{
		{TS: "1700000002.000002", UserID: "U-BETH", UserName: "beth", Text: "reply", Timestamp: "1:02 PM"},
		{TS: "1700000003.000003", UserID: "U-ME", UserName: "me", Text: "mine", Timestamp: "1:03 PM", Delivery: core.DeliverySent},
	}
	m.SetThread(parent, replies, "C1", parent.TS)

	if !m.SetDelivery("1700000003.000003", core.DeliveryDelivered) {
		t.Fatal("SetDelivery on a reply reported no change")
	}
	rows := strings.Join(stripRows(m.View(30, 80)), "\n")
	for _, want := range []string{"1:01 PM ✓", "1:03 PM ✓✓"} {
		if !strings.Contains(rows, want) {
			t.Errorf("view lacks %q:\n%s", want, rows)
		}
	}
	if !m.MarkReadThrough("1700000002.000002") {
		t.Fatal("MarkReadThrough reported no change")
	}
	if m.parent.Delivery != core.DeliveryRead || m.replies[1].Delivery != core.DeliveryDelivered {
		t.Errorf("after read through the first reply: parent %d, own reply %d; want read, delivered", m.parent.Delivery, m.replies[1].Delivery)
	}
}
