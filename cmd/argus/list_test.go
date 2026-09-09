package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage/local"
)

// A DSN that is never dialled: list only parses it for the database name.
const (
	testDSN      = "postgres://u:p@db.internal:5432/app_production?sslmode=disable"
	testDatabase = "app_production"
)

func seedManifest(t *testing.T, storageRoot, backupID string, createdAt time.Time, size int64) {
	t.Helper()

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	m := manifest.Manifest{
		BackupID:  backupID,
		CreatedAt: createdAt,
		Source:    manifest.Source{Host: "db.internal", Database: testDatabase},
		Artifact: manifest.Artifact{
			ObjectKey:   manifest.ArtifactKey(testDatabase, backupID, false),
			SizeBytes:   size,
			SHA256:      "abc123",
			Compression: "gzip",
		},
		Verification: manifest.Verification{Status: manifest.StatusPending},
		ToolVersion:  version,
	}

	if err := manifest.Write(context.Background(), backend, m); err != nil {
		t.Fatalf("manifest.Write() error = %v", err)
	}
}

func TestRunList_Empty(t *testing.T) {
	configPath, _ := writeConfig(t)
	t.Setenv("ARGUS_DATABASE_URL", testDSN)

	var out bytes.Buffer
	if err := runList(context.Background(), configPath, false, &out); err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	if got := out.String(); !strings.Contains(got, "no backups found") {
		t.Errorf("runList() output = %q, want it to report no backups", got)
	}
}

func TestRunList_Table(t *testing.T) {
	configPath, storageRoot := writeConfig(t)
	t.Setenv("ARGUS_DATABASE_URL", testDSN)

	seedManifest(t, storageRoot, "older", time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC), 1024)
	seedManifest(t, storageRoot, "newer", time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC), 5*1024*1024)

	var out bytes.Buffer
	if err := runList(context.Background(), configPath, false, &out); err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{"BACKUP ID", "older", "newer", "1.0 KiB", "5.0 MiB", "pending", "2026-09-05 03:00:00"} {
		if !strings.Contains(got, want) {
			t.Errorf("runList() output missing %q:\n%s", want, got)
		}
	}

	// Newest first, so the latest backup is the first row an operator reads.
	if strings.Index(got, "newer") > strings.Index(got, "older") {
		t.Errorf("runList() listed the older backup first:\n%s", got)
	}
}

func TestRunList_JSON(t *testing.T) {
	configPath, storageRoot := writeConfig(t)
	t.Setenv("ARGUS_DATABASE_URL", testDSN)

	seedManifest(t, storageRoot, "older", time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC), 1024)
	seedManifest(t, storageRoot, "newer", time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC), 2048)

	var out bytes.Buffer
	if err := runList(context.Background(), configPath, true, &out); err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	var got []manifest.Manifest
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}

	if len(got) != 2 {
		t.Fatalf("decoded %d manifests, want 2", len(got))
	}
	if got[0].BackupID != "newer" || got[1].BackupID != "older" {
		t.Errorf("order = [%s %s], want [newer older]", got[0].BackupID, got[1].BackupID)
	}
	if got[0].Artifact.SizeBytes != 2048 {
		t.Errorf("SizeBytes = %d, want 2048", got[0].Artifact.SizeBytes)
	}
}

// An empty result must be an empty array, not null, so that a script piping
// this into jq can iterate it without a special case.
func TestRunList_JSONEmptyIsArray(t *testing.T) {
	configPath, _ := writeConfig(t)
	t.Setenv("ARGUS_DATABASE_URL", testDSN)

	var out bytes.Buffer
	if err := runList(context.Background(), configPath, true, &out); err != nil {
		t.Fatalf("runList() error = %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("runList() output = %q, want %q", got, "[]")
	}
}

func TestRunList_MissingConfig(t *testing.T) {
	t.Setenv("ARGUS_DATABASE_URL", testDSN)

	err := runList(context.Background(), filepath.Join(t.TempDir(), "absent.yaml"), false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runList() error = nil, want error")
	}
	if !errors.Is(err, errConfig) {
		t.Errorf("runList() error = %v, want it to wrap errConfig", err)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{bytes: 0, want: "0 B"},
		{bytes: 512, want: "512 B"},
		{bytes: 1023, want: "1023 B"},
		{bytes: 1024, want: "1.0 KiB"},
		{bytes: 3224, want: "3.1 KiB"},
		{bytes: 1024 * 1024, want: "1.0 MiB"},
		{bytes: 5 * 1024 * 1024 * 1024, want: "5.0 GiB"},
		{bytes: 418234112, want: "398.9 MiB"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := humanSize(tc.bytes); got != tc.want {
				t.Errorf("humanSize(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		})
	}
}
