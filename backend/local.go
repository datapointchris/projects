package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/graph"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/store/sqlite/generated"
)

// LocalBackend provides direct SQLite access for local mode.
type LocalBackend struct {
	db *sql.DB
	q  *generated.Queries
	// assignsItemNumbers is set when nothing upstream will hand one out, which
	// is exactly the sync-disabled case. See AssigningItemNumbers.
	assignsItemNumbers bool
	// repoRegistryPath enables the repo-named-project ban. Empty means no
	// registry and no ban. See RefusingRepoNames.
	repoRegistryPath string
}

// LocalOption configures a LocalBackend at construction.
type LocalOption func(*LocalBackend)

// AssigningItemNumbers makes this database the authority for item numbers.
//
// Pass it when sync is off — the work machine, which is never allowed to reach
// the ichrisbirch API, so its database is a disjoint universe that no server
// will ever number. With sync on the server assigns instead, because a number
// guessed locally and a number assigned upstream would disagree and the handle
// would change under a user who had already written it down.
func AssigningItemNumbers() LocalOption {
	return func(b *LocalBackend) { b.assignsItemNumbers = true }
}

// RefusingRepoNames bans naming a project after a repo, reading the registry at
// path on the create and rename paths only.
//
// A project name must be bounded work — something that ends — and a repo does
// not end, so a project named after one silently becomes the eternal bucket the
// next papercut falls into. The repo association is an item's repo tag, which
// already crosses project boundaries and outlives any single project.
//
// It lives on the backend because every create and rename passes through here,
// whichever surface asked for it. Loaded lazily, so a session that never names
// a project does not pay to read the registry.
func RefusingRepoNames(path string) LocalOption {
	return func(b *LocalBackend) { b.repoRegistryPath = path }
}

// NewLocalBackend creates a backend that operates directly on a local SQLite database.
func NewLocalBackend(db *sql.DB, opts ...LocalOption) *LocalBackend {
	b := &LocalBackend{db: db, q: generated.New(db)}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Compile-time check that LocalBackend implements Backend.
var _ Backend = (*LocalBackend)(nil)

func (b *LocalBackend) ctx() context.Context {
	return context.Background()
}

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// --- Projects ---

// refuseRepoName rejects a project name the repo registry knows. A missing
// registry bans nothing: refusing to create a project on a machine without one
// is worse than a project named after a repo.
func (b *LocalBackend) refuseRepoName(name string) error {
	if b.repoRegistryPath == "" {
		return nil
	}
	registry, err := repos.Load(b.repoRegistryPath)
	if err != nil {
		return err
	}
	if registry.Knows(name) {
		return fmt.Errorf("%w: %q names a repo, which never ends — name the bounded work instead", model.ErrRepoNamedProject, name)
	}
	return nil
}

// ListProjectsByStatus takes a status name or model.StatusAll.
func (b *LocalBackend) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	rows, err := b.q.ListProjectsWithItemCount(b.ctx(), status)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	out := make([]model.ProjectWithItemCount, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectWithItemCount(r)
	}
	return out, nil
}

