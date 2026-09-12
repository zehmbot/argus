package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/retention"
	"github.com/zehmbot/argus/internal/storage"
)

// runPrune applies the retention policy. It reports what it would do and
// changes nothing unless apply is set.
func runPrune(ctx context.Context, configPath string, apply bool, out io.Writer) error {
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

	manifests, err := manifest.List(ctx, backend, source.Database)
	if err != nil {
		return fmt.Errorf("%w: %w", errStorage, err)
	}

	plan, err := retention.Apply(manifests, time.Now().UTC(), cfg.RetentionPolicy())
	if err != nil {
		// The refusal is the feature, so show the state that caused it
		// rather than only reporting that something went wrong.
		if printErr := printPlan(out, plan, false); printErr != nil {
			return printErr
		}

		return fmt.Errorf("%w: refusing to delete anything", err)
	}

	if err := printPlan(out, plan, apply); err != nil {
		return err
	}

	if !apply {
		slog.Info("prune dry run", "kept", len(plan.Keep), "would_delete", len(plan.Delete))

		if len(plan.Delete) > 0 {
			fmt.Fprintf(out, "\nnothing was deleted. Re-run with --apply to carry this out.\n")
		}

		return nil
	}

	if err := deleteBackups(ctx, backend, plan.Delete, out); err != nil {
		return err
	}

	slog.Info("prune complete", "kept", len(plan.Keep), "deleted", len(plan.Delete))

	return nil
}

// deleteBackups removes each backup, manifest first.
//
// The order is the mirror of how a backup is written. There, the manifest is
// written last, so that nothing is visible until the artifact is safely
// stored. Here it is deleted first, so that nothing is visible once the
// artifact is about to go. Interrupted the other way round, a manifest would
// be left pointing at an artifact that no longer exists: a backup that
// appears in list and fails only when someone needs it.
//
// Interrupted this way round, the artifact is orphaned. That costs storage
// and nothing else, which is the cheaper of the two failures.
func deleteBackups(ctx context.Context, backend storage.Backend, doomed []manifest.Manifest, out io.Writer) error {
	var failures []error

	for _, m := range doomed {
		manifestKey := manifest.ManifestKey(m.Source.Database, m.BackupID)

		if err := backend.Delete(ctx, manifestKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			failures = append(failures, fmt.Errorf("deleting %s: %w", manifestKey, err))

			// Leave the artifact alone: its manifest still points at it.
			continue
		}

		// Already gone is not a failure. Deleting is meant to be safe to
		// retry after an interrupted run.
		if err := backend.Delete(ctx, m.Artifact.ObjectKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			failures = append(failures, fmt.Errorf("deleting %s: %w", m.Artifact.ObjectKey, err))

			continue
		}

		fmt.Fprintf(out, "deleted %s\n", m.BackupID)
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w: %w", errStorage, errors.Join(failures...))
	}

	return nil
}

func printPlan(out io.Writer, plan retention.Plan, apply bool) error {
	verb := "would delete"
	if apply {
		verb = "deleting"
	}

	fmt.Fprintf(out, "keeping %d backups, %s %d\n\n", len(plan.Keep), verb, len(plan.Delete))

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "BACKUP ID\tCREATED (UTC)\tVERIFIED\tACTION")

	for _, m := range plan.Keep {
		writePlanRow(w, m, "keep")
	}
	for _, m := range plan.Delete {
		writePlanRow(w, m, "delete")
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing plan: %w", err)
	}

	return nil
}

func writePlanRow(w io.Writer, m manifest.Manifest, action string) {
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
		m.BackupID,
		m.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		m.Verification.Status,
		action,
	)
}
