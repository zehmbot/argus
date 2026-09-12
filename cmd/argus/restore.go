package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"filippo.io/age"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/pipeline"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/storage"
)

// runRestore loads the backup named by backupID into the database at
// targetDSN.
func runRestore(ctx context.Context, configPath, backupID, targetDSN string) error {
	if backupID == "" {
		return fmt.Errorf("%w: a backup id is required", errConfig)
	}
	if targetDSN == "" {
		return fmt.Errorf("%w: --target is required", errConfig)
	}

	target, err := postgres.ParseDSN(targetDSN)
	if err != nil {
		return fmt.Errorf("%w: parsing --target: %w", errConfig, err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	// The source DSN identifies which database's backups to look in. It is
	// not connected to: restore reads from storage and writes to --target.
	sourceDSN, err := config.SourceDSN()
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	source, err := postgres.ParseDSN(sourceDSN)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	backend, err := openBackend(ctx, cfg)
	if err != nil {
		return err
	}

	m, err := manifest.Read(ctx, backend, source.Database, backupID)
	if err != nil {
		return fmt.Errorf("%w: %w", errStorage, err)
	}

	// Resolve the key before downloading anything, so a missing or wrong
	// identity fails in a second rather than after a large download.
	identity, err := identityFor(m)
	if err != nil {
		return err
	}

	workDir, err := os.MkdirTemp("", "argus-restore-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	artifactPath := filepath.Join(workDir, path.Base(m.Artifact.ObjectKey))
	if err := download(ctx, backend, m, artifactPath); err != nil {
		return err
	}

	// A restore into a real cluster keeps the ownership the dump records;
	// only verification, whose throwaway database has none of those roles,
	// drops it.
	if err := loadInto(ctx, artifactPath, targetDSN, identity, postgres.RestoreOptions{}); err != nil {
		return err
	}

	slog.Info("restore complete",
		"backup_id", m.BackupID,
		"target_database", target.Database,
		"target_host", target.Host,
	)

	return nil
}

// identityFor returns the key needed to read m's artifact, or nil when
// it was not encrypted.
func identityFor(m manifest.Manifest) (*age.X25519Identity, error) {
	if m.Artifact.EncryptionRecipient == "" {
		return nil, nil
	}

	identity, err := config.AgeIdentity()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errConfig, err)
	}

	// Recording the recipient in the manifest earns its keep here: without
	// this check the failure would be age reporting that no identity matched,
	// which says nothing about which key is actually wanted. Both values are
	// public keys, so naming them gives nothing away.
	if got := identity.Recipient().String(); got != m.Artifact.EncryptionRecipient {
		return nil, fmt.Errorf(
			"%w: backup %s is encrypted to %s, but the configured identity is for %s",
			errConfig, m.BackupID, m.Artifact.EncryptionRecipient, got,
		)
	}

	return identity, nil
}

// download fetches the artifact to dstPath and checks it against the
// manifest.
//
// The check happens before anything touches the target database. A corrupted
// artifact must be refused while the target is still untouched, rather than
// discovered part way through writing it.
func download(ctx context.Context, backend storage.Backend, m manifest.Manifest, dstPath string) error {
	r, err := backend.Get(ctx, m.Artifact.ObjectKey)
	if err != nil {
		return fmt.Errorf("%w: downloading %s: %w", errStorage, m.Artifact.ObjectKey, err)
	}
	defer r.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dstPath, err)
	}
	defer dst.Close()

	hash := sha256.New()

	size, err := io.Copy(io.MultiWriter(dst, hash), r)
	if err != nil {
		return fmt.Errorf("%w: downloading %s: %w", errStorage, m.Artifact.ObjectKey, err)
	}

	if err := dst.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", dstPath, err)
	}

	if size != m.Artifact.SizeBytes {
		return fmt.Errorf("artifact %s is %d bytes, manifest says %d", m.Artifact.ObjectKey, size, m.Artifact.SizeBytes)
	}

	if sum := hex.EncodeToString(hash.Sum(nil)); sum != m.Artifact.SHA256 {
		return fmt.Errorf("artifact %s failed its checksum: computed %s, manifest says %s", m.Artifact.ObjectKey, sum, m.Artifact.SHA256)
	}

	return nil
}

// loadInto decrypts and decompresses the artifact straight into pg_restore,
// so the plaintext dump is never written to disk.
func loadInto(ctx context.Context, artifactPath, targetDSN string, identity *age.X25519Identity, opts postgres.RestoreOptions) error {
	f, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("opening artifact: %w", err)
	}
	defer f.Close()

	var src io.Reader = f

	if identity != nil {
		src, err = pipeline.Decrypt(src, identity)
		if err != nil {
			return err
		}
	}

	gz, err := pipeline.Decompress(src)
	if err != nil {
		return err
	}
	defer gz.Close()

	return postgres.Restore(ctx, targetDSN, gz, opts)
}
