package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/pipeline"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/storage"
)

// runBackup dumps the configured database, compresses and optionally
// encrypts it, uploads it, and records a manifest describing the result.
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

	// nil when encryption is not configured. The recipient is a public key,
	// so backup never holds anything that could read the artifact back.
	recipient, err := cfg.Recipient()
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	backend, err := openBackend(ctx, cfg)
	if err != nil {
		return err
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

	key := manifest.ArtifactKey(conn.Database, backupID, recipient != nil)

	// Name the staged file after the object key, so the extensions on disk
	// and in the bucket describe the same thing.
	artifactPath := filepath.Join(workDir, path.Base(key))

	sum, size, err := buildArtifact(dumpPath, artifactPath, recipient)
	if err != nil {
		return err
	}

	if err := upload(ctx, backend, key, artifactPath); err != nil {
		return err
	}

	artifact := manifest.Artifact{
		ObjectKey:   key,
		SizeBytes:   size,
		SHA256:      sum,
		Compression: "gzip",
	}
	if recipient != nil {
		artifact.EncryptionRecipient = cfg.Encryption.Recipient
	}

	m := manifest.Manifest{
		BackupID:          backupID,
		CreatedAt:         start,
		DurationSeconds:   int(time.Since(start).Seconds()),
		Source:            source,
		Artifact:          artifact,
		SchemaFingerprint: fingerprint,
		TableCounts:       counts,
		// Nothing has been restored yet; verify fills this in later.
		Verification: manifest.Verification{Status: manifest.StatusPending},
		ToolVersion:  version,
	}

	// The manifest write is the commit point of the whole operation: until it
	// lands, list and prune cannot see the artifact, so any failure above it
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

// buildArtifact gzips the dump at srcPath into dstPath, encrypting it to
// recipient when one is configured, and returns the checksum and size of the
// bytes actually written.
//
// The checksum covers the finished artifact rather than the compressed but
// not yet encrypted bytes, so that a downloaded object can be checked for
// corruption by someone who does not hold the key to decrypt it.
func buildArtifact(srcPath, dstPath string, recipient age.Recipient) (sha256Hex string, size int64, err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, fmt.Errorf("opening dump: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("creating artifact: %w", err)
	}
	defer dst.Close()

	// Everything written to sink lands in the file and in the hash at once,
	// so the checksum never costs a second pass over the data.
	hash := sha256.New()
	sink := io.Writer(io.MultiWriter(dst, hash))

	var encrypted io.WriteCloser
	if recipient != nil {
		encrypted, err = pipeline.Encrypt(sink, recipient)
		if err != nil {
			return "", 0, err
		}
		sink = encrypted
	}

	if err := pipeline.Compress(sink, src); err != nil {
		return "", 0, err
	}

	// age writes its authentication tag on close, so this has to happen
	// before the file is measured, and before anything is uploaded.
	if encrypted != nil {
		if err := encrypted.Close(); err != nil {
			return "", 0, fmt.Errorf("closing encryption writer: %w", err)
		}
	}

	// Close before measuring, so the size recorded in the manifest is the
	// size the artifact will actually have.
	if err := dst.Close(); err != nil {
		return "", 0, fmt.Errorf("closing artifact: %w", err)
	}

	info, err := os.Stat(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("sizing artifact: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
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
