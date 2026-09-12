package verify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/postgres"
)

// Options tune what counts as a successful verification.
type Options struct {
	RowCountTolerance float64
	// SmokeQuery is optional. When set it must succeed and return at least
	// one row.
	SmokeQuery string
}

// Result is the outcome of verifying one backup.
type Result struct {
	Checks []manifest.Check
	// RestoredPGVersion is what the throwaway server actually reported,
	// rather than what was asked for.
	RestoredPGVersion string
}

// Run restores a backup into a throwaway database and checks what came back
// against what the manifest recorded.
//
// The caller supplies restore, because getting the artifact out of storage,
// checked, decrypted and decompressed is the command's business; this
// package's business is the database and the checks.
func Run(ctx context.Context, m manifest.Manifest, restore func(dsn string) error, opts Options) (Result, error) {
	container, err := StartPostgres(ctx, m.Source.PGVersion)
	if err != nil {
		return Result{}, err
	}

	// WithoutCancel so the container is still removed when verification
	// failed because the context was cancelled.
	defer container.Remove(context.WithoutCancel(ctx))

	result := Result{RestoredPGVersion: container.Version}

	restoreErr := restore(container.DSN)
	result.Checks = append(result.Checks, restoreCheck(restoreErr))

	// Stop here when the restore failed. Comparing a database that was never
	// populated would add a page of noise about missing tables, which says
	// nothing beyond what the first failure already said.
	if restoreErr != nil {
		return result, nil
	}

	fingerprint, err := postgres.SchemaFingerprint(ctx, container.DSN)
	if err != nil {
		return Result{}, fmt.Errorf("fingerprinting the restored schema: %w", err)
	}
	result.Checks = append(result.Checks, fingerprintCheck(m.SchemaFingerprint, fingerprint))

	counts, err := postgres.TableCounts(ctx, container.DSN)
	if err != nil {
		return Result{}, fmt.Errorf("counting rows in the restored database: %w", err)
	}
	result.Checks = append(result.Checks, rowCountCheck(m.TableCounts, counts, opts.RowCountTolerance))

	if opts.SmokeQuery != "" {
		result.Checks = append(result.Checks, smokeCheck(ctx, container.DSN, opts.SmokeQuery))
	}

	return result, nil
}

// smokeCheck runs the operator's own query against the restored database.
//
// It exists because argus cannot know what makes a particular database
// usable. Row counts and a schema fingerprint prove the shape came back;
// only the people who own the data can say whether it means anything.
//
// The query must succeed and return at least one row.
func smokeCheck(ctx context.Context, dsn, query string) manifest.Check {
	check := manifest.Check{Name: CheckSmokeQuery}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		check.Detail = fmt.Sprintf("opening connection: %v", err)

		return check
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		check.Detail = fmt.Sprintf("query failed: %v", err)

		return check
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			check.Detail = fmt.Sprintf("query failed: %v", err)
		} else {
			check.Detail = "query returned no rows"
		}

		return check
	}

	if err := rows.Err(); err != nil {
		check.Detail = fmt.Sprintf("query failed: %v", err)

		return check
	}

	check.Passed = true

	return check
}

// ErrFailed reports that verification ran and the backup did not pass.
var ErrFailed = errors.New("verification failed")
