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

//go:embed lookup_rows.sql
var lookupRows string

// Pragmas ride in the DSN rather than being executed after the open, because
// PRAGMA is per-connection and database/sql hands out a pool. Running
// `PRAGMA foreign_keys=ON` through db.Exec sets it on whichever single
// connection served that call and leaves every other one with SQLite's default,
// which is off — so the ON DELETE CASCADE on every child table was inert, and a
// deleted project left its memberships behind as orphans.
//
// A transaction takes the write lock when it begins, because a rule reads inside
// it and then writes. SQLite refuses a deferred transaction's upgrade from read to
// write outright once another writer has committed, with no busy wait. Beginning
// immediate waits out busy_timeout instead, and what the rule read holds at commit.
const connectionOptions = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Open creates a SQLite connection and ensures the schema exists.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+connectionOptions)
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

	if _, err := db.Exec(lookupRows); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seeding lookup tables: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	// Check if schema already exists by looking for the projects table
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='project_items'").Scan(&name)
	if err == sql.ErrNoRows {
		// Fresh database — apply full schema
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("checking schema: %w", err)
	}

	// Existing database — apply incremental migrations
	if err := dropSyncTables(db); err != nil {
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
	return migrateKindAndCompletedAt(db)
}

// migrateKindAndCompletedAt adds the lookup tables and the columns a database from
// before them lacks. A completed_at stays null, because the time an item was
// finished before the column existed is unknown.
func migrateKindAndCompletedAt(db *sql.DB) error {
	const lookups = `
CREATE TABLE IF NOT EXISTS project_kinds (name TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS project_statuses (name TEXT PRIMARY KEY);`
	if _, err := db.Exec(lookups); err != nil {
		return fmt.Errorf("creating lookup tables: %w", err)
	}
	columns := []struct{ table, column, definition string }{
		{"projects", "kind", "TEXT NOT NULL DEFAULT 'life'"},
		{"project_items", "completed_at", "TEXT"},
		{"project_item_tasks", "completed_at", "TEXT"},
	}
	for _, c := range columns {
		// Tasks arrived with the schema rather than through a migration, so a
		// database old enough may not have the table at all.
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", c.table).Scan(&name)
		if err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return fmt.Errorf("checking %s: %w", c.table, err)
		}
		err = db.QueryRow("SELECT name FROM pragma_table_info(?) WHERE name = ?", c.table, c.column).Scan(&name)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("checking %s.%s: %w", c.table, c.column, err)
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.definition)); err != nil {
			return fmt.Errorf("adding %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// dropSyncTables removes what a database kept while it was a copy of an API: the
// queue of pushes, the pull times, and the API it was bound to. It refuses while
// the queue holds a row, because that edit never reached the API and the queue is
// the only record saying so. The other two hold nothing a person wrote.
//
// MIGRATION: retires with config/retired_sync.go.
func dropSyncTables(db *sql.DB) error {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='pending_sync'").Scan(&name)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("checking pending_sync: %w", err)
	}
	if err == nil {
		var queued int
		if err := db.QueryRow("SELECT COUNT(*) FROM pending_sync").Scan(&queued); err != nil {
			return fmt.Errorf("counting pending_sync: %w", err)
		}
		if queued > 0 {
			return fmt.Errorf("%d edits here were queued for %s and never reached it. Push them with `projects sync` "+
				"from v1.24.2, or discard them with DELETE FROM pending_sync", queued, boundAPI(db))
		}
	}
	const drop = `
DROP TABLE IF EXISTS pending_sync;
DROP TABLE IF EXISTS sync_state;
DROP TABLE IF EXISTS sync_origin;`
	if _, err := db.Exec(drop); err != nil {
		return fmt.Errorf("dropping the sync tables: %w", err)
	}
	return nil
}

// boundAPI is the API a synced database was bound to, as its refusal names it.
func boundAPI(db *sql.DB) string {
	var url string
	if err := db.QueryRow("SELECT api_url FROM sync_origin WHERE id = 1").Scan(&url); err != nil {
		return "the API this database synced with"
	}
	return url
}

// migrateOrphanedChildRows deletes rows the cascades would never have allowed:
// memberships, dependencies and tasks whose parent is gone. Every database
// written before foreign keys were actually enforced has them — a deleted
// project left its memberships pointing at nothing.
//
// Deleting is not a data loss decision. A membership whose project does not
// exist names no project, so nothing can reach it and nothing can render it. It
// runs before migrateProjectStatus because that
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
// drop, and leaving it would refuse a completed project beside a live one
// sharing its name — exactly the case the partial index exists to allow.
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
// column existed. Left null rather than backfilled, so the first create
// allocates from an empty column, which starts at 1.
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
// as every migration here is.
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
