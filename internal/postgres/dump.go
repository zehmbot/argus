// Package postgres invokes the pg_dump and pg_restore binaries.
package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DumpArgs returns the pg_dump arguments that produce a custom-format dump
// of dsn at outputPath. Compression is disabled so the pipeline's own gzip
// step is the only compression layer, keeping the manifest's recorded
// compression method accurate.
func DumpArgs(dsn, outputPath string) []string {
	return []string{
		"--format=custom",
		"--compress=0",
		"--file=" + outputPath,
		dsn,
	}
}

// Dump runs pg_dump against dsn, writing a custom-format dump to outputPath.
// On failure it removes any partial file left at outputPath.
func Dump(ctx context.Context, dsn, outputPath string) error {
	cmd := exec.CommandContext(ctx, "pg_dump", DumpArgs(dsn, outputPath)...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
