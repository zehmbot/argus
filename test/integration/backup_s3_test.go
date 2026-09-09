//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage/s3"
)

// writeS3Config writes a config pointing storage at a MinIO bucket.
func writeS3Config(t *testing.T, cfg s3.Config) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "argus.yaml")
	body := fmt.Sprintf(
		"storage:\n  s3:\n    endpoint: %s\n    bucket: %s\n    region: %s\n    insecure: true\n",
		cfg.Endpoint, cfg.Bucket, cfg.Region,
	)

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return path
}

// TestBackupToS3 runs the binary end to end with object storage rather than a
// local directory, which is the wiring the s3 package tests do not cover.
func TestBackupToS3(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	s3cfg, creds := startMinIO(t)
	configPath := writeS3Config(t, s3cfg)

	env := []string{
		"ARGUS_DATABASE_URL=" + dsn,
		"ARGUS_S3_ACCESS_KEY_ID=" + creds.AccessKeyID,
		"ARGUS_S3_SECRET_ACCESS_KEY=" + creds.SecretAccessKey,
	}

	out, code := runArgus(t, env, "backup", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus backup exited %d:\n%s", code, out)
	}

	backend, err := s3.New(ctx, s3cfg, creds)
	if err != nil {
		t.Fatalf("s3.New() error = %v", err)
	}

	manifests, err := manifest.List(ctx, backend, "app_production")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("found %d manifests in the bucket, want 1", len(manifests))
	}

	m := manifests[0]

	if m.TableCounts["users"] != 25 || m.TableCounts["orders"] != 100 {
		t.Errorf("TableCounts = %v, want users 25 and orders 100", m.TableCounts)
	}

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
	if dump := gunzip(t, stored); !strings.HasPrefix(string(dump), "PGDMP") {
		t.Error("artifact in the bucket is not a custom-format dump")
	}

	// list reads the same bucket back through the command.
	out, code = runArgus(t, env, "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, m.BackupID) {
		t.Errorf("argus list did not mention %s:\n%s", m.BackupID, out)
	}
}

// Without credentials, the command must fail as a configuration error before
// it does any work.
func TestBackupToS3_MissingCredentials(t *testing.T) {
	dsn := startPostgres(t)
	s3cfg, _ := startMinIO(t)
	configPath := writeS3Config(t, s3cfg)

	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "backup", "--config", configPath)
	if code != 1 {
		t.Fatalf("argus backup exited %d, want 1 (configuration error):\n%s", code, out)
	}
	if !strings.Contains(out, "ARGUS_S3_ACCESS_KEY_ID") {
		t.Errorf("error does not name the missing variable:\n%s", out)
	}
}