func (b *LocalBackend) GetProject(id string) (*model.ProjectWithItemCount, error) {
	row, err := b.q.GetProjectWithItemCount(b.ctx(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project: %w", err)
	}
	result := toModelProjectWithItemCountFromGet(row)
	return &result, nil
}

func (b *LocalBackend) CreateProject(input model.CreateProject) (*model.Project, error) {
	ctx := b.ctx()
	if err := b.refuseRepoName(input.Name); err != nil {
		return nil, err
	}
	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning create project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := b.q.WithTx(tx)

	p, err := qtx.CreateProject(ctx, generated.CreateProjectParams{
		ID:          newID(),
		Name:        input.Name,
		Description: toNullString(input.Description),
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, model.ErrDuplicateName
		}
		return nil, fmt.Errorf("creating project: %w", err)
	}
	if err := b.logUndo(qtx, "create", "project", p.ID, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing create project: %w", err)
	}
	result := toModelProject(p)
	return &result, nil
}

func (b *LocalBackend) UpdateProject(id string, input model.UpdateProject) (*model.Project, error) {
	ctx := b.ctx()

	current, err := b.q.GetProject(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project for update: %w", err)
	}

	params := generated.UpdateProjectParams{
		Name:        current.Name,
		Description: current.Description,
		Position:    current.Position,
		ID:          id,
	}
	if input.Name != nil {
		// Checked on rename too: a ban one `edit` walks around is decoration.
		if err := b.refuseRepoName(*input.Name); err != nil {
			return nil, err
		}
		params.Name = *input.Name
	}
	if input.Description != nil {
		params.Description = toNullString(input.Description)
	}
	if input.Position != nil {
		params.Position = int64(*input.Position)
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning update project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := b.q.WithTx(tx)

	p, err := qtx.UpdateProject(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("updating project: %w", err)
	}
	if err := b.logUndo(qtx, "update", "project", id, current); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing update project: %w", err)
	}
	result := toModelProject(p)
	return &result, nil
}

// SetProjectStatus moves a project between active, someday, completed and
// dropped. A reason is required when dropping, optional when shelving and
// ignored when reopening — the closing timestamp and the reason are
// consequences of the transition, derived in the statement rather than handed
// in, so an active project can never carry either.
func (b *LocalBackend) SetProjectStatus(id, status string, reason *string) (*model.Project, error) {
	ctx := b.ctx()
	if status == model.StatusDropped && (reason == nil || strings.TrimSpace(*reason) == "") {
		return nil, model.ErrDropReasonRequired
	}

	current, err := b.q.GetProject(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project for status change: %w", err)
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning project status transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := b.q.WithTx(tx)

	p, err := qtx.SetProjectStatus(ctx, generated.SetProjectStatusParams{
		NewStatus: status,
		Reason:    toNullString(reason),
		ID:        id,
	})
	if err != nil {
		// Reopening into a name a live project has taken. Only active projects
		// hold a name, so this can only fire on the way back to active.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, model.ErrDuplicateName
		}
		return nil, fmt.Errorf("setting project status: %w", err)
	}
	if err := b.logUndo(qtx, "update", "project", id, current); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing project status: %w", err)
	}
	result := toModelProject(p)
	return &result, nil
}

func (b *LocalBackend) DeleteProject(id string) error {
	ctx := b.ctx()
	snapshot, err := captureProject(ctx, b.q, id)
	if err != nil {
		return err
	}

	tx, err := b.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning delete project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := b.q.WithTx(tx)

	if err := qtx.DeleteProject(ctx, id); err != nil {
		return fmt.Errorf("deleting project: %w", err)
	}
	if err := b.logUndo(qtx, "delete", "project", id, snapshot); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing delete project: %w", err)
	}
	return nil
}

func (b *LocalBackend) ReorderProject(projectID string, newPosition int) error {
	ctx := b.ctx()
	current, err := b.q.GetProject(ctx, projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("project %q: %w", projectID, model.ErrNotFound)
		}
		return fmt.Errorf("getting project for reorder: %w", err)
	}

	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{
			ID:       projectID,
			Position: int64(newPosition),
		}); err != nil {
			return fmt.Errorf("reordering project: %w", err)
		}
		return b.logUndo(qtx, "update", "project_position", projectID, current)
	})
}

// --- Items ---

