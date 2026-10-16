package core

import "testing"

// Reports replace a status (a bridge may report failure after "sent",
// or success after a retry), but a read is final, and a read only
// applies to messages that got somewhere.
func TestDeliveryTransitions(t *testing.T) {
	for _, tc := range []struct {
		cur, next, want Delivery
	}{
		{DeliverySent, DeliveryDelivered, DeliveryDelivered},
		{DeliverySent, DeliveryFailed, DeliveryFailed},
		{DeliveryFailed, DeliverySent, DeliverySent},
		{DeliveryRead, DeliveryDelivered, DeliveryRead},
		{DeliveryDelivered, DeliveryNone, DeliveryDelivered},
	} {
		if got := tc.cur.Update(tc.next); got != tc.want {
			t.Errorf("%d.Update(%d) = %d, want %d", tc.cur, tc.next, got, tc.want)
		}
	}
	for cur, want := range map[Delivery]Delivery{
		DeliveryNone:      DeliveryNone,
		DeliverySent:      DeliveryRead,
		DeliveryDelivered: DeliveryRead,
		DeliveryRead:      DeliveryRead,
		DeliveryFailed:    DeliveryFailed,
	} {
		if got := cur.Read(); got != want {
			t.Errorf("%d.Read() = %d, want %d", cur, got, want)
		}
	}
}
