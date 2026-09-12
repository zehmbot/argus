package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/zehmbot/argus/internal/retention"
	"github.com/zehmbot/argus/internal/verify"
)

const version = "0.1.0"

// Process exit codes. argus runs under cron, so monitoring has to be able to
// tell the failure modes apart: 3 (verification failed) is more alarming than
// 2 (backup failed), because it means we think we have backups but they do
// not restore.
const (
	exitConfig  = 1
	exitBackup  = 2
	exitVerify  = 3
	exitStorage = 4
)

// Sentinel errors that select an exit code. A failure that matches none of
// them is a failure of the operation itself.
var (
	errConfig  = errors.New("configuration error")
	errStorage = errors.New("storage error")
)

func exitCodeFor(err error) int {
	switch {
	case errors.Is(err, errConfig):
		return exitConfig
	case errors.Is(err, errStorage):
		return exitStorage
	case errors.Is(err, verify.ErrFailed), errors.Is(err, retention.ErrNoVerifiedBackup):
		// Prune refusing because nothing verified would survive is the same
		// alarm as a verification failing: there is no backup anyone has
		// shown to restore.
		return exitVerify
	default:
		return exitBackup
	}
}

// commonFlags are accepted by every command.
type commonFlags struct {
	config    string
	logFormat string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitConfig)
	}

	ctx := context.Background()
	args := os.Args[2:]

	switch os.Args[1] {
	case "version":
		fmt.Println(version)

	case "backup":
		fs, common := commandFlags("backup")
		parseFlags(fs, args, common)

		if err := runBackup(ctx, common.config); err != nil {
			fail(err)
		}

	case "list":
		fs, common := commandFlags("list")
		asJSON := fs.Bool("json", false, "print the manifests as JSON")
		parseFlags(fs, args, common)

		if err := runList(ctx, common.config, *asJSON, os.Stdout); err != nil {
			fail(err)
		}

	case "restore":
		// The documented form puts the backup id before the flags, and Go's
		// flag package stops parsing at the first non-flag argument. Take the
		// id off the front first so both orders work.
		backupID, rest := splitBackupID(args)

		fs, common := commandFlags("restore")
		target := fs.String("target", "", "connection string of the database to restore into")
		parseFlags(fs, rest, common)

		if backupID == "" {
			backupID = fs.Arg(0)
		}

		if err := runRestore(ctx, common.config, backupID, *target); err != nil {
			fail(err)
		}

	case "verify":
		backupID, rest := splitBackupID(args)

		fs, common := commandFlags("verify")
		latest := fs.Bool("latest", false, "verify the most recent backup")
		parseFlags(fs, rest, common)

		if backupID == "" {
			backupID = fs.Arg(0)
		}

		if err := runVerify(ctx, common.config, backupID, *latest, os.Stdout); err != nil {
			fail(err)
		}

	case "prune":
		fs, common := commandFlags("prune")
		apply := fs.Bool("apply", false, "actually delete the backups the policy drops")
		dryRun := fs.Bool("dry-run", false, "report what would be deleted (the default)")
		parseFlags(fs, args, common)

		if *apply && *dryRun {
			fail(fmt.Errorf("%w: --apply and --dry-run contradict each other", errConfig))
		}

		if err := runPrune(ctx, common.config, *apply, os.Stdout); err != nil {
			fail(err)
		}

	default:
		usage()
		os.Exit(exitConfig)
	}
}

// splitBackupID takes a leading positional argument off the front, so that a
// backup id written before the flags is not mistaken for the end of them.
func splitBackupID(args []string) (backupID string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}

	return "", args
}

// commandFlags builds the flag set every command shares.
func commandFlags(name string) (*flag.FlagSet, *commonFlags) {
	// ContinueOnError rather than ExitOnError: flag's own exit code is 2,
	// which this tool has already spent on "backup failed". A malformed
	// command line is a configuration error.
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	var common commonFlags
	fs.StringVar(&common.config, "config", "argus.yaml", "path to the argus config file")
	fs.StringVar(&common.logFormat, "log-format", logFormatText, "log format: text or json")

	return fs, &common
}

func parseFlags(fs *flag.FlagSet, args []string, common *commonFlags) {
	if err := fs.Parse(args); err != nil {
		os.Exit(exitConfig)
	}

	if err := setupLogging(common.logFormat); err != nil {
		fail(fmt.Errorf("%w: %w", errConfig, err))
	}
}

// fail reports err through the logger and exits with the code it selects.
//
// Errors go through the logger rather than straight to stderr so that a cron
// job running with --log-format=json gets its failures in the same shape as
// everything else. A failure nobody can parse is the one you most want to.
func fail(err error) {
	slog.Error(err.Error())
	os.Exit(exitCodeFor(err))
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: argus <command> [flags]

commands:
  backup  [--config argus.yaml]                     back up the configured database
  list    [--config argus.yaml] [--json]            list the backups in storage
  restore <backup-id> --target postgres://...       restore a backup into a database
  verify  <backup-id> | --latest                    restore a backup and check it
  prune   [--apply]                                 apply the retention policy
  version                                           print the argus version

every command also accepts --log-format=text|json
`)
}
