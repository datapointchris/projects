package sqlite

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// An in-memory database is pinned to one connection, which is exactly why the
// old version of this test passed while cascades were inert everywhere else.
// The pragma has to hold on a file-backed database, on every connection the
// pool hands out.
func TestOpen_ForeignKeysEnabledOnEveryConnection(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var mu sync.Mutex
	var off int
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var fk int
			if err := database.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
				mu.Lock()
				off++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if off > 0 {
		t.Errorf("%d of 40 connections had foreign keys off: every cascade in the schema is inert on them", off)
	}
}

// The cascade is the thing foreign keys were declared for. Deleting a project
// left its memberships behind for as long as the pragma only reached one
// connection, which is how a real database collected 46 orphans.
func TestDeletingAProjectTakesItsMembershipsWithIt(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "cascade.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	mustExec(t, database, `INSERT INTO projects (id, name) VALUES ('p1', 'work')`)
	mustExec(t, database, `INSERT INTO project_items (id, title) VALUES ('i1', 'thing')`)
	mustExec(t, database, `INSERT INTO project_item_memberships (item_id, project_id, position) VALUES ('i1', 'p1', 0)`)
	mustExec(t, database, `DELETE FROM projects WHERE id = 'p1'`)

	var left int
	if err := database.QueryRow(`SELECT COUNT(*) FROM project_item_memberships`).Scan(&left); err != nil {
		t.Fatalf("counting memberships: %v", err)
	}
	if left != 0 {
		t.Errorf("%d membership(s) survived the project they belong to", left)
	}
}

// Databases written before the pragma reached every connection carry orphans
// that the projects rebuild's own foreign key check then refuses to migrate
// past, which locks the user out of their data entirely.
func TestOpen_SweepsOrphansLeftByTheUnenforcedCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphans.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening raw: %v", err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	mustExec(t, raw, `INSERT INTO project_items (id, title) VALUES ('i1', 'thing')`)
	mustExec(t, raw, `INSERT INTO project_item_memberships (item_id, project_id, position) VALUES ('i1', 'gone', 0)`)
	mustExec(t, raw, `INSERT INTO project_item_dependencies (item_id, depends_on_id) VALUES ('i1', 'gone')`)
	mustExec(t, raw, `INSERT INTO project_item_tasks (id, item_id, title) VALUES ('t1', 'gone', 'orphan')`)
	if err := raw.Close(); err != nil {
		t.Fatalf("closing raw: %v", err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open refused a database with orphaned rows: %v", err)
	}
	defer func() { _ = database.Close() }()

	var violations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		t.Fatalf("checking foreign keys: %v", err)
	}
	if violations != 0 {
		t.Errorf("%d orphaned rows survived the sweep", violations)
	}

	// The item itself is not collateral: only the unreachable rows go.
	var items int
	if err := database.QueryRow(`SELECT COUNT(*) FROM project_items`).Scan(&items); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if items != 1 {
		t.Errorf("the sweep took %d item(s) with it", 1-items)
	}
}

