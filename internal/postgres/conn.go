package postgres

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ConnInfo describes where a connection string points. The manifest records
// it so a restored database can be traced back to its source.
type ConnInfo struct {
	Host     string
	Database string
}

// ParseDSN extracts the host and database from a connection string. It
// accepts both the URL form (postgres://host/db) and the keyword/value form
// (host=... dbname=...), because libpq accepts both and an operator will
// paste whichever one they already have.
//
// It returns only the host and database, never the password: the result is
// written into a manifest that lands in object storage. pgconn redacts the
// password before putting the connection string into its parse errors, and
// what survives that redaction is host, user, and database name, which this
// tool records in the clear anyway.
func ParseDSN(dsn string) (ConnInfo, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return ConnInfo{}, fmt.Errorf("parsing connection string: %w", err)
	}

	return ConnInfo{Host: cfg.Host, Database: cfg.Database}, nil
}
