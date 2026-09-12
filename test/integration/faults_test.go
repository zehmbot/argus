//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/zehmbot/argus/internal/storage/local"
	"github.com/zehmbot/argus/internal/storage/s3"
)

// seedIncompressible adds a table whose contents gzip barely shrinks, so the
// dump takes long enough to be interrupted part way through. A table of
// repeated characters would compress to almost nothing and finish instantly.
func seedIncompressible(t *testing.T, dsn string, rows int) {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "CREATE TABLE bulk (id serial PRIMARY KEY, payload text NOT NULL)"); err != nil {
		t.Fatalf("creating bulk table: %v", err)
	}

	const insert = `
		INSERT INTO bulk (payload)
		SELECT string_agg(md5(random()::text), '')
		FROM generate_series(1, 10), generate_series(1, $1) g
		GROUP BY g`

	if _, err := db.ExecContext(ctx, insert, rows); err != nil {
		t.Fatalf("seeding bulk table: %v", err)
	}
}

// waitForStagingFile blocks until the local backend has begun writing an
// object, which means the upload is genuinely in progress. Polling for that
// condition beats sleeping for a guessed interval, which would either kill
// the process too early or not at all.
func waitForStagingFile(t *testing.T, storageRoot string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(storageRoot, ".tmp", "upload-*"))
		if err != nil {
			t.Fatalf("globbing staging directory: %v", err)
		}
		if len(matches) > 0 {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("upload never started")
}

// TestBackupKilledMidStream is the fault this whole design exists to survive.
// A backup host can lose the process at any moment, and what must never
// happen is that the remains look like a usable backup.
func TestBackupKilledMidStream(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	seedIncompressible(t, dsn, 40000)

	configPath, storageRoot := writeConfig(t, "")

	cmd := exec.Command(argusBin, "backup", "--config", configPath)
	cmd.Env = append(os.Environ(), "ARGUS_DATABASE_URL="+dsn)

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting argus: %v", err)
	}

	waitForStagingFile(t, storageRoot)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing argus: %v", err)
	}

	// A killed process cannot report an exit status cleanly; the point is
	// only that it did not finish its work.
	_ = cmd.Wait()

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	// Nothing may be visible at a real key. The staging file the process was
	// part way through writing is not one: it is only promoted on success.
	keys, err := backend.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("storage holds %v after a killed backup, want nothing", keys)
	}

	// And the command that an operator would actually run must agree.
	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "no backups found") {
		t.Errorf("argus list = %q, want it to report no backups", out)
	}
}

var errUploadSource = errors.New("upload source failed")

// failingReader yields n bytes and then fails, standing in for a dump that
// dies part way through a large upload.
type failingReader struct {
	remaining int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, errUploadSource
	}

	if len(p) > f.remaining {
		p = p[:f.remaining]
	}
	for i := range p {
		p[i] = 'x'
	}
	f.remaining -= len(p)

	return len(p), nil
}

// TestS3_AbortsMultipartUploadOnFailure covers the failure that costs money
// rather than data: parts already uploaded when the source fails must be
// abandoned by the client, not left behind to accrue storage charges.
func TestS3_AbortsMultipartUploadOnFailure(t *testing.T) {
	ctx := context.Background()
	cfg, creds := startMinIO(t)

	backend, err := s3.New(ctx, cfg, creds)
	if err != nil {
		t.Fatalf("s3.New() error = %v", err)
	}

	const key = "backups/app_production/interrupted.dump.gz"

	// Larger than one part, so a multipart upload is genuinely in progress
	// by the time the source fails.
	err = backend.Put(ctx, key, &failingReader{remaining: 20 << 20})
	if err == nil {
		t.Fatal("Put() error = nil, want the source failure to propagate")
	}

	if _, statErr := backend.Stat(ctx, key); statErr == nil {
		t.Error("a failed upload left an object at the key")
	}

	admin, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKeyID, creds.SecretAccessKey, ""),
	})
	if err != nil {
		t.Fatalf("minio.New() error = %v", err)
	}

	var orphaned []string
	for upload := range admin.ListIncompleteUploads(ctx, cfg.Bucket, "", true) {
		if upload.Err != nil {
			t.Fatalf("listing incomplete uploads: %v", upload.Err)
		}
		orphaned = append(orphaned, fmt.Sprintf("%s (%d bytes)", upload.Key, upload.Size))
	}

	if len(orphaned) != 0 {
		t.Errorf("multipart upload was not aborted, parts left behind: %v", orphaned)
	}
}
