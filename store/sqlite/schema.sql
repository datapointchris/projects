-- Schema definition for sqlc

-- Lookup tables. lookup_rows.sql holds their rows, and
-- TestEachStoreHoldsTheKnownKindsAndStatuses holds those to model's lists.
CREATE TABLE project_kinds (
    name TEXT PRIMARY KEY
);

CREATE TABLE project_statuses (
    name TEXT PRIMARY KEY
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    -- Unique among ACTIVE projects only, via the partial index in indexes.sql.
    -- A global UNIQUE would make a completed `clisteno` own the name forever.
    name TEXT NOT NULL,
    description TEXT,
    -- A name in project_kinds, checked by the rule that writes it.
    kind TEXT NOT NULL DEFAULT 'life',
    -- active | someday | completed | dropped. A project is a finite effort, so
    -- completing it is what hides it — there is no separate archived flag the way
    -- items have one. `dropped` requires a reason, which is what stops it reading
    -- as deferred. A name in project_statuses, checked by the rule that writes it.
    status TEXT NOT NULL DEFAULT 'active',
    status_reason TEXT,
    closed_at TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE project_items (
    id TEXT PRIMARY KEY,
    -- The handle: short enough to type, unlike the UUID key. Assigned as each
    -- item is created. Null on an item from before the column, and on one a
    -- database once kept as a copy of an API created before the API numbered it.
    number INTEGER,
    title TEXT NOT NULL,
    notes TEXT,
    -- Repo registry name. Null for the many items that are not repo work at
    -- all — home projects, things to sell, errands.
    repo TEXT,
    completed INTEGER NOT NULL DEFAULT 0,
    -- Null where the completion time is unknown: never finished, or finished
    -- before this column existed.
    completed_at TEXT,
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
    completed_at TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE undo_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    previous_state TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    actor TEXT NOT NULL DEFAULT '',
    client TEXT NOT NULL DEFAULT ''
);
