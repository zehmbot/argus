package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

const version = "0.1.0"

// Process exit codes. argus runs under cron, so monitoring has to be able to
// tell the failure modes apart: 3 (verification failed) is more alarming than
// 2 (backup failed), because it means we think we have backups but they do
// not restore. Code 3 arrives with the verify command.
const (
	exitConfig  = 1
	exitBackup  = 2
	exitStorage = 4
)

// Sentinel errors that select an exit code. A failure that matches neither is
// a failure of the operation itself.
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
	default:
		return exitBackup
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitConfig)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(version)

	case "backup":
		// ContinueOnError rather than ExitOnError: flag's own exit code is 2,
		// which this tool has already spent on "backup failed". A malformed
		// command line is a configuration error.
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		configPath := fs.String("config", "argus.yaml", "path to the argus config file")

		if err := fs.Parse(os.Args[2:]); err != nil {
			os.Exit(exitConfig)
		}

		if err := runBackup(context.Background(), *configPath); err != nil {
			fmt.Fprintln(os.Stderr, "argus:", err)
			os.Exit(exitCodeFor(err))
		}

	default:
		usage()
		os.Exit(exitConfig)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: argus <command> [flags]

commands:
  backup [--config argus.yaml]   back up the configured database
  version                        print the argus version
`)
}
