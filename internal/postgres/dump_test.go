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
