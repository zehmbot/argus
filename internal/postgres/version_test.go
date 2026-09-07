package postgres

import "testing"

func TestParseServerVersion(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "upstream build", raw: "17.11", want: "17.11"},
		{name: "packaged build", raw: "17.11 (Debian 17.11-1.pgdg13+2)", want: "17.11"},
		{name: "beta", raw: "18beta1", want: "18beta1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseServerVersion(tc.raw)
			if err != nil {
				t.Fatalf("parseServerVersion() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("parseServerVersion(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseServerVersion_Empty(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if _, err := parseServerVersion(raw); err == nil {
			t.Errorf("parseServerVersion(%q) error = nil, want error", raw)
		}
	}
}
