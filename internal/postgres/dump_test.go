package postgres

import "testing"

func TestDumpArgs(t *testing.T) {
	got := DumpArgs("postgres://user:pass@localhost:5432/app", "/tmp/out.dump")

	want := []string{
		"--format=custom",
		"--compress=0",
		"--file=/tmp/out.dump",
		"postgres://user:pass@localhost:5432/app",
	}

	if len(got) != len(want) {
		t.Fatalf("DumpArgs() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DumpArgs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseDumpVersion(t *testing.T) {
	cases := []struct {
		name   string
		banner string
		want   string
	}{
		{
			name:   "upstream build",
			banner: "pg_dump (PostgreSQL) 17.11\n",
			want:   "17.11",
		},
		{
			name:   "packaged build",
			banner: "pg_dump (PostgreSQL) 17.11 (Debian 17.11-1.pgdg13+2)\n",
			want:   "17.11",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDumpVersion(tc.banner)
			if err != nil {
				t.Fatalf("parseDumpVersion() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("parseDumpVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDumpVersion_Unexpected(t *testing.T) {
	cases := []struct {
		name   string
		banner string
	}{
		{name: "empty", banner: ""},
		{name: "another binary", banner: "psql (PostgreSQL) 17.11\n"},
		{name: "truncated", banner: "pg_dump (PostgreSQL)\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseDumpVersion(tc.banner); err == nil {
				t.Error("parseDumpVersion() error = nil, want error")
			}
		})
	}
}
