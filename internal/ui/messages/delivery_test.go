package messages

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/schwagner/mtx/internal/config"
	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui/styles"
)

func deliveryModel() Model {
	styles.Apply("dark", config.Theme{})
	return New([]MessageItem{
		{TS: "1700000001.000001", UserID: "U-ME", UserName: "me", Text: "one", Timestamp: "1:01 PM", Delivery: core.DeliverySent},
		{TS: "1700000002.000002", UserID: "U-BETH", UserName: "beth", Text: "two", Timestamp: "1:02 PM"},
		{TS: "1700000003.000003", UserID: "U-ME", UserName: "me", Text: "three", Timestamp: "1:03 PM", Delivery: core.DeliveryDelivered},
		{TS: "1700000004.000004", UserID: "U-ME", UserName: "me", Text: "four", Timestamp: "1:04 PM", Delivery: core.DeliveryFailed},
	}, "beth")
}

// headerRow is the plain text of the header row of the message whose
// header shows clock.
func headerRow(t *testing.T, m *Model, clock string) string {
	t.Helper()
	for _, row := range strings.Split(ansi.Strip(m.View(30, 80)), "\n") {
		if strings.Contains(row, clock) {
			return row
		}
	}
	t.Fatalf("no row shows %q", clock)
	return ""
}

// The indicator follows the timestamp on own messages and is absent
// from messages without a status.
func TestDeliveryMarkInHeader(t *testing.T) {
	m := deliveryModel()
	for clock, want := range map[string]string{
		"1:01 PM": "1:01 PM ✓",
		"1:03 PM": "1:03 PM ✓✓",
		"1:04 PM": "1:04 PM ✗ not delivered",
	} {
		if row := headerRow(t, &m, clock); !strings.Contains(row, want) {
			t.Errorf("header %q lacks %q", row, want)
		}
	}
	if row := headerRow(t, &m, "1:02 PM"); strings.ContainsAny(row, "✓✗") {
		t.Errorf("message without a status shows one: %q", row)
	}
}

func TestSetDeliveryAndMarkReadThrough(t *testing.T) {
	m := deliveryModel()
	if !m.SetDelivery("1700000001.000001", core.DeliveryDelivered) {
		t.Error("SetDelivery reported no change")
	}
	if m.SetDelivery("1700000001.000001", core.DeliveryDelivered) {
		t.Error("repeated SetDelivery reported a change")
	}
	if !m.MarkReadThrough("1700000003.000003") {
		t.Error("MarkReadThrough reported no change")
	}
	want := []core.Delivery{core.DeliveryRead, core.DeliveryNone, core.DeliveryRead, core.DeliveryFailed}
	for i, w := range want {
		if got := m.messages[i].Delivery; got != w {
			t.Errorf("message %d: delivery %d, want %d", i, got, w)
		}
	}
	if m.SetDelivery("1700000003.000003", core.DeliverySent) || m.messages[2].Delivery != core.DeliveryRead {
		t.Error("a late report undid a read")
	}
}
