package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ServerVersion returns the version of the server behind dsn, such as
// "17.11".
func ServerVersion(ctx context.Context, dsn string) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", fmt.Errorf("opening connection: %w", err)
	}
	defer db.Close()

	var raw string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&raw); err != nil {
		return "", fmt.Errorf("querying server_version: %w", err)
	}

	version, err := parseServerVersion(raw)
	if err != nil {
		return "", fmt.Errorf("querying server_version: %w", err)
	}

	return version, nil
}

// parseServerVersion strips the packaging suffix that distribution builds
// report, turning "17.11 (Debian 17.11-1.pgdg13+2)" into "17.11". Verify
// starts an ephemeral container matching the version in the manifest, so
// that field has to hold the version and nothing else.
func parseServerVersion(raw string) (string, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", fmt.Errorf("unexpected server_version %q", raw)
	}

	return fields[0], nil
}
