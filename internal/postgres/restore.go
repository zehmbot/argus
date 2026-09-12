package postgres

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// RestoreOptions adjust how an archive is loaded.
type RestoreOptions struct {
	// NoOwner skips the dump's ownership statements.
	//
	// A dump names the roles that owned each object. Restoring it into a
	// cluster where those roles do not exist fails, which is exactly the
	// case when verifying into a throwaway container. A restore into a real
	// cluster should preserve ownership, so this is off by default and
	// turned on only by verification.
	NoOwner bool
}

// RestoreArgs returns the pg_restore arguments for loading an archive into
// the database at dsn.
//
// The archive is fed on stdin rather than named as a file, so the pipeline
// can decrypt and decompress straight into pg_restore without staging the
// plaintext dump on disk.
//
// --single-transaction makes the restore all or nothing. A half-restored
// database is worse than one that was never touched, because it looks like
// it worked. It implies --exit-on-error, which is stated anyway so the
// intent is not buried in a flag's side effect: pg_restore otherwise
// continues past errors and can exit successfully having skipped objects.
//
// The cost is that a single transaction rules out parallel restore and holds
// its locks for the whole run. For a tool whose job is a restore you can
// trust, that is the right side of the trade.
func RestoreArgs(dsn string, opts RestoreOptions) []string {
	args := []string{
		"--dbname=" + dsn,
		"--single-transaction",
		"--exit-on-error",
	}

	if opts.NoOwner {
		args = append(args, "--no-owner")
	}

	return args
}

// Restore runs pg_restore against dsn, reading the archive from r.
func Restore(ctx context.Context, dsn string, r io.Reader, opts RestoreOptions) error {
	cmd := exec.CommandContext(ctx, "pg_restore", RestoreArgs(dsn, opts)...)
	cmd.Stdin = r

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
