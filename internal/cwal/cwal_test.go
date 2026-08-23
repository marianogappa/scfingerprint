package cwal

import (
	"testing"
)

func TestLoadDefaultList(t *testing.T) {
	entries, err := LoadDefaultList()
	if err != nil {
		t.Fatalf("LoadDefaultList: %v", err)
	}
	if len(entries) != 128 {
		t.Fatalf("expected 128 nicknames, got %d", len(entries))
	}
	totalAccounts := 0
	for _, e := range entries {
		if e.Nickname == "" {
			t.Fatal("empty nickname")
		}
		if len(e.Accounts) == 0 {
			t.Fatalf("%s has no accounts", e.Nickname)
		}
		for _, a := range e.Accounts {
			if a.AuroraID == 0 {
				t.Fatalf("%s has zero aurora_id", e.Nickname)
			}
		}
		totalAccounts += len(e.Accounts)
	}
	if totalAccounts != 152 {
		t.Fatalf("expected 152 total accounts, got %d", totalAccounts)
	}
}

func TestLoadDefaultListSorted(t *testing.T) {
	entries, err := LoadDefaultList()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Nickname < entries[i-1].Nickname {
			t.Fatalf("not sorted: %s before %s", entries[i-1].Nickname, entries[i].Nickname)
		}
	}
}

func TestAuroraIDs(t *testing.T) {
	entries, err := LoadDefaultList()
	if err != nil {
		t.Fatal(err)
	}
	ids := AuroraIDs(entries)
	if len(ids) == 0 {
		t.Fatal("no aurora IDs")
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("not sorted/deduped: %d then %d", ids[i-1], ids[i])
		}
	}
}

func TestLookupByNickname(t *testing.T) {
	entries, err := LoadDefaultList()
	if err != nil {
		t.Fatal(err)
	}
	e := LookupByNickname(entries, "Absolute")
	if e == nil {
		t.Fatal("Absolute not found")
	}
	if len(e.Accounts) != 2 {
		t.Fatalf("Absolute should have 2 accounts, got %d", len(e.Accounts))
	}
	if LookupByNickname(entries, "nonexistent_player_xyz") != nil {
		t.Fatal("should not find nonexistent player")
	}
}

func TestLookupByAuroraID(t *testing.T) {
	entries, err := LoadDefaultList()
	if err != nil {
		t.Fatal(err)
	}
	// 702723014 is Absolute's first account (NaBi)
	found := LookupByAuroraID(entries, 702723014)
	if len(found) == 0 {
		t.Fatal("should find Absolute by aurora_id 702723014")
	}
	if found[0].Nickname != "Absolute" {
		t.Fatalf("expected Absolute, got %s", found[0].Nickname)
	}
	if len(LookupByAuroraID(entries, 0)) > 0 {
		t.Fatal("should not find anything for aurora_id 0")
	}
}

func TestParseDefaultListBadJSON(t *testing.T) {
	if _, err := ParseDefaultList([]byte("not json")); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