func (b *LocalBackend) ListAllItemsIncludingArchived() ([]model.ProjectItem, error) {
	items, err := b.q.ListAllItemsRaw(b.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing all items: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (b *LocalBackend) ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := b.q.ListItemsByProject(b.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing items by project: %w", err)
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProject(r)
	}
	return out, nil
}

func (b *LocalBackend) GetItem(id string) (*model.ProjectItemDetail, error) {
	pi, err := b.q.GetItem(b.ctx(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item: %w", err)
	}
	result := toModelProjectItem(pi)

	ps, err := b.q.GetItemProjects(b.ctx(), id)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}

	depIDs, err := b.q.GetDependencyIDs(b.ctx(), id)
	if err != nil {
		return nil, fmt.Errorf("getting dependency IDs: %w", err)
	}
	if depIDs == nil {
		depIDs = []string{}
	}

	return &model.ProjectItemDetail{
		ProjectItem:   result,
		Projects:      toModelProjects(ps),
		DependencyIDs: depIDs,
	}, nil
}

func (b *LocalBackend) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	if len(input.ProjectIDs) == 0 {
		return nil, model.ErrLastProject
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := b.q.WithTx(tx)
	ctx := b.ctx()

	number := sql.NullInt64{}
	if b.assignsItemNumbers {
		next, err := qtx.NextItemNumber(ctx)
		if err != nil {
			return nil, fmt.Errorf("allocating item number: %w", err)
		}
		number = sql.NullInt64{Int64: next, Valid: true}
	}

	pi, err := qtx.CreateItem(ctx, generated.CreateItemParams{
		ID:     newID(),
		Number: number,
		Title:  input.Title,
		Notes:  toNullString(input.Notes),
		Repo:   repoToNullString(input.Repo),
	})
	if err != nil {
		return nil, fmt.Errorf("creating item: %w", err)
	}

	for _, pid := range input.ProjectIDs {
		err := qtx.AddItemToProject(ctx, generated.AddItemToProjectParams{
			ItemID:      pi.ID,
			ProjectID:   pid,
			ProjectID_2: pid,
		})
		if err != nil {
			return nil, fmt.Errorf("adding item to project %s: %w", pid, err)
		}
	}

	if err := b.logUndo(qtx, "create", "item", pi.ID, nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return b.GetItem(pi.ID)
}

func (b *LocalBackend) UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	ctx := b.ctx()

	current, err := b.q.GetItem(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item for update: %w", err)
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := b.q.WithTx(tx)

	params := generated.UpdateItemParams{
		Title:     current.Title,
		Notes:     current.Notes,
		Repo:      current.Repo,
		Completed: current.Completed,
		Archived:  current.Archived,
		ID:        id,
	}
	if input.Title != nil {
		params.Title = *input.Title
	}
	if input.Notes != nil {
		params.Notes = toNullString(input.Notes)
	}
	if input.Repo != nil {
		params.Repo = repoToNullString(input.Repo)
	}
	if input.Completed != nil {
		params.Completed = boolToInt64(input.Completed)
	}
	if input.Archived != nil {
		params.Archived = boolToInt64(input.Archived)
	}

	pi, err := qtx.UpdateItem(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("updating item: %w", err)
	}

	if err := b.logUndo(qtx, "update", "item", id, current); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	result := toModelProjectItem(pi)
	return &result, nil
}

func (b *LocalBackend) DeleteItem(id string) error {
	ctx := b.ctx()
	snapshot, err := captureItem(ctx, b.q, id)
	if err != nil {
		return err
	}

	tx, err := b.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := b.q.WithTx(tx)

	if err := b.logUndo(qtx, "delete", "item", id, snapshot); err != nil {
		return err
	}

	if err := qtx.DeleteItem(ctx, id); err != nil {
		return fmt.Errorf("deleting item: %w", err)
	}

	return tx.Commit()
}

func (b *LocalBackend) ReorderItem(itemID, projectID string, newPosition int) error {
	ctx := b.ctx()
	current, err := b.q.GetMembership(ctx, generated.GetMembershipParams{
		ItemID:    itemID,
		ProjectID: projectID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			// The pairing is what is absent, not either id on its own: both can
			// exist while the item is not in that project.
			return fmt.Errorf("item %q is not in project %q: %w", itemID, projectID, model.ErrNotFound)
		}
		return fmt.Errorf("getting membership for reorder: %w", err)
	}

	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
			ItemID:    itemID,
			ProjectID: projectID,
			Position:  int64(newPosition),
		}); err != nil {
			return fmt.Errorf("reordering item: %w", err)
		}
		return b.logUndo(qtx, "update", "item_position", itemID, current)
	})
}

// --- Multi-project membership ---

