package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed indexes.sql
var indexes string

// Pragmas ride in the DSN rather than being executed after the open, because
// PRAGMA is per-connection and database/sql hands out a pool. Running
// `PRAGMA foreign_keys=ON` through db.Exec sets it on whichever single
// connection served that call and leaves every other one with SQLite's default,
// which is off — so the ON DELETE CASCADE on every child table was inert, and a
// project deleted by a sync sweep left its memberships behind as orphans.
const connectionPragmas = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// Open creates a SQLite connection and ensures the schema exists.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+connectionPragmas)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// In-memory databases are per-connection in SQLite. Without this,
	// Go's connection pool hands out separate blank databases to each goroutine.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	if _, err := db.Exec(indexes); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating indexes: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	// Check if schema already exists by looking for the projects table
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='project_items'").Scan(&name)
	if err == sql.ErrNoRows {
		// Fresh database — apply full schema (includes sync tables)
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("checking schema: %w", err)
	}

	// Existing database — apply incremental migrations
	if err := migrateSyncTables(db); err != nil {
		return err
	}
	if err := migrateSyncOrigin(db); err != nil {
		return err
	}
	if err := migrateOrphanedChildRows(db); err != nil {
		return err
	}
	if err := migrateItemRepo(db); err != nil {
		return err
	}
	if err := migrateItemNumber(db); err != nil {
		return err
	}
	if err := migrateProjectStatus(db); err != nil {
		return err
	}
	return nil
}

// migrateSyncOrigin adds the table that pairs a database with its API. Left
// empty for an existing database: which API it belongs to is not derivable from
// anything in it, so the first pull is what records it.
func migrateSyncOrigin(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='sync_origin'").Scan(&name)
	if err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("checking sync_origin: %w", err)
	}

	const create = `
CREATE TABLE sync_origin (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    api_url TEXT NOT NULL,
    adopted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);`
	if _, err := db.Exec(create); err != nil {
		return fmt.Errorf("creating sync_origin: %w", err)
	}
	return nil
}

// migrateOrphanedChildRows deletes rows the cascades would never have allowed:
// memberships, dependencies and tasks whose parent is gone. Every database
// written before foreign keys were actually enforced has them — a sync sweep
// that deleted a project left its memberships pointing at nothing, and a real
// database had accumulated 46 of them.
//
// Deleting is not a data loss decision. A membership whose project does not
// exist names no project, so nothing can reach it and nothing can render it;
// were the project to come back, the item pull rebuilds the membership from the
// server's own answer. It runs before migrateProjectStatus because that
// migration's foreign key check is what these rows fail.
func migrateOrphanedChildRows(db *sql.DB) error {
	sweeps := []struct {
		table  string
		delete string
	}{
		{"project_item_memberships", `DELETE FROM project_item_memberships
			WHERE item_id NOT IN (SELECT id FROM project_items)
			   OR project_id NOT IN (SELECT id FROM projects)`},
		{"project_item_dependencies", `DELETE FROM project_item_dependencies
			WHERE item_id NOT IN (SELECT id FROM project_items)
			   OR depends_on_id NOT IN (SELECT id FROM project_items)`},
		{"project_item_tasks", `DELETE FROM project_item_tasks
			WHERE item_id NOT IN (SELECT id FROM project_items)`},
	}
	for _, sweep := range sweeps {
		// Tasks and dependencies arrived with the schema rather than through a
		// migration, so a database old enough may not have the table at all.
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", sweep.table).Scan(&name)
		if err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return fmt.Errorf("checking %s: %w", sweep.table, err)
		}
		if _, err := db.Exec(sweep.delete); err != nil {
			return fmt.Errorf("sweeping orphaned %s: %w", sweep.table, err)
		}
	}
	return nil
}

