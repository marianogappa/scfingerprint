package main

import (
	"bytes"
	"strings"
	"testing"

	scfingerprint "github.com/marianogappa/scfingerprint"
)

func renderSame(gamesA, gamesB int) string {
	evidenceN := min(gamesA, gamesB)
	v := scfingerprint.Verdict{Z: 4.2, Cosine: 0.9, FPR: 0.0001, EvidenceN: evidenceN, GamesA: gamesA, GamesB: gamesB}
	r := sameReport{Tier: sameVerdict(v.FPR, v.EvidenceN), Verdict: v}
	var buf bytes.Buffer
	renderSameReport(&buf, newPalette(false), 1, verdictLead, r)
	return buf.String()
}

func TestSameReportCountsTheSmallerSide(t *testing.T) {
	cases := []struct {
		name           string
		gamesA, gamesB int
		want           []string
		notWant        []string
	}{
		{
			name: "1 vs many", gamesA: 1, gamesB: 65,
			want:    []string{"Confidence: lead (1 game of evidence)", "on 1 + 65 games", "Only 1 game of evidence on the smaller side keeps this a lead", "Evidence: 1 game (1 + 65)"},
			notWant: []string{"66", "Confidence: strong", "makes this strong"},
		},
		{
			name: "many vs 1", gamesA: 65, gamesB: 1,
			want:    []string{"Confidence: lead (1 game of evidence)", "on 65 + 1 games"},
			notWant: []string{"66", "Confidence: strong", "makes this strong"},
		},
		{
			name: "many vs many", gamesA: 6, gamesB: 20,
			want:    []string{"Confidence: strong (6 games of evidence)", "on 6 + 20 games", "6+ games on each side agreeing"},
			notWant: []string{"26"},
		},
	}
	for _, c := range cases {
		out := renderSame(c.gamesA, c.gamesB)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in:\n%s", c.name, w, out)
			}
		}
		for _, nw := range c.notWant {
			if strings.Contains(out, nw) {
				t.Errorf("%s: unexpected %q in:\n%s", c.name, nw, out)
			}
		}
	}
}