func (b *LocalBackend) AddToProject(itemID, projectID string) error {
	ctx := b.ctx()
	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.AddItemToProject(ctx, generated.AddItemToProjectParams{
			ItemID:      itemID,
			ProjectID:   projectID,
			ProjectID_2: projectID,
		}); err != nil {
			return fmt.Errorf("adding item to project: %w", err)
		}
		return b.logUndo(qtx, "create", "membership", itemID, generated.ProjectItemMembership{
			ItemID:    itemID,
			ProjectID: projectID,
		})
	})
}

func (b *LocalBackend) RemoveFromProject(itemID, projectID string) error {
	ctx := b.ctx()
	projects, err := b.q.GetItemProjects(ctx, itemID)
	if err != nil {
		return fmt.Errorf("checking project count: %w", err)
	}
	if len(projects) <= 1 {
		return model.ErrLastProject
	}

	current, err := b.q.GetMembership(ctx, generated.GetMembershipParams{
		ItemID:    itemID,
		ProjectID: projectID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("item %q is not in project %q: %w", itemID, projectID, model.ErrNotFound)
		}
		return fmt.Errorf("getting membership for remove: %w", err)
	}

	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{
			ItemID:    itemID,
			ProjectID: projectID,
		}); err != nil {
			return fmt.Errorf("removing item from project: %w", err)
		}
		return b.logUndo(qtx, "delete", "membership", itemID, current)
	})
}

func (b *LocalBackend) GetItemProjects(itemID string) ([]model.Project, error) {
	ps, err := b.q.GetItemProjects(b.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}
	return toModelProjects(ps), nil
}

// --- Dependencies ---

func (b *LocalBackend) AddDependency(itemID, dependsOn string) error {
	ctx := b.ctx()

	deps, err := b.q.GetAllDependencies(ctx)
	if err != nil {
		return fmt.Errorf("getting dependencies for cycle check: %w", err)
	}

	adj := make(map[string][]string)
	for _, d := range deps {
		adj[d.DependsOnID] = append(adj[d.DependsOnID], d.ItemID)
	}

	if graph.WouldCycle(adj, dependsOn, itemID) {
		return model.ErrCyclicDependency
	}

	edge := generated.ProjectItemDependency{ItemID: itemID, DependsOnID: dependsOn}
	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.AddDependency(ctx, generated.AddDependencyParams(edge)); err != nil {
			return fmt.Errorf("adding dependency: %w", err)
		}
		return b.logUndo(qtx, "create", "dependency", itemID, edge)
	})
}

func (b *LocalBackend) RemoveDependency(itemID, dependsOn string) error {
	ctx := b.ctx()
	edge := generated.ProjectItemDependency{ItemID: itemID, DependsOnID: dependsOn}
	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.RemoveDependency(ctx, generated.RemoveDependencyParams(edge)); err != nil {
			return fmt.Errorf("removing dependency: %w", err)
		}
		return b.logUndo(qtx, "delete", "dependency", itemID, edge)
	})
}

func (b *LocalBackend) GetBlockers(itemID string) ([]model.ProjectItem, error) {
	items, err := b.q.GetBlockers(b.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (b *LocalBackend) ListDependencies() ([]model.Dependency, error) {
	rows, err := b.q.GetAllDependencies(b.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing dependencies: %w", err)
	}
	out := make([]model.Dependency, len(rows))
	for i, r := range rows {
		out[i] = model.Dependency{ItemID: r.ItemID, DependsOnID: r.DependsOnID}
	}
	return out, nil
}

func (b *LocalBackend) ListMemberships() ([]model.Membership, error) {
	rows, err := b.q.ListAllMemberships(b.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing memberships: %w", err)
	}
	out := make([]model.Membership, len(rows))
	for i, r := range rows {
		out[i] = model.Membership{ItemID: r.ItemID, ProjectID: r.ProjectID, Position: int(r.Position)}
	}
	return out, nil
}

// --- Tasks ---

func (b *LocalBackend) ListTasks(itemID string) ([]model.ProjectItemTask, error) {
	rows, err := b.q.ListTasksByItem(b.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	out := make([]model.ProjectItemTask, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemTask(r)
	}
	return out, nil
}

func (b *LocalBackend) CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := b.ctx()
	id := newID()

	var created generated.ProjectItemTask
	err := b.inTx(func(qtx *generated.Queries) error {
		t, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
			ID:       id,
			ItemID:   itemID,
			Title:    input.Title,
			ItemID_2: itemID,
		})
		if err != nil {
			return fmt.Errorf("creating task: %w", err)
		}
		created = t
		// The row, not nil: undoing a task create still has to know which item
		// owned it so the reversal can be pushed.
		return b.logUndo(qtx, "create", "task", id, t)
	})
	if err != nil {
		return nil, err
	}

	result := toModelProjectItemTask(created)
	return &result, nil
}

