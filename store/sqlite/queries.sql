-- Terminal projects are hidden unless asked for: closing a project is what takes
-- it out of the list, so an unfiltered default would make completing one do
-- nothing visible. Named parameter so the filter reads as a status rather than
-- a bare `?`, and so sqlfluff sees a bind rather than an unqualified column.
--
-- The completed split rides along with the total rather than being a second
-- query: the pane draws both numbers on every row, so asking twice would mean a
-- query per project.
--
-- The order is active, then someday, then closed. A someday project has no
-- closed_at, so ordering on closed_at alone would mix it in among the active
-- ones.
-- name: ListProjectsWithItemCount :many
SELECT
    p.*,
    CAST(COALESCE(SUM(CASE WHEN pi.completed = 1 THEN 1 ELSE 0 END), 0) AS INTEGER)
        AS completed_count,
    COUNT(pi.id) AS item_count
FROM projects p
LEFT JOIN project_item_memberships m ON p.id = m.project_id
LEFT JOIN project_items pi ON m.item_id = pi.id AND pi.archived = 0
WHERE CAST(@status_filter AS TEXT) = 'all' OR p.status = CAST(@status_filter AS TEXT)
GROUP BY p.id
ORDER BY
    CASE p.status WHEN 'active' THEN 0 WHEN 'someday' THEN 1 ELSE 2 END,
    p.closed_at DESC, p.position ASC, p.name ASC;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: GetProjectWithItemCount :one
SELECT
    p.*,
    CAST(COALESCE(SUM(CASE WHEN pi.completed = 1 THEN 1 ELSE 0 END), 0) AS INTEGER)
        AS completed_count,
    COUNT(pi.id) AS item_count
FROM projects p
LEFT JOIN project_item_memberships m ON p.id = m.project_id
LEFT JOIN project_items pi ON m.item_id = pi.id AND pi.archived = 0
WHERE p.id = ?
GROUP BY p.id;

-- name: CreateProject :one
INSERT INTO projects (id, name, description, position)
VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM projects))
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET name = ?,
    description = ?,
    position = ?
WHERE id = ?
RETURNING *;

-- Every consequence of the transition in one statement: closed_at is stamped on
-- the way out and cleared on the way back, and the reason goes with it, so a
-- caller cannot leave an active project carrying a closing date.
-- name: SetProjectStatus :one
UPDATE projects
SET status = sqlc.arg(new_status),
    status_reason = CASE WHEN sqlc.arg(new_status) = 'active' THEN NULL ELSE sqlc.narg(reason) END,
    closed_at = CASE
        WHEN sqlc.arg(new_status) = 'active' THEN NULL
        -- A someday project is set aside, not closed, so it records no close.
        WHEN sqlc.arg(new_status) = 'someday' THEN NULL
        -- Already closed and staying that way: keep the original date rather
        -- than restamping it on an edit to the reason.
        WHEN closed_at IS NOT NULL AND status = sqlc.arg(new_status) THEN closed_at
        ELSE strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- name: UpdateProjectPosition :exec
UPDATE projects SET position = ? WHERE id = ?;

-- name: ListItemsByProject :many
SELECT pi.*, m.position AS membership_position,
    (SELECT COUNT(*) FROM project_item_memberships m2 WHERE m2.item_id = pi.id) AS project_count
FROM project_items pi
INNER JOIN project_item_memberships m ON pi.id = m.item_id
WHERE m.project_id = ? AND pi.archived = 0
ORDER BY m.position, pi.created_at;

-- name: GetItem :one
SELECT * FROM project_items WHERE id = ?;

-- name: CreateItem :one
INSERT INTO project_items (id, number, title, notes, repo)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- Only used when sync is off and this database assigns its own numbers. With
-- sync on the server is the authority and this would hand out a number it is
-- about to disagree with.
-- name: NextItemNumber :one
SELECT COALESCE(MAX(number), 0) + 1 FROM project_items;

-- name: SetItemNumber :exec
UPDATE project_items SET number = ? WHERE id = ?;

-- name: UpdateItem :one
UPDATE project_items
SET title = ?,
    notes = ?,
    repo = ?,
    completed = ?,
    archived = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
RETURNING *;

