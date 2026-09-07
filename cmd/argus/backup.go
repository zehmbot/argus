package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/pipeline"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/local"
)

// runBackup dumps the configured database, compresses it, uploads it, and
// records a manifest describing the result.
func runBackup(ctx context.Context, configPath string) error {
	start := time.Now().UTC()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	// The DSN comes from the environment, never from the config file, so a
	// database password cannot end up committed to version control.
	dsn, err := config.SourceDSN()
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	conn, err := postgres.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	backend, err := local.New(cfg.Storage.Local.Path)
	if err != nil {
		return fmt.Errorf("%w: opening storage: %w", errStorage, err)
	}

	backupID, err := manifest.NewID(start)
	if err != nil {
		return err
	}

	// Read the source metadata before dumping, so the recorded schema and
	// row counts describe the database as the dump began. The source can
	// still change while pg_dump runs, which is why verification compares
	// row counts within a tolerance rather than exactly.
	source, err := describeSource(ctx, dsn, conn)
	if err != nil {
		return err
	}

	fingerprint, err := postgres.SchemaFingerprint(ctx, dsn)
	if err != nil {
		return fmt.Errorf("fingerprinting schema: %w", err)
	}

	counts, err := postgres.TableCounts(ctx, dsn)
	if err != nil {
		return fmt.Errorf("counting rows: %w", err)
	}

	// Phase 1 stages the dump on local disk, which keeps the upload a simple
	// retryable copy. Phase 3 replaces this with a streaming pipeline so that
	// a 40 GB database does not require 40 GB of free space on the host.
	workDir, err := os.MkdirTemp("", "argus-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	dumpPath := filepath.Join(workDir, backupID+".dump")
	if err := postgres.Dump(ctx, dsn, dumpPath); err != nil {
		return err
	}

	artifactPath := dumpPath + ".gz"
	sum, size, err := compressToFile(dumpPath, artifactPath)
	if err != nil {
		return err
	}

	key := manifest.ArtifactKey(conn.Database, backupID)
	if err := upload(ctx, backend, key, artifactPath); err != nil {
		return err
	}

	m := manifest.Manifest{
		BackupID:        backupID,
		CreatedAt:       start,
		DurationSeconds: int(time.Since(start).Seconds()),
		Source:          source,
		Artifact: manifest.Artifact{
			ObjectKey:   key,
			SizeBytes:   size,
			SHA256:      sum,
			Compression: "gzip",
		},
		SchemaFingerprint: fingerprint,
		TableCounts:       counts,
		// Nothing has been restored yet; verify fills this in later.
		Verification: manifest.Verification{Status: manifest.StatusPending},
		ToolVersion:  version,
	}

	// The manifest write is the commit point of the whole operation: until it
	// lands, list and prune cannot see the artifact, so any failure above
	// leaves nothing in the bucket that looks like a usable backup.
	if err := manifest.Write(ctx, backend, m); err != nil {
		return fmt.Errorf("%w: %w", errStorage, err)
	}

	fmt.Printf("wrote backup %s (%d bytes) to %s\n", backupID, size, key)

	return nil
}

func describeSource(ctx context.Context, dsn string, conn postgres.ConnInfo) (manifest.Source, error) {
	pgVersion, err := postgres.ServerVersion(ctx, dsn)
	if err != nil {
		return manifest.Source{}, fmt.Errorf("reading server version: %w", err)
	}

	dumpVersion, err := postgres.DumpVersion(ctx)
	if err != nil {
		return manifest.Source{}, err
	}

	return manifest.Source{
		Host:          conn.Host,
		Database:      conn.Database,
		PGVersion:     pgVersion,
		PGDumpVersion: dumpVersion,
	}, nil
}

// compressToFile gzips srcPath into dstPath, returning the checksum of the
// compressed bytes and their size.
func compressToFile(srcPath, dstPath string) (sha256Hex string, size int64, err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, fmt.Errorf("opening dump: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("creating compressed artifact: %w", err)
	}
	defer dst.Close()

	sum, err := pipeline.Compress(dst, src)
	if err != nil {
		return "", 0, err
	}

	// Close before measuring, so the gzip trailer is on disk and the size
	// recorded in the manifest is the size the artifact will actually have.
	if err := dst.Close(); err != nil {
		return "", 0, fmt.Errorf("closing compressed artifact: %w", err)
	}

	info, err := os.Stat(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("sizing compressed artifact: %w", err)
	}

	return sum, info.Size(), nil
}

func upload(ctx context.Context, backend storage.Backend, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening artifact: %w", err)
	}
	defer f.Close()

	if err := backend.Put(ctx, key, f); err != nil {
		return fmt.Errorf("%w: uploading artifact: %w", errStorage, err)
	}

	return nil
}
