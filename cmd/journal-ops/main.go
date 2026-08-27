// Command journal-ops performs offline backup and restore operations for the
// single-process development Journal. The Hub must be stopped for restore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func main() {
	mode := flag.String("mode", "", "operation: backup or restore")
	journal := flag.String("journal", "", "source Journal path for backup")
	backup := flag.String("backup", "", "backup Journal path for restore")
	destination := flag.String("destination", "", "destination Journal path")
	manifest := flag.String("manifest", "", "SHA-256 manifest path")
	flag.Parse()

	if err := run(*mode, *journal, *backup, *destination, *manifest); err != nil {
		log.Fatal(err)
	}
}

func run(mode, journal, backup, destination, manifest string) error {
	switch mode {
	case "backup":
		if journal == "" || destination == "" || manifest == "" {
			return errors.New("backup requires --journal, --destination, and --manifest")
		}
		store, err := core.OpenJournal(journal)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.BackupWithManifest(destination, manifest); err != nil {
			return err
		}
		fmt.Printf("Journal backup written: %s\nManifest written: %s\n", destination, manifest)
		return nil
	case "restore":
		if backup == "" || destination == "" || manifest == "" {
			return errors.New("restore requires --backup, --destination, and --manifest")
		}
		if err := core.RestoreJournalBackupWithManifest(backup, manifest, destination); err != nil {
			return err
		}
		fmt.Printf("Journal restored: %s\n", destination)
		return nil
	default:
		return fmt.Errorf("unsupported --mode %q", mode)
	}
}
