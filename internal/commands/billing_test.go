package commands

import "testing"

func TestParsePeriodFrom(t *testing.T) {
	cases := []struct {
		in        string
		wantEmpty bool
		wantLabel string
	}{
		{"", true, ""},
		{"7d", false, "7d"},
		{"24h", false, "24h"},
		{"2w", false, "2w"},
		{"bad", true, ""},
		{"0d", true, ""},
		{"-3d", true, ""},
		{"5x", true, ""},
	}
	for _, c := range cases {
		from, label := parsePeriodFrom(c.in)
		if c.wantEmpty && from != "" {
			t.Errorf("parsePeriodFrom(%q) from = %q, want empty", c.in, from)
		}
		if !c.wantEmpty && from == "" {
			t.Errorf("parsePeriodFrom(%q) from is empty, want a timestamp", c.in)
		}
		if label != c.wantLabel {
			t.Errorf("parsePeriodFrom(%q) label = %q, want %q", c.in, label, c.wantLabel)
		}
	}
}
