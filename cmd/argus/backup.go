package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/pipeline"
	"github.com/zehmbot/argus/internal/postgres"
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

	key := manifest.ArtifactKey(conn.Database, backupID, recipient != nil)

	// pg_dump, gzip, age and the upload all run at once, joined by a pipe.
	// Nothing is staged on disk, so backing up a 40 GB database needs no
	// free space on the host, and the checksum is computed on the way past
	// rather than by reading the artifact back.
	result, err := pipeline.Stream(recipient,
		func(w io.Writer) error {
			return postgres.Dump(ctx, dsn, w)
		},
		func(r io.Reader) error {
			if err := backend.Put(ctx, key, r); err != nil {
				return fmt.Errorf("%w: uploading artifact: %w", errStorage, err)
			}

			return nil
		},
	)
	if err != nil {
		return err
	}

	artifact := manifest.Artifact{
		ObjectKey:   key,
		SizeBytes:   result.Size,
		SHA256:      result.SHA256,
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

	fmt.Printf("wrote backup %s (%d bytes) to %s\n", backupID, result.Size, key)

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
