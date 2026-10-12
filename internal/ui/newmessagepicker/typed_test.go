package newmessagepicker

import "testing"

func typeQuery(m *Model, q string) {
	for _, r := range q {
		m.HandleKey(string(r))
	}
}

func TestTypedID_UnknownUserCanBeSubmitted(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
	m.Open()
	typeQuery(&m, "@beth:example.com")
	res := m.HandleKey("enter")
	if res == nil || len(res.UserIDs) != 1 || res.UserIDs[0] != "@beth:example.com" {
		t.Fatalf("got %+v, want @beth:example.com", res)
	}
}

func TestTypedID_PartialIDAddsNoRow(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
	m.Open()
	typeQuery(&m, "@beth:")
	for _, i := range m.filtered {
		if m.users[i].ID == "@beth:" {
			t.Fatal("incomplete ID was offered")
		}
	}
}

func TestTypedID_KnownUserIsHighlighted(t *testing.T) {
	users := append(testUsers(), User{ID: "@beth:example.com", DisplayName: "Beth", Username: "Beth"})
	m := New()
	m.SetUsers(users)
	m.Open()
	typeQuery(&m, "@beth:example.com")
	if len(m.users) != len(users) {
		t.Fatalf("synthesized a duplicate row: %d users", len(m.users))
	}
	res := m.HandleKey("enter")
	if res == nil || res.UserIDs[0] != "@beth:example.com" {
		t.Fatalf("got %+v", res)
	}
}

func TestTypedID_RowDroppedWhenQueryChanges(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
	m.Open()
	typeQuery(&m, "@beth:example.com")
	for range "example.com" {
		m.HandleKey("backspace")
	}
	if len(m.users) != len(testUsers()) {
		t.Fatalf("typed row lingered: %d users", len(m.users))
	}
}

func TestTypedID_SurvivesAsPill(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
	m.Open()
	typeQuery(&m, "@beth:example.com")
	m.HandleKey("tab") // pill it; the query clears
	typeQuery(&m, "ali")
	m.HandleKey("tab")
	res := m.HandleKey("enter")
	if res == nil || len(res.UserIDs) != 2 {
		t.Fatalf("got %+v, want two recipients", res)
	}
	got := map[string]bool{res.UserIDs[0]: true, res.UserIDs[1]: true}
	if !got["@beth:example.com"] || !got["U1"] {
		t.Fatalf("got %v", res.UserIDs)
	}
}
