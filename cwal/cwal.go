// Package cwal maps pro-gamer nicknames to stable Blizzard account IDs
// (aurora_id) and resolves them to current toons. The seed data comes from
// the CWAL.gg Player Tracker browser extension's default_list.json (128
// nicknames → 152 accounts as of 2026-08-08, curated by WorsT21/Impact44 +
// DudeNerd). aurora_id is Blizzard's persistent account ID — it survives
// name changes and season resets, making the mapping recency-proof.
//
// The complementary API (aurora_id → current toons) uses cwal.gg's public
// Supabase backend.
package cwal

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed default_list.json
var defaultListBytes []byte

// Account is one Blizzard account linked to a nickname.
type Account struct {
	AuroraID  int64  `json:"aurora_id"`
	BattleTag string `json:"battle_tag"`
}

// Entry is one pro-gamer identity: a nickname and its linked accounts.
type Entry struct {
	Nickname string    `json:"nickname"`
	Accounts []Account `json:"accounts"`
}

// Toon is one handle resolved from the cwal.gg API for an aurora_id.
type Toon struct {
	Handle   string `json:"handle"`
	Gateway  int    `json:"gateway"`
	LastSeen string `json:"last_seen,omitempty"`
}

// ResolvedEntry extends Entry with the current toons for each account.
type ResolvedEntry struct {
	Entry
	Toons map[int64][]Toon `json:"toons,omitempty"`
}

// LoadDefaultList parses the embedded default_list.json and returns entries
// sorted by nickname.
func LoadDefaultList() ([]Entry, error) {
	return ParseDefaultList(defaultListBytes)
}

// ParseDefaultList parses a default_list.json payload.
func ParseDefaultList(data []byte) ([]Entry, error) {
	var raw map[string][]Account
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cwal: parsing default_list: %w", err)
	}
	entries := make([]Entry, 0, len(raw))
	for nick, accts := range raw {
		entries = append(entries, Entry{Nickname: nick, Accounts: accts})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Nickname < entries[j].Nickname
	})
	return entries, nil
}

// AuroraIDs returns a deduplicated sorted slice of all aurora IDs in the
// entries.
func AuroraIDs(entries []Entry) []int64 {
	seen := map[int64]bool{}
	for _, e := range entries {
		for _, a := range e.Accounts {
			seen[a.AuroraID] = true
		}
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// LookupByAuroraID returns all entries whose accounts include the given
// aurora_id. Multiple entries can share an aurora_id when a player is known
// by different nicknames.
func LookupByAuroraID(entries []Entry, id int64) []Entry {
	var out []Entry
	for _, e := range entries {
		for _, a := range e.Accounts {
			if a.AuroraID == id {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// LookupByNickname returns the entry for the given nickname, or nil.
func LookupByNickname(entries []Entry, nick string) *Entry {
	for i, e := range entries {
		if e.Nickname == nick {
			return &entries[i]
		}
	}
	return nil
}