func (b *LocalBackend) UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := b.ctx()

	current, err := b.q.GetTask(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task %q: %w", taskID, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting task for update: %w", err)
	}

	params := generated.UpdateTaskParams{
		Title:     current.Title,
		Completed: current.Completed,
		Position:  current.Position,
		ID:        taskID,
	}
	if input.Title != nil {
		params.Title = *input.Title
	}
	if input.Completed != nil {
		params.Completed = boolToInt64(input.Completed)
	}
	if input.Position != nil {
		params.Position = int64(*input.Position)
	}

	var updated generated.ProjectItemTask
	err = b.inTx(func(qtx *generated.Queries) error {
		t, err := qtx.UpdateTask(ctx, params)
		if err != nil {
			return fmt.Errorf("updating task: %w", err)
		}
		updated = t
		return b.logUndo(qtx, "update", "task", taskID, current)
	})
	if err != nil {
		return nil, err
	}

	result := toModelProjectItemTask(updated)
	return &result, nil
}

func (b *LocalBackend) DeleteTask(itemID, taskID string) error {
	ctx := b.ctx()
	current, err := b.q.GetTask(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("task %q: %w", taskID, model.ErrNotFound)
		}
		return fmt.Errorf("getting task for delete: %w", err)
	}

	return b.inTx(func(qtx *generated.Queries) error {
		if err := qtx.DeleteTask(ctx, taskID); err != nil {
			return fmt.Errorf("deleting task: %w", err)
		}
		return b.logUndo(qtx, "delete", "task", taskID, current)
	})
}

// --- Search ---

func (b *LocalBackend) Search(query string) ([]model.ProjectItem, error) {
	q := sql.NullString{String: query, Valid: true}
	items, err := b.q.SearchItems(b.ctx(), generated.SearchItemsParams{
		Column1: q,
		Column2: q,
	})
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return toModelProjectItems(items), nil
}

// --- Filters ---

