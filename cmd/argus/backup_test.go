package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage/local"
)

func TestRunBackup_MissingConfig(t *testing.T) {
	err := runBackup(context.Background(), filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("runBackup() error = nil, want error")
	}
	if !errors.Is(err, errConfig) {
		t.Errorf("runBackup() error = %v, want it to wrap errConfig", err)
	}
}

// writeConfig writes a config file pointing storage at its own directory,
// and returns the config path and the storage root.
func writeConfig(t *testing.T) (configPath, storageRoot string) {
	t.Helper()

	dir := t.TempDir()
	storageRoot = filepath.Join(dir, "backups")
	configPath = filepath.Join(dir, "argus.yaml")

	// ToSlash because a Windows path's backslashes would be escape
	// sequences to the YAML parser.
	body := fmt.Sprintf("storage:\n  local:\n    path: %s\n", filepath.ToSlash(storageRoot))
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return configPath, storageRoot
}

// TestRunBackup_EndToEnd runs the whole command against a real PostgreSQL.
// It is skipped unless ARGUS_TEST_DATABASE_URL points at one, so that the
// default `go test ./...` stays runnable with nothing installed.
//
// In Git Bash, from the project root:
//
//	export ARGUS_TEST_DATABASE_URL="postgres://argus:argus@localhost:5432/argus?sslmode=disable"
//	go test ./cmd/argus/ -run TestRunBackup_EndToEnd -v
func TestRunBackup_EndToEnd(t *testing.T) {
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ARGUS_TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	configPath, storageRoot := writeConfig(t)
	t.Setenv("ARGUS_DATABASE_URL", dsn)

	if err := runBackup(ctx, configPath); err != nil {
		t.Fatalf("runBackup() error = %v", err)
	}

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	manifests, err := manifest.List(ctx, backend, "argus")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifest.List() returned %d manifests, want 1", len(manifests))
	}

	m := manifests[0]

	if m.Source.Database != "argus" {
		t.Errorf("Source.Database = %q, want %q", m.Source.Database, "argus")
	}
	if m.Source.PGVersion == "" || m.Source.PGDumpVersion == "" {
		t.Errorf("Source = %+v, want both versions recorded", m.Source)
	}
	if m.Artifact.Compression != "gzip" {
		t.Errorf("Artifact.Compression = %q, want %q", m.Artifact.Compression, "gzip")
	}
	if m.SchemaFingerprint == "" {
		t.Error("SchemaFingerprint is empty")
	}
	if m.Verification.Status != manifest.StatusPending {
		t.Errorf("Verification.Status = %q, want %q", m.Verification.Status, manifest.StatusPending)
	}
	if m.ToolVersion != version {
		t.Errorf("ToolVersion = %q, want %q", m.ToolVersion, version)
	}

	// The artifact the manifest points at must exist, and must be exactly
	// what the manifest says it is: right size, right checksum, real gzip.
	r, err := backend.Get(ctx, m.Artifact.ObjectKey)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", m.Artifact.ObjectKey, err)
	}
	defer r.Close()

	hash := sha256.New()
	stored, err := io.ReadAll(io.TeeReader(r, hash))
	if err != nil {
		t.Fatalf("reading artifact: %v", err)
	}

	if int64(len(stored)) != m.Artifact.SizeBytes {
		t.Errorf("artifact is %d bytes, manifest says %d", len(stored), m.Artifact.SizeBytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != m.Artifact.SHA256 {
		t.Errorf("artifact sha256 = %s, manifest says %s", got, m.Artifact.SHA256)
	}
	if len(stored) < 2 || stored[0] != 0x1f || stored[1] != 0x8b {
		t.Error("artifact does not start with the gzip magic bytes")
	}
}
