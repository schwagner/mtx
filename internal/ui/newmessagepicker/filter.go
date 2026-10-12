package newmessagepicker

import (
	"slices"
	"sort"
	"strings"
)

// filter rebuilds m.filtered from m.users honoring the current query,
// the current-user exclusion, and the ranking rules:
//
//  1. Match tier (only when query non-empty):
//     0 = prefix on display name OR username
//     1 = substring on display name OR username
//     2 = subsequence on display name OR username
//     Non-matchers are dropped.
//  2. Recency DESC.
//  3. DisplayName ASC (case-insensitive).
//
// Empty query: include all users (minus self) sorted by Recency DESC
// then DisplayName ASC.
func (m *Model) filter() {
	m.dropTyped()
	m.filtered = m.filtered[:0]
	defer m.addTyped()
	q := strings.ToLower(m.query)

	if q == "" {
		for i, u := range m.users {
			if u.ID == m.currentUserID {
				continue
			}
			m.filtered = append(m.filtered, i)
		}
		sort.SliceStable(m.filtered, func(i, j int) bool {
			return m.lessNoQuery(m.filtered[i], m.filtered[j])
		})
		return
	}

	type match struct {
		idx  int
		tier int
	}
	var matches []match
	for i, u := range m.users {
		if u.ID == m.currentUserID {
			continue
		}
		tier, ok := matchTier(u, q)
		if !ok {
			continue
		}
		matches = append(matches, match{idx: i, tier: tier})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		ua, ub := m.users[a.idx], m.users[b.idx]
		if ua.Recency != ub.Recency {
			return ua.Recency > ub.Recency
		}
		return strings.ToLower(ua.DisplayName) < strings.ToLower(ub.DisplayName)
	})

	for _, mm := range matches {
		m.filtered = append(m.filtered, mm.idx)
	}
}

// matchTier returns (tier, true) if u matches q on either its
// DisplayName or its Username. tier 0 = prefix, 1 = substring,
// 2 = subsequence. q is expected to already be lower-cased.
func matchTier(u User, q string) (int, bool) {
	name := strings.ToLower(u.DisplayName)
	handle := strings.ToLower(u.Username)
	if strings.HasPrefix(name, q) || strings.HasPrefix(handle, q) {
		return 0, true
	}
	if strings.Contains(name, q) || strings.Contains(handle, q) {
		return 1, true
	}
	if isSubsequence(name, q) || isSubsequence(handle, q) {
		return 2, true
	}
	return 0, false
}

// isSubsequence reports whether every rune of q appears in s in
// order. Both inputs are expected to already be lower-cased.
func isSubsequence(s, q string) bool {
	qi := 0
	qrunes := []rune(q)
	if len(qrunes) == 0 {
		return true
	}
	for _, r := range s {
		if qi >= len(qrunes) {
			break
		}
		if r == qrunes[qi] {
			qi++
		}
	}
	return qi == len(qrunes)
}

// lessNoQuery is the comparator used when the query is empty:
// Recency DESC, then DisplayName ASC.
func (m *Model) lessNoQuery(ai, bi int) bool {
	a, b := m.users[ai], m.users[bi]
	if a.Recency != b.Recency {
		return a.Recency > b.Recency
	}
	return strings.ToLower(a.DisplayName) < strings.ToLower(b.DisplayName)
}

// typedID reports whether q is a complete Matrix user ID
// ("@localpart:server"), which may be messaged without being known.
func typedID(q string) bool {
	local, server, ok := strings.Cut(q, ":")
	return ok && len(local) > 1 && local[0] == '@' && server != "" &&
		!strings.ContainsAny(q, " \t")
}

// dropTyped removes the synthesized typed-ID row unless it has been
// selected as a pill, in which case it becomes an ordinary user.
func (m *Model) dropTyped() {
	if m.typed < 0 {
		return
	}
	if _, kept := m.selected[m.users[m.typed].ID]; !kept {
		m.users = m.users[:m.typed]
	}
	m.typed = -1
}

// addTyped puts the user whose ID is the query at the top of the
// list, synthesizing a row for an ID the picker has not seen.
func (m *Model) addTyped() {
	q := strings.TrimSpace(m.query)
	if !typedID(q) || q == m.currentUserID {
		return
	}
	idx := slices.IndexFunc(m.users, func(u User) bool { return u.ID == q })
	if idx < 0 {
		// Clip so the append never writes into the caller's array.
		m.users = append(slices.Clip(m.users), User{ID: q, DisplayName: q, Username: q[1:]})
		idx = len(m.users) - 1
		m.typed = idx
	}
	m.filtered = slices.DeleteFunc(m.filtered, func(i int) bool { return i == idx })
	m.filtered = slices.Insert(m.filtered, 0, idx)
}