-- name: DeleteItem :exec
DELETE FROM project_items WHERE id = ?;

-- name: AddItemToProject :exec
INSERT INTO project_item_memberships (item_id, project_id, position)
VALUES (?, ?, (SELECT COALESCE(MAX(m.position), 0) + 1 FROM project_item_memberships m WHERE m.project_id = ?));

-- name: RemoveItemFromProject :exec
DELETE FROM project_item_memberships WHERE item_id = ? AND project_id = ?;

-- name: GetItemProjects :many
SELECT p.*
FROM projects p
INNER JOIN project_item_memberships m ON p.id = m.project_id
WHERE m.item_id = ?
ORDER BY p.name;

-- name: UpdateItemPosition :exec
UPDATE project_item_memberships SET position = ? WHERE item_id = ? AND project_id = ?;

-- Undo of a delete puts back rows whose counterpart may itself have been
-- deleted since. OR IGNORE drops those instead of failing the whole undo.
-- name: RestoreMembership :exec
INSERT OR IGNORE INTO project_item_memberships (item_id, project_id, position)
VALUES (?, ?, ?);

-- name: GetMembership :one
SELECT * FROM project_item_memberships WHERE item_id = ? AND project_id = ?;

-- name: GetItemMemberships :many
SELECT * FROM project_item_memberships WHERE item_id = ?;

-- name: GetProjectMemberships :many
SELECT * FROM project_item_memberships WHERE project_id = ?;

-- name: AddDependency :exec
INSERT INTO project_item_dependencies (item_id, depends_on_id) VALUES (?, ?);

-- name: RemoveDependency :exec
DELETE FROM project_item_dependencies WHERE item_id = ? AND depends_on_id = ?;

-- name: GetBlockers :many
SELECT pi.*
FROM project_items pi
INNER JOIN project_item_dependencies d ON pi.id = d.depends_on_id
WHERE d.item_id = ? AND pi.completed = 0;

-- name: GetDependencyIDs :many
SELECT depends_on_id FROM project_item_dependencies WHERE item_id = ?;

-- name: GetAllDependencies :many
SELECT * FROM project_item_dependencies;

-- Both directions: deleting an item drops the rows where it is the dependent
-- and the rows where it is the blocker.
-- name: GetDependenciesInvolvingItem :many
SELECT * FROM project_item_dependencies WHERE item_id = ? OR depends_on_id = ?;

-- name: SearchItems :many
SELECT * FROM project_items
WHERE (title LIKE '%' || ? || '%' OR notes LIKE '%' || ? || '%')
  AND archived = 0
ORDER BY created_at DESC;

-- name: ListBlockedItems :many
SELECT DISTINCT pi.*
FROM project_items pi
INNER JOIN project_item_dependencies d ON pi.id = d.item_id
INNER JOIN project_items blocker ON d.depends_on_id = blocker.id
WHERE blocker.completed = 0
  AND pi.completed = 0
  AND pi.archived = 0;

-- name: ListArchivedItems :many
SELECT pi.*, m.position AS membership_position
FROM project_items pi
INNER JOIN project_item_memberships m ON pi.id = m.item_id
WHERE m.project_id = ? AND pi.archived = 1
ORDER BY pi.updated_at DESC;

-- name: ListTasksByItem :many
SELECT * FROM project_item_tasks
WHERE item_id = ?
ORDER BY position, created_at;

-- name: GetTask :one
SELECT * FROM project_item_tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO project_item_tasks (id, item_id, title, position)
VALUES (?, ?, ?, (SELECT COALESCE(MAX(t.position), 0) + 1 FROM project_item_tasks t WHERE t.item_id = ?))
RETURNING *;

-- name: UpdateTask :one
UPDATE project_item_tasks
SET title = ?,
    completed = ?,
    position = ?
WHERE id = ?
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM project_item_tasks WHERE id = ?;

-- Sync: pending operations

-- name: InsertPendingSync :exec
INSERT INTO pending_sync (operation, entity_type, entity_id, payload)
VALUES (?, ?, ?, ?);

-- name: ListPendingSync :many
SELECT * FROM pending_sync ORDER BY id ASC;

-- name: GetOldestPendingSync :one
SELECT * FROM pending_sync ORDER BY id ASC LIMIT 1;

