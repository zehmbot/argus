// Package retention decides which backups to keep.
//
// The policy is a pure function: manifests and a reference time in, a plan
// out. No network, no clock, no storage. That is what makes it possible to
// test the awkward cases exhaustively — short months, year boundaries, gaps
// in history, backups that have never been verified — rather than hoping
// they never arise.
package retention

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/zehmbot/argus/internal/manifest"
)

// Policy is a grandfather-father-son retention: keep the most recent backup
// of each of the last Daily days, Weekly weeks, and Monthly months that have
// one.
//
// Periods are counted from the backups that exist, not from the current
// time. Counting from the clock would mean a system that stopped backing up
// a month ago has every daily slot expire at once, deleting almost
// everything precisely when nothing new is arriving to replace it.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Default is the policy used when the config does not set one.
var Default = Policy{Daily: 7, Weekly: 4, Monthly: 6}

// Plan is what prune would do, split into what survives and what does not.
type Plan struct {
	Keep   []manifest.Manifest
	Delete []manifest.Manifest
}

// ErrNoVerifiedBackup reports that carrying out the plan would leave no
// backup that has been proven to restore.
var ErrNoVerifiedBackup = errors.New("no verified backup would remain")

// Apply returns the plan for manifests under policy.
//
// It refuses rather than returning a plan when deleting would leave nothing
// that has ever been verified. The rule belongs here rather than in the
// caller: it is part of what this policy means, and a caller that forgot to
// apply it would delete the last known-good backup without ever being wrong
// about the retention counts.
func Apply(manifests []manifest.Manifest, now time.Time, policy Policy) (Plan, error) {
	sorted := slices.Clone(manifests)

	// Newest first, with the id breaking ties so the plan is identical for
	// identical input.
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}

		return sorted[i].BackupID > sorted[j].BackupID
	})

	keep := make(map[string]bool, len(sorted))

	// A backup dated in the future is a clock that went wrong somewhere, not
	// a backup that has outlived its usefulness. Keep it, and do not let it
	// occupy a slot that a real backup should have.
	for _, m := range sorted {
		if m.CreatedAt.After(now) {
			keep[m.BackupID] = true
		}
	}

	markPeriods(sorted, keep, now, policy.Daily, dayKey)
	markPeriods(sorted, keep, now, policy.Weekly, weekKey)
	markPeriods(sorted, keep, now, policy.Monthly, monthKey)

	var plan Plan

	for _, m := range sorted {
		if keep[m.BackupID] {
			plan.Keep = append(plan.Keep, m)

			continue
		}

		plan.Delete = append(plan.Delete, m)
	}

	// Only a plan that actually deletes something can lose the last good
	// backup. Refusing when there is nothing to delete would turn a nightly
	// prune into a nightly false alarm; whether any backup has been verified
	// at all is verify's business to report.
	if len(plan.Delete) > 0 && !anyVerified(plan.Keep) {
		return Plan{Keep: sorted}, ErrNoVerifiedBackup
	}

	return plan, nil
}

// markPeriods keeps the newest backup of each of the most recent limit
// periods that contain one.
func markPeriods(sorted []manifest.Manifest, keep map[string]bool, now time.Time, limit int, key func(time.Time) string) {
	if limit <= 0 {
		return
	}

	seen := make(map[string]bool, limit)

	for _, m := range sorted {
		// Already kept unconditionally, and must not use up a slot.
		if m.CreatedAt.After(now) {
			continue
		}

		period := key(m.CreatedAt)
		if seen[period] {
			continue
		}

		if len(seen) >= limit {
			return
		}

		seen[period] = true
		keep[m.BackupID] = true
	}
}

// The period keys are computed in UTC, the same way manifests record their
// timestamps. Bucketing in local time would make a backup taken near
// midnight land in a different day depending on the host's zone, and would
// give a daylight saving transition the power to merge or split a period.
func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// weekKey uses the ISO week, which is what makes the last week of December
// and the first of January fall out correctly.
func weekKey(t time.Time) string {
	year, week := t.UTC().ISOWeek()

	return fmt.Sprintf("%04d-W%02d", year, week)
}

func monthKey(t time.Time) string {
	return t.UTC().Format("2006-01")
}

func anyVerified(manifests []manifest.Manifest) bool {
	for _, m := range manifests {
		if m.Verification.Status == manifest.StatusVerified {
			return true
		}
	}

	return false
}
