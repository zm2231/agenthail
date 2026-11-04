package daemon

import "time"

type daemonStartup struct {
	began        time.Time
	registryOpen time.Duration
}

// SetStartup records when this process began starting and how long opening its registry took.
func (d *Daemon) SetStartup(began time.Time, registryOpen time.Duration) {
	d.startup = daemonStartup{began: began, registryOpen: registryOpen}
}

func (d *Daemon) logRegistryStartup() {
	if d.startup.began.IsZero() {
		return
	}
	d.log.Printf("registry open took %s", d.startup.registryOpen.Round(time.Millisecond))
	if d.Registry == nil {
		return
	}
	if migration, migrated := d.Registry.Migration(); migrated {
		d.log.Printf("registry migrated from schema %d to %d: backup %s, migration %s", migration.From, migration.To, migration.Backup.Round(time.Millisecond), migration.Migrate.Round(time.Millisecond))
	}
}

func (d *Daemon) logStarted() {
	if d.startup.began.IsZero() {
		d.log.Printf("started; %d surfaces", len(d.Surfaces))
		return
	}
	d.log.Printf("started; %d surfaces, %s after launch", len(d.Surfaces), time.Since(d.startup.began).Round(time.Millisecond))
}