-- name: DeletePendingSync :exec
DELETE FROM pending_sync WHERE id = ?;

-- name: DeletePendingSyncByEntity :execrows
DELETE FROM pending_sync WHERE entity_id = ?;

-- name: UpdatePendingSyncError :exec
UPDATE pending_sync SET attempts = attempts + 1, last_error = ? WHERE id = ?;

-- name: CountPendingSync :one
SELECT COUNT(*) FROM pending_sync;

-- name: DeleteAllPendingSync :exec
DELETE FROM pending_sync;

-- name: MaxPendingSyncID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) AS max_id FROM pending_sync;

-- name: DeletePendingSyncUpTo :exec
DELETE FROM pending_sync WHERE id <= ?;

-- name: ListPendingSyncEntityIDsAfter :many
SELECT DISTINCT entity_id FROM pending_sync WHERE id > ?;

-- Sync: state tracking

-- Counted rather than listed: the caller only needs to know whether this
-- database has anything to lose.
-- name: CountProjects :one
SELECT COUNT(*) FROM projects;

-- name: CountItems :one
SELECT COUNT(*) FROM project_items;

-- Whether this database has ever reconciled with an API. A database that has
-- pulled was bound to something, even if nothing recorded what.
-- name: HasPulledBefore :one
SELECT COUNT(*) FROM sync_state WHERE last_pull_at > '1970-01-01T00:00:00.000Z';

-- name: GetSyncOrigin :one
SELECT api_url FROM sync_origin WHERE id = 1;

-- Adoption is deliberate and always overwrites: the caller has already decided
-- this database belongs to this API.
-- name: SetSyncOrigin :exec
INSERT INTO sync_origin (id, api_url, adopted_at)
VALUES (1, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ON CONFLICT(id) DO UPDATE SET
    api_url = excluded.api_url,
    adopted_at = excluded.adopted_at;

-- name: UpsertSyncState :exec
INSERT INTO sync_state (entity_type, last_pull_at, last_push_at)
VALUES (?, ?, ?)
ON CONFLICT(entity_type) DO UPDATE SET
    last_pull_at = CASE WHEN excluded.last_pull_at > sync_state.last_pull_at THEN excluded.last_pull_at ELSE sync_state.last_pull_at END,
    last_push_at = CASE WHEN excluded.last_push_at > sync_state.last_push_at THEN excluded.last_push_at ELSE sync_state.last_push_at END;

-- Sync: pull reconciliation (upserts)

-- name: UpsertProject :exec
INSERT INTO projects (id, name, description, status, status_reason, closed_at, position, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    description = excluded.description,
    status = excluded.status,
    status_reason = excluded.status_reason,
    closed_at = excluded.closed_at,
    position = excluded.position;

-- name: UpsertItem :exec
INSERT INTO project_items (id, number, title, notes, repo, completed, archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    number = excluded.number,
    title = excluded.title,
    notes = excluded.notes,
    repo = excluded.repo,
    completed = excluded.completed,
    archived = excluded.archived,
    updated_at = excluded.updated_at;

-- name: UpsertTask :exec
INSERT INTO project_item_tasks (id, item_id, title, completed, position, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title = excluded.title,
    completed = excluded.completed,
    position = excluded.position;

-- name: UpsertMembership :exec
INSERT INTO project_item_memberships (item_id, project_id, position)
VALUES (?, ?, ?)
ON CONFLICT(item_id, project_id) DO UPDATE SET
    position = excluded.position;

-- name: UpsertDependency :exec
INSERT OR IGNORE INTO project_item_dependencies (item_id, depends_on_id)
VALUES (?, ?);

-- name: DeleteAllMemberships :exec
DELETE FROM project_item_memberships;

-- name: DeleteAllDependencies :exec
DELETE FROM project_item_dependencies;

-- name: ListAllProjectsRaw :many
SELECT * FROM projects ORDER BY position, name;

-- name: ListAllItemsRaw :many
SELECT * FROM project_items ORDER BY created_at DESC;

-- name: ListAllMemberships :many
SELECT * FROM project_item_memberships;

-- name: ListAllTasks :many
SELECT * FROM project_item_tasks ORDER BY item_id, position;

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
