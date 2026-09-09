package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/postgres"
)

// runList prints the backups recorded in storage, newest first.
func runList(ctx context.Context, configPath string, asJSON bool, out io.Writer) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	// list never connects to the database. It needs the DSN only to learn
	// which database's backups to show, and ParseDSN does not dial. Taking
	// the name from the DSN rather than the config file means the two can
	// never disagree and list the wrong database's backups.
	dsn, err := config.SourceDSN()
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	conn, err := postgres.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}

	backend, err := openBackend(ctx, cfg)
	if err != nil {
		return err
	}

	manifests, err := manifest.List(ctx, backend, conn.Database)
	if err != nil {
		return fmt.Errorf("%w: %w", errStorage, err)
	}

	if asJSON {
		return writeJSON(out, manifests)
	}

	return writeTable(out, manifests)
}

func writeJSON(out io.Writer, manifests []manifest.Manifest) error {
	// An empty result must print [] rather than null, so that a script
	// piping this into jq can iterate it without a special case.
	if manifests == nil {
		manifests = []manifest.Manifest{}
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifests); err != nil {
		return fmt.Errorf("writing json: %w", err)
	}

	return nil
}

func writeTable(out io.Writer, manifests []manifest.Manifest) error {
	if len(manifests) == 0 {
		_, err := fmt.Fprintln(out, "no backups found")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "BACKUP ID\tCREATED (UTC)\tSIZE\tVERIFIED")
	for _, m := range manifests {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			m.BackupID,
			m.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
			humanSize(m.Artifact.SizeBytes),
			m.Verification.Status,
		)
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing table: %w", err)
	}

	return nil
}

// humanSize renders a byte count in binary units, because a raw count of a
// multi-gigabyte artifact tells an operator nothing at a glance.
func humanSize(bytes int64) string {
	const unit = 1024

	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
