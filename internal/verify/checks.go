// Package verify restores a backup into a throwaway database and checks the
// result against what the manifest recorded when the backup was taken.
package verify

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zehmbot/argus/internal/manifest"
)

// Check names, recorded in the manifest so that a failure says which part of
// verification failed rather than only that it did.
const (
	CheckRestoreExitCode   = "restore_exit_code"
	CheckSchemaFingerprint = "schema_fingerprint_match"
	CheckRowCounts         = "row_counts_within_tolerance"
	CheckSmokeQuery        = "user_smoke_query"
)

// DefaultRowCountTolerance is the fraction by which a restored table's row
// count may differ from the count recorded at backup time.
//
// It is not zero on purpose. The counts are read before pg_dump starts, and
// pg_dump then runs in its own consistent snapshot, so on a database that is
// being written to the two legitimately disagree. A tolerance of zero would
// fail verification on every live system, and a check that always fails is
// one an operator learns to ignore — which is worse than not running it.
const DefaultRowCountTolerance = 0.01

// restoreCheck records whether pg_restore succeeded.
func restoreCheck(err error) manifest.Check {
	check := manifest.Check{Name: CheckRestoreExitCode, Passed: err == nil}
	if err != nil {
		check.Detail = err.Error()
	}

	return check
}

// fingerprintCheck compares the restored schema against the recorded one.
//
// This is what catches a restore that appears to work but produced a
// different database: a column dropped by a migration that ran between the
// dump and the restore, or a dump taken from the wrong source.
func fingerprintCheck(recorded, restored string) manifest.Check {
	check := manifest.Check{Name: CheckSchemaFingerprint, Passed: recorded == restored}
	if !check.Passed {
		check.Detail = fmt.Sprintf("backup recorded %s, restored database is %s", recorded, restored)
	}

	return check
}

// rowCountCheck compares every table's row count against what the backup
// recorded, allowing each to drift by at most tolerance.
func rowCountCheck(recorded, restored map[string]int64, tolerance float64) manifest.Check {
	var problems []string

	for table, want := range recorded {
		got, present := restored[table]
		if !present {
			problems = append(problems, fmt.Sprintf("%s is missing from the restore", table))

			continue
		}

		if !withinTolerance(want, got, tolerance) {
			problems = append(problems, fmt.Sprintf("%s restored %d rows, backup recorded %d", table, got, want))
		}
	}

	// A table nobody asked for is as much a sign of a wrong restore as a
	// missing one.
	for table := range restored {
		if _, present := recorded[table]; !present {
			problems = append(problems, fmt.Sprintf("%s was restored but is not in the backup", table))
		}
	}

	// Map iteration order is random, so sort: a check's detail must not
	// change between runs on identical input.
	sort.Strings(problems)

	check := manifest.Check{Name: CheckRowCounts, Passed: len(problems) == 0}
	if !check.Passed {
		check.Detail = strings.Join(problems, "; ")
	}

	return check
}

// withinTolerance reports whether got is within tolerance of want, as a
// fraction of want.
func withinTolerance(want, got int64, tolerance float64) bool {
	if want == got {
		return true
	}

	// Nothing to take a percentage of: a table recorded as empty that comes
	// back with rows in it is wrong by any measure.
	if want == 0 {
		return false
	}

	drift := math.Abs(float64(got-want)) / math.Abs(float64(want))

	return drift <= tolerance
}

// Passed reports whether every check passed.
func Passed(checks []manifest.Check) bool {
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}

	return true
}
