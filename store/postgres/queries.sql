-- The write lock is the analog of beginning a SQLite transaction immediate: a rule
-- reads and then writes, and what it read has to hold at commit.
-- name: TakeWriteLock :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- The third argument makes each setting end with this transaction.
-- name: BindWriter :exec
SELECT
    set_config('projects.actor', sqlc.arg(actor)::text, true),
    set_config('projects.request_id', sqlc.arg(request_id)::text, true),
    set_config('projects.job_run_id', sqlc.arg(job_run_id)::text, true),
    set_config('projects.traceparent', sqlc.arg(traceparent)::text, true),
    set_config('projects.baggage', sqlc.arg(baggage)::text, true);

-- name: ListProjects :many
SELECT * FROM projects ORDER BY position, name;

-- name: ListProjectItemFacts :many
SELECT m.project_id, pi.completed, pi.archived, pi.repo
FROM project_item_memberships AS m
INNER JOIN project_items AS pi ON m.item_id = pi.id;

-- name: ListProjectKinds :many
SELECT name FROM project_kinds ORDER BY name;

-- name: ListProjectStatuses :many
SELECT name FROM project_statuses ORDER BY name;

-- name: GetProject :one
SELECT * FROM projects WHERE id = sqlc.arg(id);

-- name: CreateProject :one
INSERT INTO projects (id, name, description, kind, status, status_reason, closed_at, position)
VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.narg(description), sqlc.arg(kind),
    sqlc.arg(status), sqlc.narg(status_reason), sqlc.narg(closed_at), sqlc.arg(project_position)
)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET
    name = sqlc.arg(name),
    description = sqlc.narg(description),
    kind = sqlc.arg(kind),
    status = sqlc.arg(status),
    status_reason = sqlc.narg(status_reason),
    closed_at = sqlc.narg(closed_at),
    position = sqlc.arg(project_position)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpdateProjectPosition :exec
UPDATE projects SET position = sqlc.arg(project_position) WHERE id = sqlc.arg(id);

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = sqlc.arg(id);

-- name: RestoreProject :exec
INSERT INTO projects (id, name, description, kind, status, status_reason, closed_at, position, created_at)
VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.narg(description), sqlc.arg(kind),
    sqlc.arg(status), sqlc.narg(status_reason), sqlc.narg(closed_at), sqlc.arg(project_position), sqlc.arg(created_at)
)
ON CONFLICT (id) DO UPDATE SET
    name = excluded.name,
    description = excluded.description,
    kind = excluded.kind,
    status = excluded.status,
    status_reason = excluded.status_reason,
    closed_at = excluded.closed_at,
    position = excluded.position;

-- name: ListAllItems :many
SELECT * FROM project_items ORDER BY created_at DESC, id ASC;

-- name: ListItemsInProject :many
SELECT
    pi.*,
    m.position AS membership_position,
    (SELECT count(*) FROM project_item_memberships AS held WHERE held.item_id = pi.id) AS project_count
FROM project_items AS pi
INNER JOIN project_item_memberships AS m ON pi.id = m.item_id
WHERE m.project_id = sqlc.arg(project_id)
ORDER BY m.position ASC, pi.created_at ASC, pi.id ASC;

-- name: ListBlockedItems :many
SELECT pi.*
FROM project_items AS pi
WHERE
    pi.completed = false
    AND pi.archived = false
    AND EXISTS (
        SELECT 1
        FROM project_item_dependencies AS d
        INNER JOIN project_items AS blocker ON d.depends_on_id = blocker.id
        WHERE d.item_id = pi.id AND blocker.completed = false
    )
ORDER BY pi.created_at DESC, pi.id ASC;

-- name: SearchItems :many
SELECT *
FROM project_items
WHERE title ILIKE '%' || sqlc.arg(query)::text || '%' OR notes ILIKE '%' || sqlc.arg(query)::text || '%'
ORDER BY created_at DESC, id ASC;

-- name: GetItem :one
SELECT * FROM project_items WHERE id = sqlc.arg(id);

-- name: GetItemByNumber :one
SELECT * FROM project_items WHERE number = sqlc.arg(item_number);

-- name: NextItemNumber :one
SELECT nextval('item_numbers')::bigint;

-- name: CreateItem :one
INSERT INTO project_items (id, number, title, notes, repo)
VALUES (sqlc.arg(id), sqlc.arg(item_number), sqlc.arg(title), sqlc.narg(notes), sqlc.narg(repo))
RETURNING *;

-- name: UpdateItem :one
UPDATE project_items
SET
    title = sqlc.arg(title),
    notes = sqlc.narg(notes),
    repo = sqlc.narg(repo),
    completed = sqlc.arg(completed),
    completed_at = sqlc.narg(completed_at),
    archived = sqlc.arg(archived),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteItem :exec
DELETE FROM project_items WHERE id = sqlc.arg(id);

