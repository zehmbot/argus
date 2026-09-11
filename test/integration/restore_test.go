//go:build integration

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/url"
	"strings"
	"testing"

	"filippo.io/age"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/local"
)

// createDatabase makes an empty database in the same cluster and returns a
// DSN pointing at it. Restoring into the same cluster keeps the roles the
// dump refers to in existence, which is what a real restore would have.
func createDatabase(t *testing.T, adminDSN, name string) string {
	t.Helper()

	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating database %s: %v", name, err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parsing dsn: %v", err)
	}
	u.Path = "/" + name

	return u.String()
}

// backupOnce runs a backup and returns its manifest and the storage it landed
// in. recipient may be empty for no encryption.
func backupOnce(t *testing.T, dsn, recipient string) (manifest.Manifest, storage.Backend, string) {
	t.Helper()

	configPath, storageRoot := writeConfig(t, recipient)

	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "backup", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus backup exited %d:\n%s", code, out)
	}

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	manifests, err := manifest.List(context.Background(), backend, "app_production")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("found %d manifests, want 1", len(manifests))
	}

	return manifests[0], backend, configPath
}

// assertMatchesManifest checks a restored database against what the manifest
// recorded of the source. This is the project's whole premise: a backup you
// have not restored and compared is a backup you are only assuming works.
func assertMatchesManifest(t *testing.T, targetDSN string, m manifest.Manifest) {
	t.Helper()

	ctx := context.Background()

	counts, err := postgres.TableCounts(ctx, targetDSN)
	if err != nil {
		t.Fatalf("TableCounts() error = %v", err)
	}

	if len(counts) != len(m.TableCounts) {
		t.Fatalf("restored tables = %v, want %v", counts, m.TableCounts)
	}
	for table, want := range m.TableCounts {
		if counts[table] != want {
			t.Errorf("restored %q has %d rows, manifest recorded %d", table, counts[table], want)
		}
	}

	fingerprint, err := postgres.SchemaFingerprint(ctx, targetDSN)
	if err != nil {
		t.Fatalf("SchemaFingerprint() error = %v", err)
	}
	if fingerprint != m.SchemaFingerprint {
		t.Errorf("restored fingerprint = %s, manifest recorded %s", fingerprint, m.SchemaFingerprint)
	}
}

func TestRestore(t *testing.T) {
	dsn := startPostgres(t)
	m, _, configPath := backupOnce(t, dsn, "")

	targetDSN := createDatabase(t, dsn, "restored_plain")

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn},
		"restore", m.BackupID, "--config", configPath, "--target", targetDSN,
	)
	if code != 0 {
		t.Fatalf("argus restore exited %d:\n%s", code, out)
	}

	assertMatchesManifest(t, targetDSN, m)
}

func TestRestore_Encrypted(t *testing.T) {
	dsn := startPostgres(t)

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	m, _, configPath := backupOnce(t, dsn, identity.Recipient().String())
	targetDSN := createDatabase(t, dsn, "restored_encrypted")

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn, "ARGUS_AGE_IDENTITY=" + identity.String()},
		"restore", m.BackupID, "--config", configPath, "--target", targetDSN,
	)
	if code != 0 {
		t.Fatalf("argus restore exited %d:\n%s", code, out)
	}

	assertMatchesManifest(t, targetDSN, m)
}

// The wrong key must be reported as such, naming both public keys, rather
// than surfacing as age saying no identity matched.
func TestRestore_WrongIdentity(t *testing.T) {
	dsn := startPostgres(t)

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	m, _, configPath := backupOnce(t, dsn, identity.Recipient().String())
	targetDSN := createDatabase(t, dsn, "restored_wrong_key")

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn, "ARGUS_AGE_IDENTITY=" + other.String()},
		"restore", m.BackupID, "--config", configPath, "--target", targetDSN,
	)
	if code != 1 {
		t.Fatalf("argus restore exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, identity.Recipient().String()) {
		t.Errorf("error does not name the key the artifact needs:\n%s", out)
	}
	if strings.Contains(out, other.String()) {
		t.Errorf("error leaks the private key it was given:\n%s", out)
	}

	assertEmptyDatabase(t, targetDSN)
}

// Fault injection: one flipped byte must be caught by the checksum, and the
// target must be left untouched. A restore that fails half way through is
// worse than one that refuses to start, because it looks like it worked.
func TestRestore_CorruptArtifact(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)

	m, backend, configPath := backupOnce(t, dsn, "")
	targetDSN := createDatabase(t, dsn, "restored_corrupt")

	r, err := backend.Get(ctx, m.Artifact.ObjectKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	stored, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("reading artifact: %v", err)
	}

	// Flip a bit in the middle, past the gzip header.
	corrupted := bytes.Clone(stored)
	corrupted[len(corrupted)/2] ^= 0x01

	if err := backend.Put(ctx, m.Artifact.ObjectKey, bytes.NewReader(corrupted)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn},
		"restore", m.BackupID, "--config", configPath, "--target", targetDSN,
	)
	if code == 0 {
		t.Fatalf("argus restore succeeded on a corrupted artifact:\n%s", out)
	}
	if !strings.Contains(out, "checksum") {
		t.Errorf("failure does not mention the checksum:\n%s", out)
	}

	assertEmptyDatabase(t, targetDSN)
}

func assertEmptyDatabase(t *testing.T, dsn string) {
	t.Helper()

	counts, err := postgres.TableCounts(context.Background(), dsn)
	if err != nil {
		t.Fatalf("TableCounts() error = %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("target holds %v, want an untouched database", counts)
	}
}
