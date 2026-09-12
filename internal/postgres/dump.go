// Package postgres invokes the pg_dump and pg_restore binaries.
package postgres

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// DumpArgs returns the pg_dump arguments that produce a custom-format dump
// of dsn on stdout.
//
// No --file: the dump is written to a pipe so it can be compressed,
// encrypted and uploaded as it is produced, rather than staged on disk.
//
// Compression is disabled so the pipeline's own gzip step is the only
// compression layer, keeping the manifest's recorded compression method
// accurate.
func DumpArgs(dsn string) []string {
	return []string{
		"--format=custom",
		"--compress=0",
		dsn,
	}
}

// Dump runs pg_dump against dsn, writing a custom-format dump to w.
func Dump(ctx context.Context, dsn string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "pg_dump", DumpArgs(dsn)...)
	cmd.Stdout = w

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

// DumpVersion returns the version of the pg_dump binary on PATH, such as
// "17.11". The manifest records it because a custom-format dump can only be
// read back by a pg_restore of the same major version or newer, so verify
// needs to know which binary produced it.
func DumpVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "pg_dump", "--version")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pg_dump --version: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	version, err := parseDumpVersion(stdout.String())
	if err != nil {
		return "", fmt.Errorf("pg_dump --version: %w", err)
	}

	return version, nil
}

// parseDumpVersion pulls the version out of pg_dump's banner, which reads
// "pg_dump (PostgreSQL) 17.11" and on packaged builds carries a distribution
// suffix after it.
func parseDumpVersion(banner string) (string, error) {
	fields := strings.Fields(banner)
	if len(fields) < 3 || fields[0] != "pg_dump" {
		return "", fmt.Errorf("unexpected version output %q", strings.TrimSpace(banner))
	}

	return fields[2], nil
}
