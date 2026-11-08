-- name: ListProjectKinds :many
SELECT name FROM project_kinds ORDER BY name;

-- name: ListProjectStatuses :many
SELECT name FROM project_statuses ORDER BY name;

-- One row per membership, which the rule adding them up reads whole.
-- name: ListProjectItemFacts :many
SELECT m.project_id, pi.completed, pi.archived, pi.repo
FROM project_item_memberships m
INNER JOIN project_items pi ON m.item_id = pi.id;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: CreateProject :one
INSERT INTO projects (id, name, description, kind, status, status_reason, closed_at, position)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET name = ?,
    description = ?,
    kind = ?,
    status = ?,
    status_reason = ?,
    closed_at = ?,
    position = ?
WHERE id = ?
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: UpdateProjectPosition :exec
UPDATE projects SET position = ? WHERE id = ?;

-- name: ListItemsInProject :many
SELECT pi.*, m.position AS membership_position,
    (SELECT COUNT(*) FROM project_item_memberships m2 WHERE m2.item_id = pi.id) AS project_count
FROM project_items pi
INNER JOIN project_item_memberships m ON pi.id = m.item_id
WHERE m.project_id = ?
ORDER BY m.position, pi.created_at, pi.id;

-- name: GetItem :one
SELECT * FROM project_items WHERE id = ?;

-- name: GetItemByNumber :one
SELECT * FROM project_items WHERE number = ?;

-- name: CreateItem :one
INSERT INTO project_items (id, number, title, notes, repo)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: NextItemNumber :one
SELECT COALESCE(MAX(number), 0) + 1 FROM project_items;

-- name: UpdateItem :one
UPDATE project_items
SET title = ?,
    notes = ?,
    repo = ?,
    completed = ?,
    completed_at = ?,
    archived = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
RETURNING *;

-- name: DeleteItem :exec
DELETE FROM project_items WHERE id = ?;

-- name: AddItemToProject :exec
INSERT INTO project_item_memberships (item_id, project_id, position)
VALUES (?, ?, ?);

-- name: RemoveItemFromProject :exec
DELETE FROM project_item_memberships WHERE item_id = ? AND project_id = ?;

-- name: GetItemProjects :many
SELECT p.*
FROM projects p
INNER JOIN project_item_memberships m ON p.id = m.project_id
WHERE m.item_id = ?
ORDER BY p.position, p.created_at, p.id;

-- name: UpdateItemPosition :exec
UPDATE project_item_memberships SET position = ? WHERE item_id = ? AND project_id = ?;

-- Undo of a delete puts back rows that may already be there, and OR IGNORE leaves
-- those alone. It does not cover a foreign key: a counterpart gone since fails
-- the undo.
-- name: RestoreMembership :exec
INSERT OR IGNORE INTO project_item_memberships (item_id, project_id, position)
VALUES (?, ?, ?);

-- name: GetMembership :one
SELECT * FROM project_item_memberships WHERE item_id = ? AND project_id = ?;

-- name: GetItemMemberships :many
SELECT * FROM project_item_memberships WHERE item_id = ? ORDER BY project_id;

-- name: GetProjectMemberships :many
SELECT * FROM project_item_memberships WHERE project_id = ? ORDER BY position, item_id;

-- name: AddDependency :exec
INSERT INTO project_item_dependencies (item_id, depends_on_id) VALUES (?, ?);

-- name: RemoveDependency :exec
DELETE FROM project_item_dependencies WHERE item_id = ? AND depends_on_id = ?;

-- name: ListDependencyItems :many
SELECT pi.*
FROM project_items pi
INNER JOIN project_item_dependencies d ON pi.id = d.depends_on_id
WHERE d.item_id = ?
ORDER BY pi.number, pi.id;

-- name: GetAllDependencies :many
SELECT * FROM project_item_dependencies ORDER BY item_id, depends_on_id;

-- Both directions: deleting an item drops the rows where it is the dependent
-- and the rows where it is the blocker.
-- name: GetDependenciesInvolvingItem :many
SELECT * FROM project_item_dependencies
WHERE item_id = ? OR depends_on_id = ?
ORDER BY item_id, depends_on_id;

-- name: SearchItems :many
SELECT * FROM project_items
WHERE title LIKE '%' || ? || '%' OR notes LIKE '%' || ? || '%'
ORDER BY created_at DESC, id ASC;

-- name: ListBlockedItems :many
SELECT DISTINCT pi.*
FROM project_items pi
INNER JOIN project_item_dependencies d ON pi.id = d.item_id
INNER JOIN project_items blocker ON d.depends_on_id = blocker.id
WHERE blocker.completed = 0
  AND pi.completed = 0
  AND pi.archived = 0
ORDER BY pi.created_at DESC, pi.id ASC;

-- name: ListTasksByItem :many
SELECT * FROM project_item_tasks
WHERE item_id = ?
ORDER BY position, created_at;

-- name: GetTask :one
SELECT * FROM project_item_tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO project_item_tasks (id, item_id, title, position)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: UpdateTask :one
UPDATE project_item_tasks
SET title = ?,
    completed = ?,
    completed_at = ?,
    position = ?
WHERE id = ?
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM project_item_tasks WHERE id = ?;

-- Undo: a restore inserts the whole row, or, where it is still there, rewrites what an edit can change.

-- name: RestoreProject :exec
INSERT INTO projects (id, name, description, kind, status, status_reason, closed_at, position, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    description = excluded.description,
    kind = excluded.kind,
    status = excluded.status,
    status_reason = excluded.status_reason,
    closed_at = excluded.closed_at,
    position = excluded.position;

-- name: RestoreItem :exec
INSERT INTO project_items (id, number, title, notes, repo, completed, completed_at, archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    number = excluded.number,
    title = excluded.title,
    notes = excluded.notes,
    repo = excluded.repo,
    completed = excluded.completed,
    completed_at = excluded.completed_at,
    archived = excluded.archived,
    updated_at = excluded.updated_at;

-- name: RestoreTask :exec
INSERT INTO project_item_tasks (id, item_id, title, completed, completed_at, position, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title = excluded.title,
    completed = excluded.completed,
    completed_at = excluded.completed_at,
    position = excluded.position;

-- name: UpsertDependency :exec
INSERT OR IGNORE INTO project_item_dependencies (item_id, depends_on_id)
VALUES (?, ?);

-- name: ListAllProjectsRaw :many
SELECT * FROM projects ORDER BY position, name;

-- name: ListAllItemsRaw :many
SELECT * FROM project_items ORDER BY created_at DESC;

-- name: ListAllMemberships :many
SELECT * FROM project_item_memberships ORDER BY project_id, item_id;

-- name: ListAllTasks :many
SELECT * FROM project_item_tasks ORDER BY item_id, position, created_at;

-- Undo

-- name: InsertUndoLog :exec
INSERT INTO undo_log (action, entity_type, entity_id, previous_state)
VALUES (?, ?, ?, ?);

-- name: GetLatestUndoLog :one
SELECT * FROM undo_log ORDER BY id DESC LIMIT 1;

-- name: DeleteUndoLog :exec
DELETE FROM undo_log WHERE id = ?;

-- name: CountUndoLogs :one
SELECT COUNT(*) FROM undo_log;
