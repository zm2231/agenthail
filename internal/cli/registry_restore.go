package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
)

const registryUsage = "usage: agenthail registry restore [<N>]"

// This never touches a.registry(): the build running it can be older than the registry it replaces.
func (a *App) cmdRegistry(args []string) error {
	if len(args) == 0 || args[0] != "restore" || len(args) > 2 {
		return errors.New(registryUsage)
	}
	if len(args) == 1 {
		return printMigrationBackups()
	}
	target, err := strconv.Atoi(args[1])
	if err != nil || target < 1 {
		return errors.New(registryUsage)
	}
	release, err := daemon.HoldDaemonLock()
	if err != nil {
		return fmt.Errorf("the daemon is running or starting; run 'agenthail daemon stop' first: %w", err)
	}
	defer release()
	restored, err := registry.RestoreMigrationBackup(target)
	if restored.From != "" {
		fmt.Printf("Restored the registry to schema %d from %s\n", restored.Schema, restored.From)
		if restored.Aside != "" {
			fmt.Printf("The registry it replaced is kept as %s\n", restored.Aside)
		}
		fmt.Println("If the daemon is not running, start it with 'agenthail daemon start'")
	}
	return err
}

func printMigrationBackups() error {
	backups := registry.ListMigrationBackups()
	if len(backups) == 0 {
		fmt.Println("No copies are kept from before a registry upgrade.")
		return nil
	}
	for _, backup := range backups {
		fmt.Printf("%d\tschema %d\t%s\t%s\n", backup.Target, backup.Schema, humanSize(backup.Size), backup.Path)
	}
	return nil
}
