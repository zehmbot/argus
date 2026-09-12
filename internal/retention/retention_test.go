package retention

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/zehmbot/argus/internal/manifest"
)

// at builds a verified backup taken at the given time. Verified by default
// because most cases are about the retention counts; the ones about the
// safety rule say otherwise explicitly.
func at(id string, t time.Time) manifest.Manifest {
	return manifest.Manifest{
		BackupID:     id,
		CreatedAt:    t,
		Verification: manifest.Verification{Status: manifest.StatusVerified},
	}
}

func pending(id string, t time.Time) manifest.Manifest {
	m := at(id, t)
	m.Verification.Status = manifest.StatusPending

	return m
}

func utc(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}

// ids returns sorted backup ids, so assertions do not depend on plan order.
func ids(manifests []manifest.Manifest) []string {
	out := make([]string, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, m.BackupID)
	}
	sort.Strings(out)

	return out
}

func assertPlan(t *testing.T, plan Plan, wantKeep, wantDelete []string) {
	t.Helper()

	sort.Strings(wantKeep)
	sort.Strings(wantDelete)

	if got := ids(plan.Keep); !slices.Equal(got, wantKeep) {
		t.Errorf("keep = %v, want %v", got, wantKeep)
	}
	if got := ids(plan.Delete); !slices.Equal(got, wantDelete) {
		t.Errorf("delete = %v, want %v", got, wantDelete)
	}
}

