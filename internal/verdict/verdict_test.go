package verdict

import "testing"

func TestSame(t *testing.T) {
	cases := []struct {
		name      string
		fpr       float64
		evidenceN int
		want      string
	}{
		{"strong rate on one game is a lead", 0.0001, 1, Lead},
		{"strong rate on two games is a lead", 0.0001, 2, Lead},
		{"strong rate on three games is strong", 0.0001, 3, Strong},
		{"lead rate on many games is a lead", 0.01, 30, Lead},
		{"no rate is weak", 1, 30, Weak},
	}
	for _, c := range cases {
		if got := Same(c.fpr, c.evidenceN); got != c.want {
			t.Errorf("%s: Same(%v, %d) = %q, want %q", c.name, c.fpr, c.evidenceN, got, c.want)
		}
	}
}
