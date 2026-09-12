package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/verify"
)

// runVerify restores a backup into a throwaway database, checks it against
// what the manifest recorded, and writes the outcome back into the manifest.
func runVerify(ctx context.Context, configPath, backupID string, latest bool, out io.Writer) error {
	switch {
	case backupID == "" && !latest:
		return fmt.Errorf("%w: a backup id or --latest is required", errConfig)
	case backupID != "" && latest:
		return fmt.Errorf("%w: give a backup id or --latest, not both", errConfig)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

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

	m, err := selectManifest(ctx, backend, source.Database, backupID, latest)
	if err != nil {
		return err
	}

	identity, err := identityFor(m)
	if err != nil {
		return err
	}

	workDir, err := os.MkdirTemp("", "argus-verify-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	artifactPath := filepath.Join(workDir, path.Base(m.Artifact.ObjectKey))
	if err := download(ctx, backend, m, artifactPath); err != nil {
		return err
	}

	result, err := verify.Run(ctx, m,
		func(dsn string) error {
			// --no-owner: the throwaway database has none of the roles the
			// dump names, and with --exit-on-error that would fail the
			// restore for a reason that says nothing about the backup.
			return loadInto(ctx, artifactPath, dsn, identity, postgres.RestoreOptions{NoOwner: true})
		},
		verify.Options{
			RowCountTolerance: cfg.RowCountTolerance(),
			SmokeQuery:        cfg.SmokeQuery(),
		},
	)
	if err != nil {
		return err
	}

	passed := verify.Passed(result.Checks)

	verifiedAt := time.Now().UTC()
	m.Verification = manifest.Verification{
		Status:            manifest.StatusFailed,
		VerifiedAt:        &verifiedAt,
		RestoredPGVersion: result.RestoredPGVersion,
		Checks:            result.Checks,
	}
	if passed {
		m.Verification.Status = manifest.StatusVerified
	}

	// Record the outcome before reporting it, and record a failure just as
	// carefully as a success: a backup known to be bad is worth more than one
	// nobody has an opinion about, and prune's safety rule reads this field.
	if err := manifest.Write(ctx, backend, m); err != nil {
		return fmt.Errorf("%w: %w", errStorage, err)
	}

	slog.Info("verification complete",
		"backup_id", m.BackupID,
		"status", m.Verification.Status,
		"restored_pg_version", result.RestoredPGVersion,
		"checks", len(result.Checks),
	)

	if err := printVerification(out, m); err != nil {
		return err
	}

	if !passed {
		return fmt.Errorf("%w: backup %s", verify.ErrFailed, m.BackupID)
	}

	return nil
}

// selectManifest resolves either an explicit backup id or the most recent
// backup.
func selectManifest(ctx context.Context, backend storage.Backend, database, backupID string, latest bool) (manifest.Manifest, error) {
	if !latest {
		m, err := manifest.Read(ctx, backend, database, backupID)
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("%w: %w", errStorage, err)
		}

		return m, nil
	}

	manifests, err := manifest.List(ctx, backend, database)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("%w: %w", errStorage, err)
	}
	if len(manifests) == 0 {
		return manifest.Manifest{}, fmt.Errorf("%w: no backups found for %s", errConfig, database)
	}

	// List returns newest first.
	return manifests[0], nil
}

func printVerification(out io.Writer, m manifest.Manifest) error {
	fmt.Fprintf(out, "backup %s: %s (restored on PostgreSQL %s)\n\n",
		m.BackupID, m.Verification.Status, m.Verification.RestoredPGVersion)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "CHECK\tRESULT\tDETAIL")
	for _, check := range m.Verification.Checks {
		outcome := "pass"
		if !check.Passed {
			outcome = "FAIL"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", check.Name, outcome, check.Detail)
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing results: %w", err)
	}

	return nil
}