-- name: RestoreItem :exec
INSERT INTO project_items (id, number, title, notes, repo, completed, completed_at, archived, created_at, updated_at)
VALUES (
    sqlc.arg(id), sqlc.arg(item_number), sqlc.arg(title), sqlc.narg(notes), sqlc.narg(repo),
    sqlc.arg(completed), sqlc.narg(completed_at), sqlc.arg(archived), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (id) DO UPDATE SET
    number = excluded.number,
    title = excluded.title,
    notes = excluded.notes,
    repo = excluded.repo,
    completed = excluded.completed,
    completed_at = excluded.completed_at,
    archived = excluded.archived,
    updated_at = excluded.updated_at;

-- name: ListAllMemberships :many
SELECT * FROM project_item_memberships ORDER BY project_id, item_id;

-- name: ListItemMemberships :many
SELECT * FROM project_item_memberships WHERE item_id = sqlc.arg(item_id) ORDER BY project_id;

-- name: ListProjectMemberships :many
SELECT * FROM project_item_memberships WHERE project_id = sqlc.arg(project_id) ORDER BY position, item_id;

-- name: GetMembership :one
SELECT * FROM project_item_memberships WHERE item_id = sqlc.arg(item_id) AND project_id = sqlc.arg(project_id);

-- name: GetItemProjects :many
SELECT p.*
FROM projects AS p
INNER JOIN project_item_memberships AS m ON p.id = m.project_id
WHERE m.item_id = sqlc.arg(item_id)
ORDER BY p.position, p.created_at, p.id;

-- name: AddItemToProject :exec
INSERT INTO project_item_memberships (item_id, project_id, position)
VALUES (sqlc.arg(item_id), sqlc.arg(project_id), sqlc.arg(item_position));

-- name: RemoveItemFromProject :exec
DELETE FROM project_item_memberships WHERE item_id = sqlc.arg(item_id) AND project_id = sqlc.arg(project_id);

-- name: UpdateItemPosition :exec
UPDATE project_item_memberships
SET position = sqlc.arg(item_position)
WHERE item_id = sqlc.arg(item_id) AND project_id = sqlc.arg(project_id);

-- name: RestoreMembership :exec
INSERT INTO project_item_memberships (item_id, project_id, position)
VALUES (sqlc.arg(item_id), sqlc.arg(project_id), sqlc.arg(item_position))
ON CONFLICT (item_id, project_id) DO NOTHING;

-- name: ListAllDependencies :many
SELECT * FROM project_item_dependencies ORDER BY item_id, depends_on_id;

-- name: ListDependenciesInvolvingItem :many
SELECT *
FROM project_item_dependencies
WHERE item_id = sqlc.arg(item_id) OR depends_on_id = sqlc.arg(item_id)
ORDER BY item_id, depends_on_id;

-- name: ListDependencyItems :many
SELECT pi.*
FROM project_items AS pi
INNER JOIN project_item_dependencies AS d ON pi.id = d.depends_on_id
WHERE d.item_id = sqlc.arg(item_id)
ORDER BY pi.number;

-- name: AddDependency :exec
INSERT INTO project_item_dependencies (item_id, depends_on_id)
VALUES (sqlc.arg(item_id), sqlc.arg(depends_on_id));

-- name: RemoveDependency :exec
DELETE FROM project_item_dependencies WHERE item_id = sqlc.arg(item_id) AND depends_on_id = sqlc.arg(depends_on_id);

-- name: RestoreDependency :exec
INSERT INTO project_item_dependencies (item_id, depends_on_id)
VALUES (sqlc.arg(item_id), sqlc.arg(depends_on_id))
ON CONFLICT (item_id, depends_on_id) DO NOTHING;

-- name: ListAllTasks :many
SELECT * FROM project_item_tasks ORDER BY item_id, position, created_at;

-- name: ListTasksByItem :many
SELECT * FROM project_item_tasks WHERE item_id = sqlc.arg(item_id) ORDER BY position, created_at;

-- name: GetTask :one
SELECT * FROM project_item_tasks WHERE id = sqlc.arg(id);

-- name: CreateTask :one
INSERT INTO project_item_tasks (id, item_id, title, position)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(title), sqlc.arg(task_position))
RETURNING *;

-- name: UpdateTask :one
UPDATE project_item_tasks
SET
    title = sqlc.arg(title),
    completed = sqlc.arg(completed),
    completed_at = sqlc.narg(completed_at),
    position = sqlc.arg(task_position)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM project_item_tasks WHERE id = sqlc.arg(id);

-- name: RestoreTask :exec
INSERT INTO project_item_tasks (id, item_id, title, completed, completed_at, position, created_at)
VALUES (
    sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(title), sqlc.arg(completed),
    sqlc.narg(completed_at), sqlc.arg(task_position), sqlc.arg(created_at)
)
ON CONFLICT (id) DO UPDATE SET
    title = excluded.title,
    completed = excluded.completed,
    completed_at = excluded.completed_at,
    position = excluded.position;

-- name: InsertUndoEntry :exec
INSERT INTO undo_log (action, entity_type, entity_id, actor, client, previous_state)
VALUES (
    sqlc.arg(action), sqlc.arg(entity_type), sqlc.arg(entity_id),
    sqlc.arg(actor), sqlc.arg(client), sqlc.narg(previous_state)
);

-- name: GetLatestUndoEntry :one
SELECT * FROM undo_log
WHERE actor = sqlc.arg(actor) AND client = sqlc.arg(client)
ORDER BY id DESC LIMIT 1;

-- name: ListUndoEntriesAfter :many
SELECT * FROM undo_log WHERE id > sqlc.arg(id) ORDER BY id;

-- name: DeleteUndoEntry :exec
DELETE FROM undo_log WHERE id = sqlc.arg(id);

-- name: CountUndoEntries :one
SELECT count(*) FROM undo_log WHERE actor = sqlc.arg(actor) AND client = sqlc.arg(client);