func (b *LocalBackend) ListBlocked() ([]model.ProjectItem, error) {
	items, err := b.q.ListBlockedItems(b.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing blocked: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (b *LocalBackend) ListArchived(projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := b.q.ListArchivedItems(b.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing archived: %w", err)
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProjectFromArchived(r)
	}
	return out, nil
}

// --- Undo ---

func (b *LocalBackend) Undo() (*model.UndoResult, error) {
	ctx := b.ctx()
	entry, err := b.q.GetLatestUndoLog(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrNothingToUndo
		}
		return nil, fmt.Errorf("getting undo log: %w", err)
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning undo transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := b.q.WithTx(tx)

	result := &model.UndoResult{
		Description: fmt.Sprintf("undid %s on %s %s", entry.Action, entry.EntityType, shortEntityID(entry.EntityID)),
		Action:      entry.Action,
		EntityType:  entry.EntityType,
		EntityID:    entry.EntityID,
	}

	// Every mutation that writes an undo entry needs a case here. A type that
	// falls through would leave the entry consumed and the change standing,
	// which reads to the user as undo silently doing nothing.
	var undoErr error
	switch entry.EntityType {
	case "project":
		undoErr = undoProject(ctx, qtx, entry, result)
	case "item":
		undoErr = undoItem(ctx, qtx, entry, result)
	case "task":
		undoErr = undoTask(ctx, qtx, entry, result)
	case "membership":
		undoErr = undoMembership(ctx, qtx, entry, result)
	case "dependency":
		undoErr = undoDependency(ctx, qtx, entry, result)
	case "item_position":
		undoErr = undoItemPosition(ctx, qtx, entry, result)
	case "project_position":
		undoErr = undoProjectPosition(ctx, qtx, entry, result)
	default:
		return nil, fmt.Errorf("undoing unknown entity type %q", entry.EntityType)
	}
	if undoErr != nil {
		return nil, undoErr
	}

	if err := qtx.DeleteUndoLog(ctx, entry.ID); err != nil {
		return nil, fmt.Errorf("deleting undo log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing undo: %w", err)
	}

	return result, nil
}

func undoItem(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	switch entry.Action {
	case "create":
		if err := qtx.DeleteItem(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing create: %w", err)
		}
	case "update":
		if entry.PreviousState.Valid {
			var prev generated.ProjectItem
			if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous state: %w", err)
			}
			restored, err := qtx.UpdateItem(ctx, generated.UpdateItemParams{
				Title:     prev.Title,
				Notes:     prev.Notes,
				Repo:      prev.Repo,
				Completed: prev.Completed,
				Archived:  prev.Archived,
				ID:        entry.EntityID,
			})
			if err != nil {
				return fmt.Errorf("restoring previous state: %w", err)
			}
			item := toModelProjectItem(restored)
			result.Restored = &item
		}
	case "delete":
		if entry.PreviousState.Valid {
			snapshot, err := decodeItemSnapshot(entry.PreviousState.String)
			if err != nil {
				return err
			}
			if err := restoreItemSnapshot(ctx, qtx, snapshot); err != nil {
				return err
			}
			item := toModelProjectItem(snapshot.Item)
			result.Restored = &item
			for _, membership := range snapshot.Memberships {
				result.RestoredProjectIDs = append(result.RestoredProjectIDs, membership.ProjectID)
			}
			for _, dependency := range snapshot.Dependencies {
				result.RestoredDependencies = append(result.RestoredDependencies, model.ItemDependency{
					ItemID:      dependency.ItemID,
					DependsOnID: dependency.DependsOnID,
				})
			}
			for _, task := range snapshot.Tasks {
				result.RestoredTasks = append(result.RestoredTasks, toModelProjectItemTask(task))
			}
		}
	}
	return nil
}

func undoTask(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("task undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemTask
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous task state: %w", err)
	}

	detail := &model.UndoDetail{ItemID: prev.ItemID}
	result.Detail = detail

	if entry.Action == "create" {
		if err := qtx.DeleteTask(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing task create: %w", err)
		}
		detail.Removed = true
		return nil
	}

	// Update and delete both reverse to the recorded row. Upsert covers both:
	// the row is still there after an update, and gone after a delete.
	if err := qtx.UpsertTask(ctx, generated.UpsertTaskParams(prev)); err != nil {
		return fmt.Errorf("restoring task: %w", err)
	}
	task := toModelProjectItemTask(prev)
	detail.Task = &task
	return nil
}

func undoMembership(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("membership undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemMembership
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous membership state: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Removed:   entry.Action == "create",
	}

	if entry.Action == "create" {
		if err := qtx.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{
			ItemID:    prev.ItemID,
			ProjectID: prev.ProjectID,
		}); err != nil {
			return fmt.Errorf("undoing membership create: %w", err)
		}
		return nil
	}

	if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(prev)); err != nil {
		return fmt.Errorf("restoring membership: %w", err)
	}
	return nil
}

func undoDependency(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("dependency undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemDependency
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous dependency state: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:      prev.ItemID,
		DependsOnID: prev.DependsOnID,
		Removed:     entry.Action == "create",
	}

	if entry.Action == "create" {
		if err := qtx.RemoveDependency(ctx, generated.RemoveDependencyParams(prev)); err != nil {
			return fmt.Errorf("undoing dependency create: %w", err)
		}
		return nil
	}

	if err := qtx.UpsertDependency(ctx, generated.UpsertDependencyParams(prev)); err != nil {
		return fmt.Errorf("restoring dependency: %w", err)
	}
	return nil
}

