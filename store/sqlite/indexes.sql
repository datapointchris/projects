-- Applied unconditionally on every Open. IF NOT EXISTS keeps it idempotent, so
-- fresh and pre-existing databases converge without a version counter.
-- Kept out of schema.sql because that file is only applied to fresh databases.

-- The (item_id, project_id) primary key already indexes item_id lookups, and
-- this composite serves bare project_id lookups as a leftmost prefix.
CREATE INDEX IF NOT EXISTS idx_memberships_position ON project_item_memberships(project_id, position);

CREATE INDEX IF NOT EXISTS idx_items_active ON project_items(archived) WHERE archived = 0;

-- The (item_id, depends_on_id) primary key already indexes item_id lookups.
CREATE INDEX IF NOT EXISTS idx_deps_depends ON project_item_dependencies(depends_on_id);

CREATE INDEX IF NOT EXISTS idx_tasks_item ON project_item_tasks(item_id);

CREATE INDEX IF NOT EXISTS idx_undo_recent ON undo_log(created_at DESC);

-- SQLite allows any number of NULLs in a unique index, which is what lets an
-- item wait for its number without colliding with every other item doing the
-- same. Declared here rather than in schema.sql because ALTER TABLE ADD COLUMN
-- cannot carry a UNIQUE constraint, so this is the only form that reaches an
-- existing database too.
CREATE UNIQUE INDEX IF NOT EXISTS idx_items_number ON project_items(number);

-- Partial, so a closed project stops owning its name and the next effort by
-- that name can exist. Mirrors uq_projects_name_active on the API side, and is
-- what lets a pull carrying a done `clisteno` beside a live one land at all.
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_name_active ON projects(name) WHERE status = 'active';
