package messages

import (
	"charm.land/lipgloss/v2"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui/styles"
)

// DeliveryMark is the indicator a message header shows after its
// timestamp for a delivery status, with its leading space: ✓ sent,
// ✓✓ delivered, ✓✓ in the accent color once read, ✗ failed. A message
// with no status gets "".
func DeliveryMark(d core.Delivery) string {
	switch d {
	case core.DeliverySent:
		return " " + styles.Timestamp.Render("✓")
	case core.DeliveryDelivered:
		return " " + styles.Timestamp.Render("✓✓")
	case core.DeliveryRead:
		return " " + lipgloss.NewStyle().Background(styles.Background).Foreground(styles.Accent).Render("✓✓")
	case core.DeliveryFailed:
		return " " + lipgloss.NewStyle().Background(styles.Background).Foreground(styles.Error).Render("✗ not delivered")
	}
	return ""
}
