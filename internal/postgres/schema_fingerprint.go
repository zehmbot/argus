package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

type columnInfo struct {
	Table  string
	Column string
	Type   string
}

// SchemaFingerprint returns a stable hash of the public schema's base
// tables and columns (name and data type), used to detect schema drift
// between a backup and its restore.
func SchemaFingerprint(ctx context.Context, dsn string) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", fmt.Errorf("opening connection: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON c.table_schema = t.table_schema AND c.table_name = t.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.column_name
	`)
	if err != nil {
		return "", fmt.Errorf("listing columns: %w", err)
	}
	defer rows.Close()

	var columns []columnInfo
	for rows.Next() {
		var c columnInfo
		if err := rows.Scan(&c.Table, &c.Column, &c.Type); err != nil {
			return "", fmt.Errorf("scanning column: %w", err)
		}
		columns = append(columns, c)
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("listing columns: %w", err)
	}

	return fingerprintColumns(columns), nil
}

// fingerprintColumns hashes columns into a stable "sha256:<hex>" fingerprint.
// columns must already be in a deterministic order.
func fingerprintColumns(columns []columnInfo) string {
	var sb strings.Builder
	for _, c := range columns {
		fmt.Fprintf(&sb, "%s.%s:%s\n", c.Table, c.Column, c.Type)
	}

	sum := sha256.Sum256([]byte(sb.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}
