package postgres

import (
	"slices"
	"testing"
)

func TestRestoreArgs(t *testing.T) {
	const dsn = "postgres://argus:argus@localhost:5432/restored?sslmode=disable"

	got := RestoreArgs(dsn)
	want := []string{
		"--dbname=" + dsn,
		"--single-transaction",
		"--exit-on-error",
	}

	if !slices.Equal(got, want) {
		t.Errorf("RestoreArgs() = %q, want %q", got, want)
	}
}

// Without --exit-on-error pg_restore can skip objects and still exit zero,
// which would report a successful restore that silently lost data.
func TestRestoreArgs_FailsLoudly(t *testing.T) {
	got := RestoreArgs("postgres://localhost/db")

	for _, want := range []string{"--exit-on-error", "--single-transaction"} {
		if !slices.Contains(got, want) {
			t.Errorf("RestoreArgs() = %q, missing %q", got, want)
		}
	}
}