func mustExec(t *testing.T, database *sql.DB, query string) {
	t.Helper()
	if _, err := database.Exec(query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestOpen_IncrementalMigration(t *testing.T) {
	// Simulate an existing database without sync tables
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	database.SetMaxOpenConns(1)

	// Create only the core tables (no sync tables)
	_, err = database.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		CREATE TABLE project_items (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			completed INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
	`)
	if err != nil {
		t.Fatalf("creating core tables: %v", err)
	}

	// Insert test data
	_, err = database.Exec("INSERT INTO projects (id, name) VALUES ('p1', 'existing')")
	if err != nil {
		t.Fatalf("inserting test data: %v", err)
	}

	// Run migration — should add sync tables without losing data
	if err := migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify sync tables exist
	var name string
	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='pending_sync'",
	).Scan(&name)
	if err != nil {
		t.Error("pending_sync table not created by migration")
	}

	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='sync_state'",
	).Scan(&name)
	if err != nil {
		t.Error("sync_state table not created by migration")
	}

	// Verify existing data survived
	var projectName string
	err = database.QueryRow("SELECT name FROM projects WHERE id='p1'").Scan(&projectName)
	if err != nil {
		t.Fatalf("existing data lost: %v", err)
	}
	if projectName != "existing" {
		t.Errorf("expected project name 'existing', got %q", projectName)
	}

	_ = database.Close()
}

func TestMigrate_AddsRepoColumnToExistingDatabase(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	database.SetMaxOpenConns(1)

	// A pre-repo database with a row already in it — the migration must add the
	// column without disturbing existing items.
	_, err = database.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		CREATE TABLE project_items (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			notes TEXT,
			completed INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		INSERT INTO project_items (id, title) VALUES ('i1', 'pre-existing item');
	`)
	if err != nil {
		t.Fatalf("creating pre-repo table: %v", err)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var repo sql.NullString
	var title string
	row := database.QueryRow("SELECT title, repo FROM project_items WHERE id = 'i1'")
	if err := row.Scan(&title, &repo); err != nil {
		t.Fatalf("selecting migrated row: %v", err)
	}
	if title != "pre-existing item" {
		t.Errorf("existing row was disturbed: title = %q", title)
	}
	if repo.Valid {
		t.Errorf("repo should be null for a pre-existing item, got %q", repo.String)
	}

	// Idempotent: a second run must not fail on the already-present column.
	if err := migrate(database); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// A database a user already has predates indexes.sql, and the name and number
// rules live there. Applied only to new databases, an upgrade would drop both.
func TestOpen_ExistingDatabaseGainsTheUniquenessRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if _, err := legacy.Exec(schema); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	_ = legacy.Close()

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	mustExec(t, database, "INSERT INTO projects (id, name) VALUES ('p1', 'taken')")
	if _, err := database.Exec("INSERT INTO projects (id, name) VALUES ('p2', 'taken')"); err == nil {
		t.Error("a second active project took a name an active project holds")
	}
	mustExec(t, database, "INSERT INTO project_items (id, title, number) VALUES ('i1', 'first', 7)")
	if _, err := database.Exec("INSERT INTO project_items (id, title, number) VALUES ('i2', 'second', 7)"); err == nil {
		t.Error("a second item took a number another item holds")
	}
}

func TestMigrate_AddsNumberColumnToExistingDatabase(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	database.SetMaxOpenConns(1)

	_, err = database.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		CREATE TABLE project_items (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			notes TEXT,
			repo TEXT,
			completed INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		INSERT INTO project_items (id, title) VALUES ('i1', 'pre-number item');
	`)
	if err != nil {
		t.Fatalf("creating pre-number table: %v", err)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Deliberately not backfilled: with sync on the pull supplies every number,
	// and with sync off the first create allocates from an empty column.
	var number sql.NullInt64
	var title string
	row := database.QueryRow("SELECT title, number FROM project_items WHERE id = 'i1'")
	if err := row.Scan(&title, &number); err != nil {
		t.Fatalf("selecting migrated row: %v", err)
	}
	if title != "pre-number item" {
		t.Errorf("existing row was disturbed: title = %q", title)
	}
	if number.Valid {
		t.Errorf("number should be null for a pre-existing item, got %d", number.Int64)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// Several items can be waiting for a number at once, so the unique index has to
// tolerate more than one null. SQLite does; a UNIQUE column constraint applied
// to a backfilled column would not have been reachable by ALTER TABLE anyway.
func TestOpen_NumberIndexAllowsManyUnnumberedItems(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer func() { _ = database.Close() }()

	for _, id := range []string{"a", "b", "c"} {
		if _, err := database.Exec("INSERT INTO project_items (id, title) VALUES (?, ?)", id, id); err != nil {
			t.Fatalf("inserting unnumbered item %s: %v", id, err)
		}
	}

	if _, err := database.Exec("INSERT INTO project_items (id, title, number) VALUES ('d', 'd', 7)"); err != nil {
		t.Fatalf("inserting numbered item: %v", err)
	}
	if _, err := database.Exec("INSERT INTO project_items (id, title, number) VALUES ('e', 'e', 7)"); err == nil {
		t.Error("a duplicate number was accepted — the handle has to name exactly one item")
	}
}

// TestMigrate_ReplacesTheGlobalNameConstraint is the one migration that rebuilds
// a table rather than adding a column, because SQLite attaches a column-level
// UNIQUE as an index no DDL can drop. Two things have to survive it: the rows,
// and the memberships that cascade off projects when the old table is dropped.
func TestMigrate_ReplacesTheGlobalNameConstraint(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enabling foreign keys: %v", err)
	}
	_, err = database.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		CREATE TABLE project_items (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			notes TEXT,
			repo TEXT,
			number INTEGER,
			completed INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
		CREATE TABLE project_item_memberships (
			item_id TEXT NOT NULL REFERENCES project_items(id) ON DELETE CASCADE,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (item_id, project_id)
		);
		INSERT INTO projects (id, name, description, position) VALUES ('p1', 'clisteno', 'notes', 3);
		INSERT INTO project_items (id, title) VALUES ('i1', 'existing item');
		INSERT INTO project_item_memberships (item_id, project_id, position) VALUES ('i1', 'p1', 0);
	`)
	if err != nil {
		t.Fatalf("creating pre-status tables: %v", err)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var name, description, status string
	var position int
	row := database.QueryRow("SELECT name, description, status, position FROM projects WHERE id = 'p1'")
	if err := row.Scan(&name, &description, &status, &position); err != nil {
		t.Fatalf("selecting migrated project: %v", err)
	}
	if name != "clisteno" || description != "notes" || position != 3 {
		t.Errorf("row lost data in the rebuild: name=%q description=%q position=%d", name, description, position)
	}
	if status != "active" {
		t.Errorf("status = %q, want active — every project was active before the column existed", status)
	}

	// The whole reason foreign keys are off for the swap: memberships cascade
	// off projects, so dropping the old table with them on would take these.
	var memberships int
	if err := database.QueryRow("SELECT COUNT(*) FROM project_item_memberships").Scan(&memberships); err != nil {
		t.Fatalf("counting memberships: %v", err)
	}
	if memberships != 1 {
		t.Errorf("memberships = %d, want 1 — the rebuild cascaded them away", memberships)
	}

	var foreignKeysOn int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeysOn); err != nil {
		t.Fatalf("reading foreign_keys pragma: %v", err)
	}
	if foreignKeysOn != 1 {
		t.Error("foreign keys left off after the rebuild — every later cascade would silently stop working")
	}

	// The global constraint is gone and the partial one takes over. Created
	// directly rather than by applying indexes.sql, which touches tables this
	// fixture has no reason to build; that the file carries it is covered by
	// TestOpen_ExistingDatabaseGainsTheUniquenessRules.
	if _, err := database.Exec(
		"CREATE UNIQUE INDEX idx_projects_name_active ON projects(name) WHERE status = 'active'",
	); err != nil {
		t.Fatalf("creating the partial index: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO projects (id, name, status) VALUES ('p2', 'clisteno', 'completed')",
	); err != nil {
		t.Errorf("a closed project must be allowed to share a name: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO projects (id, name) VALUES ('p3', 'clisteno')",
	); err == nil {
		t.Error("two ACTIVE projects sharing a name must still be refused")
	}
}
