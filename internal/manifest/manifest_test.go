package manifest

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	verifiedAt := time.Date(2026, 9, 5, 3, 14, 2, 0, time.UTC)

	return Manifest{
		BackupID:        "2026-09-05T03-00-12Z-a3f9",
		CreatedAt:       time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC),
		DurationSeconds: 84,
		Source: Source{
			Host:          "db.internal",
			Database:      "app_production",
			PGVersion:     "17.11",
			PGDumpVersion: "17.11",
		},
		Artifact: Artifact{
			ObjectKey:   "backups/app_production/2026-09-05T03-00-12Z-a3f9.dump.gz",
			SizeBytes:   418234112,
			SHA256:      "abc123",
			Compression: "gzip",
		},
		SchemaFingerprint: "sha256:def456",
		TableCounts:       map[string]int64{"users": 18422, "orders": 90311},
		Verification: Verification{
			Status:            StatusVerified,
			VerifiedAt:        &verifiedAt,
			RestoredPGVersion: "17.11",
			Checks:            []Check{{Name: "restore_exit_code", Passed: true}},
		},
		ToolVersion: "0.1.0",
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	want := testManifest()

	var buf bytes.Buffer
	if err := want.Encode(&buf); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	got, err := Decode(&buf)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if got.BackupID != want.BackupID {
		t.Errorf("BackupID = %q, want %q", got.BackupID, want.BackupID)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if got.Artifact != want.Artifact {
		t.Errorf("Artifact = %+v, want %+v", got.Artifact, want.Artifact)
	}
	if got.Source != want.Source {
		t.Errorf("Source = %+v, want %+v", got.Source, want.Source)
	}
	if got.SchemaFingerprint != want.SchemaFingerprint {
		t.Errorf("SchemaFingerprint = %q, want %q", got.SchemaFingerprint, want.SchemaFingerprint)
	}
	if len(got.TableCounts) != len(want.TableCounts) {
		t.Fatalf("TableCounts = %v, want %v", got.TableCounts, want.TableCounts)
	}
	for table, count := range want.TableCounts {
		if got.TableCounts[table] != count {
			t.Errorf("TableCounts[%q] = %d, want %d", table, got.TableCounts[table], count)
		}
	}
	if got.Verification.Status != want.Verification.Status {
		t.Errorf("Verification.Status = %q, want %q", got.Verification.Status, want.Verification.Status)
	}
	if got.Verification.VerifiedAt == nil || !got.Verification.VerifiedAt.Equal(*want.Verification.VerifiedAt) {
		t.Errorf("Verification.VerifiedAt = %v, want %v", got.Verification.VerifiedAt, want.Verification.VerifiedAt)
	}
	if len(got.Verification.Checks) != 1 || got.Verification.Checks[0] != want.Verification.Checks[0] {
		t.Errorf("Verification.Checks = %+v, want %+v", got.Verification.Checks, want.Verification.Checks)
	}
	if got.ToolVersion != want.ToolVersion {
		t.Errorf("ToolVersion = %q, want %q", got.ToolVersion, want.ToolVersion)
	}
}

func TestEncode_OmitsEmptyVerificationFields(t *testing.T) {
	m := testManifest()
	m.Verification = Verification{Status: StatusPending}

	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	got := buf.String()
	for _, field := range []string{"verified_at", "restored_pg_version", "checks", "encryption_recipient", "detail"} {
		if strings.Contains(got, field) {
			t.Errorf("Encode() emitted empty field %q:\n%s", field, got)
		}
	}
}

func TestDecode_Rejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "not json", input: "this is not json"},
		{name: "truncated", input: `{"backup_id": "2026-09-05T03-00-12Z-a3f9"`},
		{name: "empty object", input: `{}`},
		{name: "missing backup_id", input: `{"artifact": {"object_key": "backups/db/id.dump.gz"}}`},
		{name: "missing object_key", input: `{"backup_id": "2026-09-05T03-00-12Z-a3f9"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(tc.input)); err == nil {
				t.Error("Decode() error = nil, want error")
			}
		})
	}
}

func TestNewID_Format(t *testing.T) {
	created := time.Date(2026, 9, 5, 3, 0, 12, 0, time.FixedZone("CEST", 2*60*60))

	got, err := NewID(created)
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}

	// The local time above is 01:00:12 UTC; the ID must be in UTC.
	want := regexp.MustCompile(`^2026-09-05T01-00-12Z-[0-9a-f]{4}$`)
	if !want.MatchString(got) {
		t.Errorf("NewID() = %q, want match %s", got, want)
	}
}

func TestNewID_Unique(t *testing.T) {
	created := time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC)

	seen := make(map[string]bool)
	for range 100 {
		id, err := NewID(created)
		if err != nil {
			t.Fatalf("NewID() error = %v", err)
		}
		seen[id] = true
	}

	// 100 draws from 16 bits collide often; only demand that the suffix
	// varies at all, which is what protects same-second backups.
	if len(seen) < 2 {
		t.Errorf("NewID() returned %d distinct IDs for the same instant, want > 1", len(seen))
	}
}

func TestKeys(t *testing.T) {
	const (
		database = "app_production"
		backupID = "2026-09-05T03-00-12Z-a3f9"
	)

	if got, want := ArtifactKey(database, backupID, false), "backups/app_production/2026-09-05T03-00-12Z-a3f9.dump.gz"; got != want {
		t.Errorf("ArtifactKey() = %q, want %q", got, want)
	}
	if got, want := ManifestKey(database, backupID), "backups/app_production/2026-09-05T03-00-12Z-a3f9.json"; got != want {
		t.Errorf("ManifestKey() = %q, want %q", got, want)
	}
}

func TestArtifactKey_Encrypted(t *testing.T) {
	const (
		database = "app_production"
		backupID = "2026-09-05T03-00-12Z-a3f9"
	)

	got := ArtifactKey(database, backupID, true)
	want := "backups/app_production/2026-09-05T03-00-12Z-a3f9.dump.gz.age"

	if got != want {
		t.Errorf("ArtifactKey(encrypted) = %q, want %q", got, want)
	}
}