// migrateProjectStatus adds the project lifecycle columns and, critically, drops
// the global UNIQUE on projects.name in favor of the partial index over active
// projects in indexes.sql.
//
// The unique constraint is why this is a table rebuild rather than three ALTER
// TABLE ADD COLUMNs. SQLite attaches it as an implicit index that no DDL can
// drop, and leaving it would fail the entire pull transaction the first time the
// server returned a completed project beside a live one sharing its name —
// exactly the case the partial index exists to allow.
//
// Foreign keys are off for the swap because project_item_memberships references
// projects ON DELETE CASCADE, so dropping the old table with them on would take
// every membership with it. PRAGMA foreign_keys is a no-op inside a transaction,
// hence the explicit ordering below; this is SQLite's own documented procedure
// for altering a table it otherwise cannot.
//
// The pragma and the transaction must reach the same connection, which is what
// the reserved *sql.Conn is for. Issued through the pool they land wherever, so
// the rebuild would run with cascades live and DROP TABLE projects would take
// every membership in the database with it.
func migrateProjectStatus(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM pragma_table_info('projects') WHERE name = 'status'").Scan(&name)
	if err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("checking projects.status: %w", err)
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserving a connection for the projects rebuild: %w", err)
	}
	// Restored before the connection goes back to the pool: handing it back with
	// foreign keys off would silently disable every cascade for whoever draws it
	// next.
	defer func() {
		_, _ = conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")
		_ = conn.Close()
	}()

	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("disabling foreign keys for the projects rebuild: %w", err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning the projects rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const rebuild = `
CREATE TABLE projects_rebuilt (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    status_reason TEXT,
    closed_at TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO projects_rebuilt (id, name, description, position, created_at)
    SELECT id, name, description, position, created_at FROM projects;
DROP TABLE projects;
ALTER TABLE projects_rebuilt RENAME TO projects;`
	if _, err := tx.Exec(rebuild); err != nil {
		return fmt.Errorf("rebuilding projects: %w", err)
	}

	var violations int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		return fmt.Errorf("checking foreign keys after the projects rebuild: %w", err)
	}
	if violations > 0 {
		return fmt.Errorf("projects rebuild left %d dangling foreign key rows", violations)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing the projects rebuild: %w", err)
	}
	return nil
}

// migrateItemNumber adds project_items.number to databases created before the
// column existed. Left null rather than backfilled: with sync on the server
// owns the numbering and the next pull fills every row in, and with sync off
// the first create allocates from an empty column, which starts at 1.
//
// The UNIQUE constraint lives in indexes.sql because ALTER TABLE ADD COLUMN
// cannot carry one.
func migrateItemNumber(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM pragma_table_info('project_items') WHERE name = 'number'").Scan(&name)
	if err == sql.ErrNoRows {
		if _, err := db.Exec("ALTER TABLE project_items ADD COLUMN number INTEGER"); err != nil {
			return fmt.Errorf("adding project_items.number: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("checking project_items.number: %w", err)
	}
	return nil
}

// migrateItemRepo adds project_items.repo to databases created before the
// column existed. Detected by column presence rather than a version counter,
// matching how migrateSyncTables probes for its tables.
func migrateItemRepo(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM pragma_table_info('project_items') WHERE name = 'repo'").Scan(&name)
	if err == sql.ErrNoRows {
		if _, err := db.Exec("ALTER TABLE project_items ADD COLUMN repo TEXT"); err != nil {
			return fmt.Errorf("adding project_items.repo: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("checking project_items.repo: %w", err)
	}
	return nil
}

func migrateSyncTables(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='pending_sync'").Scan(&name)
	if err == sql.ErrNoRows {
		const syncMigration = `
CREATE TABLE pending_sync (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE TABLE sync_state (
    entity_type TEXT PRIMARY KEY,
    last_pull_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000Z',
    last_push_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000Z'
);`
		if _, err := db.Exec(syncMigration); err != nil {
			return fmt.Errorf("applying sync migration: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("checking sync tables: %w", err)
	}
	return nil
}
