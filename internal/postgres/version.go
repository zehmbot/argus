package postgres

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ServerVersion returns the server_version setting (e.g. "16.3") of the
// database identified by dsn.
func ServerVersion(ctx context.Context, dsn string) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", fmt.Errorf("opening connection: %w", err)
	}
	defer db.Close()

	var version string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		return "", fmt.Errorf("querying server_version: %w", err)
	}

	return version, nil
}