func TestApply(t *testing.T) {
	// Later than every backup the cases below treat as already taken, so that
	// only the deliberately future-dated one counts as clock skew.
	defaultNow := utc(2026, time.March, 16, 0)

	cases := []struct {
		name      string
		policy    Policy
		manifests []manifest.Manifest
		// now overrides defaultNow for cases set in another year.
		now        time.Time
		wantKeep   []string
		wantDelete []string
	}{
		{
			name:      "no backups at all",
			policy:    Default,
			manifests: nil,
		},
		{
			name:      "fewer backups than the policy allows",
			policy:    Policy{Daily: 7},
			manifests: []manifest.Manifest{at("a", utc(2026, time.March, 15, 3)), at("b", utc(2026, time.March, 14, 3))},
			wantKeep:  []string{"a", "b"},
		},
		{
			name:   "one per day, older than the daily count",
			policy: Policy{Daily: 3},
			manifests: []manifest.Manifest{
				at("d15", utc(2026, time.March, 15, 3)),
				at("d14", utc(2026, time.March, 14, 3)),
				at("d13", utc(2026, time.March, 13, 3)),
				at("d12", utc(2026, time.March, 12, 3)),
				at("d11", utc(2026, time.March, 11, 3)),
			},
			wantKeep:   []string{"d15", "d14", "d13"},
			wantDelete: []string{"d12", "d11"},
		},
		{
			name:   "several backups in one day keep only the newest",
			policy: Policy{Daily: 2},
			manifests: []manifest.Manifest{
				at("morning", utc(2026, time.March, 15, 3)),
				at("noon", utc(2026, time.March, 15, 12)),
				at("evening", utc(2026, time.March, 15, 21)),
				at("yesterday", utc(2026, time.March, 14, 3)),
			},
			wantKeep:   []string{"evening", "yesterday"},
			wantDelete: []string{"morning", "noon"},
		},
		{
			name:   "gaps in history do not consume slots",
			policy: Policy{Daily: 3},
			manifests: []manifest.Manifest{
				at("mar15", utc(2026, time.March, 15, 3)),
				at("mar01", utc(2026, time.March, 1, 3)),
				at("feb01", utc(2026, time.February, 1, 3)),
				at("jan01", utc(2026, time.January, 1, 3)),
			},
			// Three days that have a backup, however far apart they are.
			wantKeep:   []string{"mar15", "mar01", "feb01"},
			wantDelete: []string{"jan01"},
		},
		{
			name:   "weekly keeps one per ISO week",
			policy: Policy{Weekly: 2},
			manifests: []manifest.Manifest{
				at("w11a", utc(2026, time.March, 12, 3)),
				at("w11b", utc(2026, time.March, 9, 3)),
				at("w10", utc(2026, time.March, 5, 3)),
				at("w09", utc(2026, time.February, 26, 3)),
			},
			wantKeep:   []string{"w11a", "w10"},
			wantDelete: []string{"w11b", "w09"},
		},
		{
			name:   "monthly keeps one per calendar month",
			policy: Policy{Monthly: 3},
			manifests: []manifest.Manifest{
				at("mar", utc(2026, time.March, 15, 3)),
				at("feb", utc(2026, time.February, 20, 3)),
				at("jan", utc(2026, time.January, 31, 3)),
				at("dec", utc(2025, time.December, 25, 3)),
			},
			wantKeep:   []string{"mar", "feb", "jan"},
			wantDelete: []string{"dec"},
		},
		{
			name:   "february is not special",
			policy: Policy{Monthly: 2},
			manifests: []manifest.Manifest{
				at("feb28", utc(2026, time.February, 28, 3)),
				at("feb01", utc(2026, time.February, 1, 3)),
				at("jan31", utc(2026, time.January, 31, 3)),
			},
			wantKeep:   []string{"feb28", "jan31"},
			wantDelete: []string{"feb01"},
		},
		{
			name:   "a leap day is an ordinary day",
			policy: Policy{Daily: 2},
			now:    utc(2028, time.March, 1, 0),
			manifests: []manifest.Manifest{
				at("leap", utc(2028, time.February, 29, 3)),
				at("before", utc(2028, time.February, 28, 3)),
				at("older", utc(2028, time.February, 27, 3)),
			},
			wantKeep:   []string{"leap", "before"},
			wantDelete: []string{"older"},
		},
		{
			name:   "a backup kept by one rule is not deleted by another",
			policy: Policy{Daily: 1, Monthly: 2},
			manifests: []manifest.Manifest{
				at("mar15", utc(2026, time.March, 15, 3)),
				at("mar14", utc(2026, time.March, 14, 3)),
				at("feb10", utc(2026, time.February, 10, 3)),
			},
			// mar15 by daily and monthly, feb10 by monthly, mar14 by neither.
			wantKeep:   []string{"mar15", "feb10"},
			wantDelete: []string{"mar14"},
		},
		{
			name:   "the rules combine rather than compete",
			policy: Policy{Daily: 2, Weekly: 2, Monthly: 2},
			manifests: []manifest.Manifest{
				at("mar15", utc(2026, time.March, 15, 3)),
				at("mar14", utc(2026, time.March, 14, 3)),
				at("mar13", utc(2026, time.March, 13, 3)),
				at("mar08", utc(2026, time.March, 8, 3)),
				at("feb15", utc(2026, time.February, 15, 3)),
				at("jan15", utc(2026, time.January, 15, 3)),
			},
			// Daily takes mar15 and mar14. March 13 and 15 share ISO week 11,
			// so weekly adds only mar08 from week 10. Monthly adds feb15.
			wantKeep:   []string{"mar15", "mar14", "mar08", "feb15"},
			wantDelete: []string{"mar13", "jan15"},
		},
		{
			name:   "a zero count disables that rule",
			policy: Policy{Daily: 0, Monthly: 1},
			manifests: []manifest.Manifest{
				at("mar15", utc(2026, time.March, 15, 3)),
				at("mar14", utc(2026, time.March, 14, 3)),
			},
			wantKeep:   []string{"mar15"},
			wantDelete: []string{"mar14"},
		},
		{
			name:   "a backup dated in the future is kept and takes no slot",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				at("skewed", utc(2027, time.January, 1, 3)),
				at("today", utc(2026, time.March, 15, 3)),
				at("yesterday", utc(2026, time.March, 14, 3)),
			},
			wantKeep:   []string{"skewed", "today"},
			wantDelete: []string{"yesterday"},
		},
		{
			name:   "timestamps either side of midnight are different days",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				at("justafter", time.Date(2026, time.March, 15, 0, 5, 0, 0, time.UTC)),
				at("justbefore", time.Date(2026, time.March, 14, 23, 55, 0, 0, time.UTC)),
			},
			wantKeep:   []string{"justafter"},
			wantDelete: []string{"justbefore"},
		},
		{
			name:   "a time in another zone is bucketed by its UTC day",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				// 01:30 on the 15th at +02:00 is 23:30 on the 14th in UTC,
				// the same UTC day as the other backup.
				at("plus2", time.Date(2026, time.March, 15, 1, 30, 0, 0, time.FixedZone("EET", 2*60*60))),
				at("utc14", time.Date(2026, time.March, 14, 23, 0, 0, 0, time.UTC)),
			},
			wantKeep:   []string{"plus2"},
			wantDelete: []string{"utc14"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.now
			if now.IsZero() {
				now = defaultNow
			}

			plan, err := Apply(tc.manifests, now, tc.policy)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}

			assertPlan(t, plan, tc.wantKeep, tc.wantDelete)
		})
	}
}

