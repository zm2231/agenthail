package registry

import (
	"database/sql"
	"fmt"
	"slices"
)

// Level is where a registry's schema stands: its schema number and the named migrations applied on top.
type Level struct {
	Schema  int
	Applied []string
}

func (l Level) String() string {
	if n := len(l.Applied); n > 0 {
		return l.Applied[n-1]
	}
	return fmt.Sprintf("schema %d", l.Schema)
}

// UpgradedFrom is the key of the copy taken when a registry at before was upgraded to l, false when l is not past before.
func (l Level) UpgradedFrom(before Level) (string, bool) {
	if l.Schema > before.Schema {
		return schemaBackupKey(l.Schema), true
	}
	added := without(l.Applied, before.Applied)
	if len(added) == 0 {
		return "", false
	}
	return added[len(added)-1], true
}

func currentLevel() Level {
	return Level{Schema: schemaVersion, Applied: knownMigrationNames()}
}

// DiskLevel never migrates, so it reads a registry newer than this build. Schema is -1 when the registry cannot be read.
func DiskLevel() Level {
	return levelOf(defaultPath())
}

func (l Level) equal(other Level) bool {
	return l.Schema == other.Schema && slices.Equal(l.Applied, other.Applied)
}

func levelOf(path string) Level {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return Level{Schema: -1}
	}
	defer db.Close()
	return levelOn(db)
}

func levelOn(db queryer) Level {
	level := Level{}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&level.Schema); err != nil {
		return Level{Schema: -1}
	}
	applied, err := appliedMigrations(db)
	if err != nil {
		return Level{Schema: -1}
	}
	level.Applied = applied
	return level
}
