package verify

import "testing"

func TestMajorVersion(t *testing.T) {
	cases := []struct {
		name     string
		recorded string
		want     string
	}{
		{name: "patch release", recorded: "17.11", want: "17"},
		{name: "major only", recorded: "17", want: "17"},
		{name: "beta", recorded: "18beta1", want: "18"},
		{name: "release candidate", recorded: "18rc2", want: "18"},
		{name: "surrounding whitespace", recorded: " 16.3\n", want: "16"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := majorVersion(tc.recorded)
			if err != nil {
				t.Fatalf("majorVersion(%q) error = %v", tc.recorded, err)
			}
			if got != tc.want {
				t.Errorf("majorVersion(%q) = %q, want %q", tc.recorded, got, tc.want)
			}
		})
	}
}

func TestMajorVersion_Unusable(t *testing.T) {
	for _, recorded := range []string{"", "   ", "unknown", "v17.11"} {
		t.Run(recorded, func(t *testing.T) {
			if _, err := majorVersion(recorded); err == nil {
				t.Errorf("majorVersion(%q) error = nil, want error", recorded)
			}
		})
	}
}

func TestHostPort(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{name: "single address", out: "127.0.0.1:49154", want: "49154"},
		{name: "trailing newline", out: "127.0.0.1:49154\n", want: "49154"},
		{
			name: "published on several addresses",
			out:  "127.0.0.1:49154\n[::1]:49155",
			want: "49154",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostPort(tc.out); got != tc.want {
				t.Errorf("hostPort(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// Two containers started in the same second must not share a password.
func TestRandomPassword(t *testing.T) {
	seen := make(map[string]bool)

	for range 100 {
		password, err := randomPassword()
		if err != nil {
			t.Fatalf("randomPassword() error = %v", err)
		}
		if len(password) != 32 {
			t.Fatalf("randomPassword() = %q, want 32 hex characters", password)
		}
		if seen[password] {
			t.Fatal("randomPassword() repeated a password")
		}
		seen[password] = true
	}
}