func undoItemPosition(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("item reorder undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemMembership
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous item position: %w", err)
	}

	if err := qtx.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Position:  prev.Position,
	}); err != nil {
		return fmt.Errorf("restoring item position: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Position:  int(prev.Position),
	}
	return nil
}

func undoProjectPosition(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("project reorder undo entry %d has no previous state", entry.ID)
	}
	var prev generated.Project
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous project position: %w", err)
	}

	if err := qtx.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{
		ID:       prev.ID,
		Position: prev.Position,
	}); err != nil {
		return fmt.Errorf("restoring project position: %w", err)
	}

	result.Detail = &model.UndoDetail{Position: int(prev.Position)}
	return nil
}

// undoProject reverses a logged project mutation.
func undoProject(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	switch entry.Action {
	case "create":
		if err := qtx.DeleteProject(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing project create: %w", err)
		}
	case "update":
		if entry.PreviousState.Valid {
			var prev generated.Project
			if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous project state: %w", err)
			}
			// Upsert rather than UpdateProject: the snapshot is the whole row,
			// and UpdateProject writes only name/description/position, so
			// undoing a complete or a drop would leave the status where it was.
			if err := qtx.UpsertProject(ctx, generated.UpsertProjectParams(prev)); err != nil {
				return fmt.Errorf("restoring previous project state: %w", err)
			}
			restored, err := qtx.GetProject(ctx, entry.EntityID)
			if err != nil {
				return fmt.Errorf("reading back the restored project: %w", err)
			}
			project := toModelProject(restored)
			result.RestoredProject = &project
		}
	case "delete":
		if entry.PreviousState.Valid {
			snapshot, err := decodeProjectSnapshot(entry.PreviousState.String)
			if err != nil {
				return err
			}
			if err := restoreProjectSnapshot(ctx, qtx, snapshot); err != nil {
				return err
			}
			project := toModelProject(snapshot.Project)
			result.RestoredProject = &project
			for _, membership := range snapshot.Memberships {
				result.RestoredItemIDs = append(result.RestoredItemIDs, membership.ItemID)
			}
		}
	}
	return nil
}

// shortEntityID names whatever an undo entry points at. It stays the UUID tail
// even for items, which display their number everywhere else: undoing a create
// leaves nothing to look the number up on, so the one identifier that works for
// every entry is the id the entry already carries.
//
// UUIDv7 front-loads the millisecond timestamp, so a prefix is identical for
// everything created in the same ~65s window. The entropy is in the tail.
func shortEntityID(id string) string {
	if len(id) >= 8 {
		return id[len(id)-8:]
	}
	return id
}

func (b *LocalBackend) CanUndo() (bool, error) {
	count, err := b.q.CountUndoLogs(b.ctx())
	if err != nil {
		return false, fmt.Errorf("counting undo logs: %w", err)
	}
	return count > 0, nil
}

// itemSnapshot is everything a delete destroys. The row alone is not enough
// to undo one: memberships, dependencies, and tasks all cascade. An item
// restored without its memberships belongs to no project, which the TUI
// cannot show at all, because it builds every view by walking projects.
type itemSnapshot struct {
	Item         generated.ProjectItem             `json:"item"`
	Memberships  []generated.ProjectItemMembership `json:"memberships"`
	Dependencies []generated.ProjectItemDependency `json:"dependencies"`
	Tasks        []generated.ProjectItemTask       `json:"tasks"`
}

// projectSnapshot is the project equivalent. Memberships cascade off a
// project delete; the items on the other end of them do not.
type projectSnapshot struct {
	Project     generated.Project                 `json:"project"`
	Memberships []generated.ProjectItemMembership `json:"memberships"`
}

func captureItem(ctx context.Context, q *generated.Queries, id string) (*itemSnapshot, error) {
	item, err := q.GetItem(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item for delete: %w", err)
	}
	memberships, err := q.GetItemMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item memberships: %w", err)
	}
	dependencies, err := q.GetDependenciesInvolvingItem(ctx, generated.GetDependenciesInvolvingItemParams{
		ItemID:      id,
		DependsOnID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("capturing item dependencies: %w", err)
	}
	tasks, err := q.ListTasksByItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item tasks: %w", err)
	}
	return &itemSnapshot{
		Item:         item,
		Memberships:  memberships,
		Dependencies: dependencies,
		Tasks:        tasks,
	}, nil
}

