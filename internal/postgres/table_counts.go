package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TableCounts returns the row count of every base table in the public
// schema of the database identified by dsn.
func TableCounts(ctx context.Context, dsn string) (map[string]int64, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening connection: %w", err)
	}
	defer db.Close()

	tables, err := listPublicTables(ctx, db)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		count, err := countRows(ctx, db, table)
		if err != nil {
			return nil, err
		}
		counts[table] = count
	}

	return counts, nil
}

func listPublicTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
	`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning table name: %w", err)
		}
		tables = append(tables, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}

	return tables, nil
}

func countRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", pgx.Identifier{table}.Sanitize())

	var count int64
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting rows in %s: %w", table, err)
	}

	return count, nil
}
