package dataset

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marianogappa/scfingerprint/internal/features"
)

func TestLoadEmbedded(t *testing.T) {
	ids, fps, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no identities loaded")
	}
	if len(ids) != len(fps) {
		t.Fatalf("ids (%d) != fps (%d)", len(ids), len(fps))
	}
	for i, id := range ids {
		if id.ID == "" {
			t.Fatalf("identity %d has empty ID", i)
		}
		if id.Confidence == "" {
			t.Fatalf("%s has empty confidence", id.ID)
		}
		if fps[i].N() < 1 {
			t.Fatalf("%s fingerprint has 0 games", id.ID)
		}
		if fps[i].Version() != features.Version {
			t.Fatalf("%s has feature version %d, want %d", id.ID, fps[i].Version(), features.Version)
		}
		if len(id.Aliases) == 0 {
			t.Fatalf("%s has no aliases", id.ID)
		}
		hasPrimary := false
		for _, a := range id.Aliases {
			if a.Primary {
				hasPrimary = true
			}
		}
		if !hasPrimary {
			t.Fatalf("%s has no primary alias", id.ID)
		}
	}
}

func TestLoadEmbeddedSorted(t *testing.T) {
	ids, _, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i].ID < ids[i-1].ID {
			t.Fatalf("identities not sorted: %s before %s", ids[i-1].ID, ids[i].ID)
		}
	}
}

func TestNewDefaultDatasetConfidenceFilter(t *testing.T) {
	all, err := NewDefaultDataset(nil, ConfidenceCandidate)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := NewDefaultDataset(nil, ConfidenceConfirmed)
	if err != nil {
		t.Fatal(err)
	}
	high, err := NewDefaultDataset(nil, ConfidenceHigh)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Len() > high.Len() || high.Len() > all.Len() {
		t.Fatalf("confidence filter ordering broken: confirmed=%d high=%d all=%d", confirmed.Len(), high.Len(), all.Len())
	}
	if confirmed.Len() == 0 {
		t.Fatal("no confirmed identities")
	}
}

func TestMeetsConfidence(t *testing.T) {
	cases := []struct {
		actual, min string
		want        bool
	}{
		{"confirmed", "confirmed", true},
		{"confirmed", "high", true},
		{"confirmed", "candidate", true},
		{"high", "confirmed", false},
		{"high", "high", true},
		{"high", "candidate", true},
		{"candidate", "confirmed", false},
		{"candidate", "high", false},
		{"candidate", "candidate", true},
	}
	for _, c := range cases {
		if got := meetsConfidence(c.actual, c.min); got != c.want {
			t.Errorf("meetsConfidence(%q, %q) = %v, want %v", c.actual, c.min, got, c.want)
		}
	}
}

func TestFeatureVersionCoupled(t *testing.T) {
	ids, fps, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for i, fp := range fps {
		if fp.Version() != features.Version {
			t.Fatalf("%s: feature version %d != current %d — dataset needs re-derivation", ids[i].ID, fp.Version(), features.Version)
		}
	}
}

func TestIdentitiesHaveReplayManifest(t *testing.T) {
	ids, fps, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if len(id.ReplayManifest) == 0 {
			t.Fatalf("%s has no replay manifest", id.ID)
		}
		if len(id.ReplayManifest) != fps[i].N() {
			t.Fatalf("%s: manifest has %d entries but fingerprint has %d games", id.ID, len(id.ReplayManifest), fps[i].N())
		}
	}
}

// Every liquipedia.json key must name an existing identity, and every URL
// must point at the wiki; a typo here would silently drop the link.
func TestLiquipediaLinksMatchIdentities(t *testing.T) {
	var links map[string]string
	if err := json.Unmarshal(liquipediaJSON, &links); err != nil {
		t.Fatalf("parsing liquipedia.json: %v", err)
	}
	if len(links) == 0 {
		t.Fatal("liquipedia.json is empty")
	}
	ids, _, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Identity{}
	for _, id := range ids {
		byID[id.ID] = id
	}
	for pid, url := range links {
		if _, ok := byID[pid]; !ok {
			t.Errorf("liquipedia.json entry %q matches no identity", pid)
		}
		if !strings.HasPrefix(url, "https://liquipedia.net/starcraft/") {
			t.Errorf("liquipedia.json entry %q has malformed URL %q", pid, url)
		}
	}
	for pid, url := range links {
		if got := byID[pid].Liquipedia; got != url {
			t.Errorf("identity %q loaded liquipedia %q, want %q", pid, got, url)
		}
	}
}