func captureProject(ctx context.Context, q *generated.Queries, id string) (*projectSnapshot, error) {
	project, err := q.GetProject(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project for delete: %w", err)
	}
	memberships, err := q.GetProjectMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing project memberships: %w", err)
	}
	return &projectSnapshot{Project: project, Memberships: memberships}, nil
}

// decodeItemSnapshot accepts either shape of previous_state: the snapshot
// written now, or the bare item row written before snapshots existed. Undo
// logs are not pruned, so pre-existing databases still hold the old shape.
func decodeItemSnapshot(raw string) (*itemSnapshot, error) {
	var snapshot itemSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err == nil && snapshot.Item.ID != "" {
		return &snapshot, nil
	}
	var item generated.ProjectItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, fmt.Errorf("unmarshaling deleted item state: %w", err)
	}
	return &itemSnapshot{Item: item}, nil
}

func decodeProjectSnapshot(raw string) (*projectSnapshot, error) {
	var snapshot projectSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err == nil && snapshot.Project.ID != "" {
		return &snapshot, nil
	}
	var project generated.Project
	if err := json.Unmarshal([]byte(raw), &project); err != nil {
		return nil, fmt.Errorf("unmarshaling deleted project state: %w", err)
	}
	return &projectSnapshot{Project: project}, nil
}

// restoreItemSnapshot puts the row back with its original timestamps, then
// reattaches whatever cascaded off it.
func restoreItemSnapshot(ctx context.Context, qtx *generated.Queries, snapshot *itemSnapshot) error {
	if err := qtx.UpsertItem(ctx, generated.UpsertItemParams(snapshot.Item)); err != nil {
		return fmt.Errorf("recreating deleted item: %w", err)
	}
	for _, membership := range snapshot.Memberships {
		if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(membership)); err != nil {
			return fmt.Errorf("restoring item membership: %w", err)
		}
	}
	for _, dependency := range snapshot.Dependencies {
		if err := qtx.UpsertDependency(ctx, generated.UpsertDependencyParams(dependency)); err != nil {
			return fmt.Errorf("restoring item dependency: %w", err)
		}
	}
	for _, task := range snapshot.Tasks {
		if err := qtx.UpsertTask(ctx, generated.UpsertTaskParams(task)); err != nil {
			return fmt.Errorf("restoring item task: %w", err)
		}
	}
	return nil
}

func restoreProjectSnapshot(ctx context.Context, qtx *generated.Queries, snapshot *projectSnapshot) error {
	if err := qtx.UpsertProject(ctx, generated.UpsertProjectParams(snapshot.Project)); err != nil {
		return fmt.Errorf("recreating deleted project: %w", err)
	}
	for _, membership := range snapshot.Memberships {
		if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(membership)); err != nil {
			return fmt.Errorf("restoring project membership: %w", err)
		}
	}
	return nil
}

// inTx runs fn inside a transaction so a mutation and its undo-log entry
// commit together. Split apart, a failure between them leaves either an undo
// entry for a change that never happened or a change nothing can reverse.
func (b *LocalBackend) inTx(fn func(qtx *generated.Queries) error) error {
	tx, err := b.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(b.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *LocalBackend) logUndo(qtx *generated.Queries, action, entityType string, entityID string, previousState any) error {
	var prevJSON sql.NullString
	if previousState != nil {
		data, err := json.Marshal(previousState)
		if err != nil {
			return fmt.Errorf("marshaling previous state: %w", err)
		}
		prevJSON = sql.NullString{String: string(data), Valid: true}
	}
	return qtx.InsertUndoLog(b.ctx(), generated.InsertUndoLogParams{
		Action:        action,
		EntityType:    entityType,
		EntityID:      entityID,
		PreviousState: prevJSON,
	})
}
