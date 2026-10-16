// internal/ui/reducer_delivery.go
//
// Delivery-status reducer for App.Update.
//
//	MessageDeliveryMsg - a new status for one own message.
//	MessagesReadMsg    - a recipient read the channel through a message.
//
// Like reactions, these annotate messages in place: every window
// showing the channel, and the thread panel when its thread is in it.
package ui

import tea "charm.land/bubbletea/v2"

var reduceDelivery reducerFunc = func(a *App, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case MessageDeliveryMsg:
		for _, mm := range a.modelsForChannel(m.ChannelID) {
			mm.SetDelivery(m.TS, m.Delivery)
		}
		if a.threadPanel.ChannelID() == m.ChannelID {
			a.threadPanel.SetDelivery(m.TS, m.Delivery)
		}
		return nil, true

	case MessagesReadMsg:
		for _, mm := range a.modelsForChannel(m.ChannelID) {
			mm.MarkReadThrough(m.ThroughTS)
		}
		if a.threadPanel.ChannelID() == m.ChannelID {
			a.threadPanel.MarkReadThrough(m.ThroughTS)
		}
		return nil, true
	}
	return nil, false
}
