package main

import (
	"testing"

	"github.com/marianogappa/scfingerprint/internal/training"
)

func samples(n int) []training.Sample {
	return make([]training.Sample, n)
}

// When every merge is rejected the anchor group becomes the whole fingerprint,
// so the identity is only as well-evidenced as whichever account leads. Sai
// shipped from a 20-game account while a 58-game one sat behind it in the
// mapping.
func TestAnchorLargestFirstPutsTheBestEvidencedAccountFirst(t *testing.T) {
	groups := [][]training.Sample{samples(20), samples(58), samples(41)}
	ids := []string{"18665802", "21027124", "99999999"}

	gotGroups, gotIDs := anchorLargestFirst(groups, ids)

	if gotIDs[0] != "21027124" {
		t.Errorf("anchor = %s, want the 58-game account 21027124", gotIDs[0])
	}
	wantSizes := []int{58, 41, 20}
	for i, want := range wantSizes {
		if len(gotGroups[i]) != want {
			t.Errorf("position %d has %d games, want %d", i, len(gotGroups[i]), want)
		}
	}
	if len(gotGroups) != len(groups) || len(gotIDs) != len(ids) {
		t.Errorf("dropped groups: %d groups and %d ids, want %d of each", len(gotGroups), len(gotIDs), len(groups))
	}
}

// Equal-sized accounts must keep mapping order, so re-running the seeder over
// an unchanged corpus cannot silently pick a different anchor.
func TestAnchorLargestFirstIsStableOnTies(t *testing.T) {
	groups := [][]training.Sample{samples(30), samples(30), samples(30)}
	ids := []string{"a", "b", "c"}

	_, gotIDs := anchorLargestFirst(groups, ids)

	for i, want := range ids {
		if gotIDs[i] != want {
			t.Fatalf("order = %v, want mapping order %v", gotIDs, ids)
		}
	}
}

func TestAnchorLargestFirstHandlesASingleAccount(t *testing.T) {
	groups := [][]training.Sample{samples(7)}
	_, gotIDs := anchorLargestFirst(groups, []string{"only"})
	if len(gotIDs) != 1 || gotIDs[0] != "only" {
		t.Errorf("got %v, want [only]", gotIDs)
	}
}
