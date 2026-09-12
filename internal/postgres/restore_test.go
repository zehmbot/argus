package postgres

import (
	"slices"
	"testing"
)

func TestRestoreArgs(t *testing.T) {
	const dsn = "postgres://argus:argus@localhost:5432/restored?sslmode=disable"

	got := RestoreArgs(dsn, RestoreOptions{})
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
	got := RestoreArgs("postgres://localhost/db", RestoreOptions{})

	for _, want := range []string{"--exit-on-error", "--single-transaction"} {
		if !slices.Contains(got, want) {
			t.Errorf("RestoreArgs() = %q, missing %q", got, want)
		}
	}
}

// A real restore must preserve ownership; only verification, which restores
// into a cluster that has none of the source's roles, opts out.
func TestRestoreArgs_NoOwner(t *testing.T) {
	plain := RestoreArgs("postgres://localhost/db", RestoreOptions{})
	if slices.Contains(plain, "--no-owner") {
		t.Error("RestoreArgs() includes --no-owner by default, want ownership preserved")
	}

	noOwner := RestoreArgs("postgres://localhost/db", RestoreOptions{NoOwner: true})
	if !slices.Contains(noOwner, "--no-owner") {
		t.Errorf("RestoreArgs(NoOwner) = %q, missing --no-owner", noOwner)
	}
}
