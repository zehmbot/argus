//go:build integration

package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage/local"
)

// The image must match the major version of the pg_dump on PATH: pg_dump
// refuses to dump a server newer than itself.
const postgresImage = "postgres:17"

// startPostgres brings up a PostgreSQL seeded from the fixture and returns
// its connection string.
func startPostgres(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	seed, err := filepath.Abs(filepath.Join("..", "fixtures", "seed.sql"))
	if err != nil {
		t.Fatalf("resolving fixture: %v", err)
	}

	container, err := postgres.Run(ctx, postgresImage,
		postgres.WithDatabase("app_production"),
		postgres.WithUsername("argus"),
		postgres.WithPassword("argus"),
		postgres.WithInitScripts(seed),
		// The entrypoint starts the server once to run the init scripts and
		// then restarts it, so the ready line appears twice. Waiting for the
		// first would race the restart.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}

	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminating postgres: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	return dsn
}

func TestBackupAndList(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	configPath, storageRoot := writeConfig(t)
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	out, code := runArgus(t, env, "backup", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus backup exited %d:\n%s", code, out)
	}

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	manifests, err := manifest.List(ctx, backend, "app_production")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("found %d manifests, want 1", len(manifests))
	}

	m := manifests[0]

	if m.Source.Database != "app_production" {
		t.Errorf("Source.Database = %q, want %q", m.Source.Database, "app_production")
	}
	if !strings.HasPrefix(m.Source.PGVersion, "17.") {
		t.Errorf("Source.PGVersion = %q, want a 17.x version", m.Source.PGVersion)
	}
	if m.Source.PGDumpVersion == "" {
		t.Error("Source.PGDumpVersion is empty")
	}
	if !strings.HasPrefix(m.SchemaFingerprint, "sha256:") {
		t.Errorf("SchemaFingerprint = %q, want a sha256: prefix", m.SchemaFingerprint)
	}
	if m.Verification.Status != manifest.StatusPending {
		t.Errorf("Verification.Status = %q, want %q", m.Verification.Status, manifest.StatusPending)
	}

	// The fixture's row counts, recorded straight from the source database.
	wantCounts := map[string]int64{"users": 25, "orders": 100}
	for table, want := range wantCounts {
		if got := m.TableCounts[table]; got != want {
			t.Errorf("TableCounts[%q] = %d, want %d", table, got, want)
		}
	}
	if len(m.TableCounts) != len(wantCounts) {
		t.Errorf("TableCounts = %v, want exactly %v", m.TableCounts, wantCounts)
	}

	// The stored artifact must be exactly what the manifest claims, and must
	// decompress to a real custom-format dump rather than merely to bytes.
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

	dump := gunzip(t, stored)
	if !strings.HasPrefix(string(dump), "PGDMP") {
		t.Error("decompressed artifact is not a custom-format dump")
	}

	// list must report the backup that was just written.
	out, code = runArgus(t, env, "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, m.BackupID) {
		t.Errorf("argus list did not mention %s:\n%s", m.BackupID, out)
	}

	out, code = runArgus(t, env, "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("argus list --json exited %d:\n%s", code, out)
	}

	var decoded []manifest.Manifest
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("argus list --json produced invalid JSON: %v\n%s", err, out)
	}
	if len(decoded) != 1 || decoded[0].BackupID != m.BackupID {
		t.Errorf("argus list --json = %+v, want the one backup %s", decoded, m.BackupID)
	}
}

// A backup that cannot even reach the database must leave nothing behind
// that a later command could mistake for a usable backup.
func TestBackupFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	configPath, storageRoot := writeConfig(t)

	unreachable := strings.Replace(dsn, "/app_production?", "/no_such_database?", 1)
	if unreachable == dsn {
		t.Fatalf("could not derive an unreachable dsn from %q", dsn)
	}

	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + unreachable}, "backup", "--config", configPath)
	if code != 2 {
		t.Fatalf("argus backup exited %d, want 2:\n%s", code, out)
	}

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	keys, err := backend.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("storage holds %v after a failed backup, want nothing", keys)
	}

	out, code = runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "no backups found") {
		t.Errorf("argus list = %q, want it to report no backups", out)
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("artifact is not gzip: %v", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompressing artifact: %v", err)
	}

	return out
}
