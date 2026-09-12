package verify

import (
	"errors"
	"strings"
	"testing"

	"github.com/zehmbot/argus/internal/manifest"
)

func TestRestoreCheck(t *testing.T) {
	if got := restoreCheck(nil); !got.Passed || got.Detail != "" {
		t.Errorf("restoreCheck(nil) = %+v, want passed with no detail", got)
	}

	got := restoreCheck(errors.New("pg_restore: exit status 1: relation already exists"))
	if got.Passed {
		t.Error("restoreCheck() passed on a failed restore")
	}
	if !strings.Contains(got.Detail, "relation already exists") {
		t.Errorf("Detail = %q, want it to carry the restore error", got.Detail)
	}
	if got.Name != CheckRestoreExitCode {
		t.Errorf("Name = %q, want %q", got.Name, CheckRestoreExitCode)
	}
}

func TestFingerprintCheck(t *testing.T) {
	const recorded = "sha256:abc123"

	if got := fingerprintCheck(recorded, recorded); !got.Passed {
		t.Errorf("fingerprintCheck() = %+v, want passed on identical fingerprints", got)
	}

	got := fingerprintCheck(recorded, "sha256:def456")
	if got.Passed {
		t.Error("fingerprintCheck() passed on differing fingerprints")
	}
	// Both values belong in the detail: knowing they differ is useless
	// without knowing what they are.
	for _, want := range []string{recorded, "sha256:def456"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("Detail = %q, want it to name %q", got.Detail, want)
		}
	}
}

func TestRowCountCheck_Passes(t *testing.T) {
	cases := []struct {
		name      string
		recorded  map[string]int64
		restored  map[string]int64
		tolerance float64
	}{
		{
			name:     "identical",
			recorded: map[string]int64{"users": 25, "orders": 100},
			restored: map[string]int64{"users": 25, "orders": 100},
		},
		{
			name:      "within tolerance",
			recorded:  map[string]int64{"orders": 1000},
			restored:  map[string]int64{"orders": 1009},
			tolerance: 0.01,
		},
		{
			name:      "exactly at the tolerance",
			recorded:  map[string]int64{"orders": 1000},
			restored:  map[string]int64{"orders": 1010},
			tolerance: 0.01,
		},
		{
			name:     "both empty",
			recorded: map[string]int64{"audit": 0},
			restored: map[string]int64{"audit": 0},
		},
		{
			name:     "no tables at all",
			recorded: map[string]int64{},
			restored: map[string]int64{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rowCountCheck(tc.recorded, tc.restored, tc.tolerance)
			if !got.Passed {
				t.Errorf("rowCountCheck() failed: %s", got.Detail)
			}
		})
	}
}

func TestRowCountCheck_Fails(t *testing.T) {
	cases := []struct {
		name       string
		recorded   map[string]int64
		restored   map[string]int64
		tolerance  float64
		wantDetail string
	}{
		{
			name:       "just outside the tolerance",
			recorded:   map[string]int64{"orders": 1000},
			restored:   map[string]int64{"orders": 1011},
			tolerance:  0.01,
			wantDetail: "orders restored 1011 rows, backup recorded 1000",
		},
		{
			name:       "table missing from the restore",
			recorded:   map[string]int64{"users": 25, "orders": 100},
			restored:   map[string]int64{"users": 25},
			wantDetail: "orders is missing from the restore",
		},
		{
			name:       "unexpected table in the restore",
			recorded:   map[string]int64{"users": 25},
			restored:   map[string]int64{"users": 25, "leftovers": 3},
			wantDetail: "leftovers was restored but is not in the backup",
		},
		{
			name:       "empty table came back with rows",
			recorded:   map[string]int64{"audit": 0},
			restored:   map[string]int64{"audit": 5},
			tolerance:  0.5,
			wantDetail: "audit restored 5 rows, backup recorded 0",
		},
		{
			name:       "rows lost entirely",
			recorded:   map[string]int64{"orders": 100},
			restored:   map[string]int64{"orders": 0},
			tolerance:  0.01,
			wantDetail: "orders restored 0 rows, backup recorded 100",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rowCountCheck(tc.recorded, tc.restored, tc.tolerance)
			if got.Passed {
				t.Fatal("rowCountCheck() passed, want failure")
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("Detail = %q, want it to contain %q", got.Detail, tc.wantDetail)
			}
		})
	}
}

// Map iteration order is random, so a detail built from several problems has
// to be sorted or the same failure reads differently on every run.
func TestRowCountCheck_DetailIsStable(t *testing.T) {
	recorded := map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
	restored := map[string]int64{}

	first := rowCountCheck(recorded, restored, 0).Detail

	for range 20 {
		if got := rowCountCheck(recorded, restored, 0).Detail; got != first {
			t.Fatalf("detail changed between runs:\n%s\n%s", first, got)
		}
	}
}

func TestPassed(t *testing.T) {
	cases := []struct {
		name   string
		checks []manifest.Check
		want   bool
	}{
		{name: "no checks", checks: nil, want: true},
		{
			name:   "all passed",
			checks: []manifest.Check{{Name: "a", Passed: true}, {Name: "b", Passed: true}},
			want:   true,
		},
		{
			name:   "one failed",
			checks: []manifest.Check{{Name: "a", Passed: true}, {Name: "b", Passed: false}},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Passed(tc.checks); got != tc.want {
				t.Errorf("Passed() = %v, want %v", got, tc.want)
			}
		})
	}
}
