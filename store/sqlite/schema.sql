-- Schema definition for sqlc

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    -- Unique among ACTIVE projects only, via the partial index in indexes.sql.
    -- A global UNIQUE would make a completed `clisteno` own the name forever,
    -- and would fail the whole pull the moment the server returns two projects
    -- that shared one.
    name TEXT NOT NULL,
    description TEXT,
    -- active | done | dropped. A project is a finite effort, so completing it
    -- is what hides it — there is no separate archived flag the way items have
    -- one. `dropped` requires a reason, which is what stops it reading as
    -- deferred. No CHECK against a lookup table: the API validates the value
    -- and this database mirrors what it is told.
    status TEXT NOT NULL DEFAULT 'active',
    status_reason TEXT,
    closed_at TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE project_items (
    id TEXT PRIMARY KEY,
    -- The handle: short enough to type, unlike the UUID key. Assigned by the
    -- ichrisbirch API when sync is on, and locally when it is off and this
    -- database is the only authority. Null in the window between creating an
    -- item offline and the push that earns it a number.
    number INTEGER,
    title TEXT NOT NULL,
    notes TEXT,
    -- Repo registry name. Null for the many items that are not repo work at
    -- all — home projects, things to sell, errands.
    repo TEXT,
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

CREATE TABLE project_item_dependencies (
    item_id TEXT NOT NULL REFERENCES project_items(id) ON DELETE CASCADE,
    depends_on_id TEXT NOT NULL REFERENCES project_items(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, depends_on_id),
    CHECK (item_id != depends_on_id)
);

CREATE TABLE project_item_tasks (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES project_items(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    completed INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

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
);

-- Which API this database belongs to. A pull deletes everything the server did
-- not return, so a database and its API are a pair and pointing one at the other
-- one is a wipe rather than a sync. Recording the pairing is what lets a pull
-- refuse instead of obeying. Single row, enforced by the CHECK.
CREATE TABLE sync_origin (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    api_url TEXT NOT NULL,
    adopted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE undo_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    previous_state TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
