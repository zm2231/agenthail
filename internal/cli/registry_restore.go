package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/registry"
)

const registryUsage = "usage: agenthail registry restore [<copy>]"

// This never touches a.registry(): the build running it can be older than the registry it replaces.
func (a *App) cmdRegistry(args []string) error {
	if len(args) == 0 || args[0] != "restore" || len(args) > 2 {
		return errors.New(registryUsage)
	}
	if len(args) == 1 {
		return printMigrationBackups()
	}
	key := args[1]
	// Older builds' refusals and release notes give the bare schema number.
	if n, err := strconv.Atoi(key); err == nil && n > 0 {
		key = "v" + strconv.Itoa(n)
	}
	if !registry.ValidBackupKey(key) {
		return errors.New(registryUsage)
	}
	release, err := daemonctl.HoldLock()
	if err != nil {
		return fmt.Errorf("the daemon is running or starting; run 'agenthail daemon stop' first: %w", err)
	}
	defer release()
	restored, err := registry.RestoreMigrationBackup(key)
	if restored.From != "" {
		fmt.Printf("Restored the registry to %s from %s\n", restored.Level, restored.From)
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
		fmt.Printf("%s\t%s\t%s\n", backup.Key, humanSize(backup.Size), backup.Path)
	}
	return nil
}