// The ISO week is what makes the turn of the year work: 31 December 2025 and
// 1 January 2026 fall in the same ISO week, so a weekly rule of one keeps
// only the newer.
func TestApply_YearBoundary(t *testing.T) {
	now := utc(2026, time.January, 10, 12)

	plan, err := Apply([]manifest.Manifest{
		at("jan01", utc(2026, time.January, 1, 3)),
		at("dec31", utc(2025, time.December, 31, 3)),
	}, now, Policy{Weekly: 1})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	assertPlan(t, plan, []string{"jan01"}, []string{"dec31"})
}

func TestApply_SafetyRule(t *testing.T) {
	now := utc(2026, time.March, 15, 12)

	cases := []struct {
		name      string
		policy    Policy
		manifests []manifest.Manifest
		wantErr   bool
	}{
		{
			name:   "refuses when every surviving backup is unverified",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				pending("mar15", utc(2026, time.March, 15, 3)),
				pending("mar14", utc(2026, time.March, 14, 3)),
			},
			wantErr: true,
		},
		{
			name:   "refuses when the only verified backup is the one being deleted",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				pending("mar15", utc(2026, time.March, 15, 3)),
				at("mar14", utc(2026, time.March, 14, 3)),
			},
			wantErr: true,
		},
		{
			name:   "allows when a verified backup survives",
			policy: Policy{Daily: 1},
			manifests: []manifest.Manifest{
				at("mar15", utc(2026, time.March, 15, 3)),
				pending("mar14", utc(2026, time.March, 14, 3)),
			},
		},
		{
			name:   "nothing to delete is not a refusal",
			policy: Policy{Daily: 7},
			manifests: []manifest.Manifest{
				pending("mar15", utc(2026, time.March, 15, 3)),
				pending("mar14", utc(2026, time.March, 14, 3)),
			},
		},
		{
			name:      "no backups at all is not a refusal",
			policy:    Default,
			manifests: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Apply(tc.manifests, now, tc.policy)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Apply() error = %v, want none", err)
				}

				return
			}

			if !errors.Is(err, ErrNoVerifiedBackup) {
				t.Fatalf("Apply() error = %v, want ErrNoVerifiedBackup", err)
			}

			// Refusing means deleting nothing, not deleting a little less.
			if len(plan.Delete) != 0 {
				t.Errorf("plan deletes %v despite refusing", ids(plan.Delete))
			}
			if len(plan.Keep) != len(tc.manifests) {
				t.Errorf("plan keeps %d of %d backups, want all of them", len(plan.Keep), len(tc.manifests))
			}
		})
	}
}

// A failed verification is not a verification. Only a backup proven to
// restore protects the rest from being deleted.
func TestApply_FailedVerificationDoesNotCount(t *testing.T) {
	now := utc(2026, time.March, 15, 12)

	failed := at("mar15", utc(2026, time.March, 15, 3))
	failed.Verification.Status = manifest.StatusFailed

	_, err := Apply([]manifest.Manifest{
		failed,
		at("mar14", utc(2026, time.March, 14, 3)),
	}, now, Policy{Daily: 1})

	if !errors.Is(err, ErrNoVerifiedBackup) {
		t.Errorf("Apply() error = %v, want ErrNoVerifiedBackup", err)
	}
}

// Storage makes no promise about the order it lists things in, so the plan
// must not depend on it.
func TestApply_IndependentOfInputOrder(t *testing.T) {
	now := utc(2026, time.March, 15, 12)

	manifests := []manifest.Manifest{
		at("mar15", utc(2026, time.March, 15, 3)),
		at("mar14", utc(2026, time.March, 14, 3)),
		at("mar13", utc(2026, time.March, 13, 3)),
		at("feb01", utc(2026, time.February, 1, 3)),
	}

	forward, err := Apply(manifests, now, Policy{Daily: 2, Monthly: 1})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	reversed := slices.Clone(manifests)
	slices.Reverse(reversed)

	backward, err := Apply(reversed, now, Policy{Daily: 2, Monthly: 1})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if !slices.Equal(ids(forward.Delete), ids(backward.Delete)) {
		t.Errorf("delete differs by input order: %v then %v", ids(forward.Delete), ids(backward.Delete))
	}
}

// Apply must not reorder or otherwise disturb the caller's slice.
func TestApply_DoesNotMutateInput(t *testing.T) {
	now := utc(2026, time.March, 15, 12)

	manifests := []manifest.Manifest{
		at("oldest", utc(2026, time.February, 1, 3)),
		at("newest", utc(2026, time.March, 15, 3)),
	}
	before := fmt.Sprint(manifests)

	if _, err := Apply(manifests, now, Default); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if after := fmt.Sprint(manifests); after != before {
		t.Errorf("Apply() reordered its input:\n%s\n%s", before, after)
	}
}
