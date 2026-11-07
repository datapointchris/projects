package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/graph"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/store/sqlite/generated"
)

// Service holds every rule about projects, items, memberships, dependencies, tasks and undo.
type Service struct {
	db *sql.DB
	q  *generated.Queries
	// assignsItemNumbers is set when nothing upstream will hand one out, which
	// is exactly the sync-disabled case. See AssigningItemNumbers.
	assignsItemNumbers bool
	// repoRegistryPath enables the repo-named-project ban. Empty means no
	// registry and no ban. See RefusingRepoNames.
	repoRegistryPath string
}

// Option configures a Service at construction.
type Option func(*Service)

// AssigningItemNumbers makes this database the authority for item numbers.
//
// Pass it when sync is off — the work machine, which is never allowed to reach
// the ichrisbirch API, so its database is a disjoint universe that no server
// will ever number. With sync on the server assigns instead, because a number
// guessed locally and a number assigned upstream would disagree and the handle
// would change under a user who had already written it down.
func AssigningItemNumbers() Option {
	return func(s *Service) { s.assignsItemNumbers = true }
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
func RefusingRepoNames(path string) Option {
	return func(s *Service) { s.repoRegistryPath = path }
}

// New creates a service over a local SQLite database.
func New(db *sql.DB, opts ...Option) *Service {
	s := &Service{db: db, q: generated.New(db)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Compile-time check that Service implements Backend.
var _ backend.Backend = (*Service)(nil)

func (s *Service) ctx() context.Context {
	return context.Background()
}

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// --- Projects ---

// refuseRepoName rejects a project name the repo registry knows. A missing
// registry bans nothing: refusing to create a project on a machine without one
// is worse than a project named after a repo.
func (s *Service) refuseRepoName(name string) error {
	if s.repoRegistryPath == "" {
		return nil
	}
	registry, err := repos.Load(s.repoRegistryPath)
	if err != nil {
		return err
	}
	if registry.Knows(name) {
		return fmt.Errorf("%w: %q names a repo, which never ends — name the bounded work instead", model.ErrRepoNamedProject, name)
	}
	return nil
}

// ListProjectsByStatus takes a status name or model.StatusAll.
func (s *Service) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	rows, err := s.q.ListProjectsWithItemCount(s.ctx(), status)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	out := make([]model.ProjectWithItemCount, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectWithItemCount(r)
	}
	return out, nil
}

func (s *Service) GetProject(id string) (*model.ProjectWithItemCount, error) {
	row, err := s.q.GetProjectWithItemCount(s.ctx(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project: %w", err)
	}
	result := toModelProjectWithItemCountFromGet(row)
	return &result, nil
}

func (s *Service) CreateProject(input model.CreateProject) (*model.Project, error) {
	ctx := s.ctx()
	if err := s.refuseRepoName(input.Name); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning create project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)

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
	if err := s.logUndo(qtx, "create", "project", p.ID, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing create project: %w", err)
	}
	result := toModelProject(p)
	return &result, nil
}

func (s *Service) UpdateProject(id string, input model.UpdateProject) (*model.Project, error) {
	ctx := s.ctx()

	current, err := s.q.GetProject(ctx, id)
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
		if err := s.refuseRepoName(*input.Name); err != nil {
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

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning update project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)

	p, err := qtx.UpdateProject(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("updating project: %w", err)
	}
	if err := s.logUndo(qtx, "update", "project", id, current); err != nil {
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
func (s *Service) SetProjectStatus(id, status string, reason *string) (*model.Project, error) {
	ctx := s.ctx()
	if status == model.StatusDropped && (reason == nil || strings.TrimSpace(*reason) == "") {
		return nil, model.ErrDropReasonRequired
	}

	current, err := s.q.GetProject(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project for status change: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning project status transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)

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
	if err := s.logUndo(qtx, "update", "project", id, current); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing project status: %w", err)
	}
	result := toModelProject(p)
	return &result, nil
}

func (s *Service) DeleteProject(id string) error {
	ctx := s.ctx()
	snapshot, err := captureProject(ctx, s.q, id)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning delete project transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)

	if err := qtx.DeleteProject(ctx, id); err != nil {
		return fmt.Errorf("deleting project: %w", err)
	}
	if err := s.logUndo(qtx, "delete", "project", id, snapshot); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing delete project: %w", err)
	}
	return nil
}

func (s *Service) ReorderProject(projectID string, newPosition int) error {
	ctx := s.ctx()
	current, err := s.q.GetProject(ctx, projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("project %q: %w", projectID, model.ErrNotFound)
		}
		return fmt.Errorf("getting project for reorder: %w", err)
	}

	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{
			ID:       projectID,
			Position: int64(newPosition),
		}); err != nil {
			return fmt.Errorf("reordering project: %w", err)
		}
		return s.logUndo(qtx, "update", "project_position", projectID, current)
	})
}

// --- Items ---

func (s *Service) ListAllItemsIncludingArchived() ([]model.ProjectItem, error) {
	items, err := s.q.ListAllItemsRaw(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing all items: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (s *Service) ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := s.q.ListItemsByProject(s.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing items by project: %w", err)
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProject(r)
	}
	return out, nil
}

func (s *Service) GetItem(id string) (*model.ProjectItemDetail, error) {
	pi, err := s.q.GetItem(s.ctx(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item: %w", err)
	}
	result := toModelProjectItem(pi)

	ps, err := s.q.GetItemProjects(s.ctx(), id)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}

	depIDs, err := s.q.GetDependencyIDs(s.ctx(), id)
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

func (s *Service) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	if len(input.ProjectIDs) == 0 {
		return nil, model.ErrLastProject
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := s.q.WithTx(tx)
	ctx := s.ctx()

	number := sql.NullInt64{}
	if s.assignsItemNumbers {
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

	if err := s.logUndo(qtx, "create", "item", pi.ID, nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return s.GetItem(pi.ID)
}

func (s *Service) UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	ctx := s.ctx()

	current, err := s.q.GetItem(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item for update: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := s.q.WithTx(tx)

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

	if err := s.logUndo(qtx, "update", "item", id, current); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	result := toModelProjectItem(pi)
	return &result, nil
}

func (s *Service) DeleteItem(id string) error {
	ctx := s.ctx()
	snapshot, err := captureItem(ctx, s.q, id)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := s.q.WithTx(tx)

	if err := s.logUndo(qtx, "delete", "item", id, snapshot); err != nil {
		return err
	}

	if err := qtx.DeleteItem(ctx, id); err != nil {
		return fmt.Errorf("deleting item: %w", err)
	}

	return tx.Commit()
}

func (s *Service) ReorderItem(itemID, projectID string, newPosition int) error {
	ctx := s.ctx()
	current, err := s.q.GetMembership(ctx, generated.GetMembershipParams{
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

	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
			ItemID:    itemID,
			ProjectID: projectID,
			Position:  int64(newPosition),
		}); err != nil {
			return fmt.Errorf("reordering item: %w", err)
		}
		return s.logUndo(qtx, "update", "item_position", itemID, current)
	})
}

// --- Multi-project membership ---

func (s *Service) AddToProject(itemID, projectID string) error {
	ctx := s.ctx()
	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.AddItemToProject(ctx, generated.AddItemToProjectParams{
			ItemID:      itemID,
			ProjectID:   projectID,
			ProjectID_2: projectID,
		}); err != nil {
			return fmt.Errorf("adding item to project: %w", err)
		}
		return s.logUndo(qtx, "create", "membership", itemID, generated.ProjectItemMembership{
			ItemID:    itemID,
			ProjectID: projectID,
		})
	})
}

func (s *Service) RemoveFromProject(itemID, projectID string) error {
	ctx := s.ctx()
	projects, err := s.q.GetItemProjects(ctx, itemID)
	if err != nil {
		return fmt.Errorf("checking project count: %w", err)
	}
	if len(projects) <= 1 {
		return model.ErrLastProject
	}

	current, err := s.q.GetMembership(ctx, generated.GetMembershipParams{
		ItemID:    itemID,
		ProjectID: projectID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("item %q is not in project %q: %w", itemID, projectID, model.ErrNotFound)
		}
		return fmt.Errorf("getting membership for remove: %w", err)
	}

	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{
			ItemID:    itemID,
			ProjectID: projectID,
		}); err != nil {
			return fmt.Errorf("removing item from project: %w", err)
		}
		return s.logUndo(qtx, "delete", "membership", itemID, current)
	})
}

func (s *Service) GetItemProjects(itemID string) ([]model.Project, error) {
	ps, err := s.q.GetItemProjects(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}
	return toModelProjects(ps), nil
}

// --- Dependencies ---

func (s *Service) AddDependency(itemID, dependsOn string) error {
	ctx := s.ctx()

	deps, err := s.q.GetAllDependencies(ctx)
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
	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.AddDependency(ctx, generated.AddDependencyParams(edge)); err != nil {
			return fmt.Errorf("adding dependency: %w", err)
		}
		return s.logUndo(qtx, "create", "dependency", itemID, edge)
	})
}

func (s *Service) RemoveDependency(itemID, dependsOn string) error {
	ctx := s.ctx()
	edge := generated.ProjectItemDependency{ItemID: itemID, DependsOnID: dependsOn}
	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.RemoveDependency(ctx, generated.RemoveDependencyParams(edge)); err != nil {
			return fmt.Errorf("removing dependency: %w", err)
		}
		return s.logUndo(qtx, "delete", "dependency", itemID, edge)
	})
}

func (s *Service) GetBlockers(itemID string) ([]model.ProjectItem, error) {
	items, err := s.q.GetBlockers(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (s *Service) ListDependencies() ([]model.Dependency, error) {
	rows, err := s.q.GetAllDependencies(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing dependencies: %w", err)
	}
	out := make([]model.Dependency, len(rows))
	for i, r := range rows {
		out[i] = model.Dependency{ItemID: r.ItemID, DependsOnID: r.DependsOnID}
	}
	return out, nil
}

func (s *Service) ListMemberships() ([]model.Membership, error) {
	rows, err := s.q.ListAllMemberships(s.ctx())
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

func (s *Service) ListTasks(itemID string) ([]model.ProjectItemTask, error) {
	rows, err := s.q.ListTasksByItem(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	out := make([]model.ProjectItemTask, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemTask(r)
	}
	return out, nil
}

func (s *Service) CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()
	id := newID()

	var created generated.ProjectItemTask
	err := s.inTx(func(qtx *generated.Queries) error {
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
		return s.logUndo(qtx, "create", "task", id, t)
	})
	if err != nil {
		return nil, err
	}

	result := toModelProjectItemTask(created)
	return &result, nil
}

func (s *Service) UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()

	current, err := s.q.GetTask(ctx, taskID)
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
	err = s.inTx(func(qtx *generated.Queries) error {
		t, err := qtx.UpdateTask(ctx, params)
		if err != nil {
			return fmt.Errorf("updating task: %w", err)
		}
		updated = t
		return s.logUndo(qtx, "update", "task", taskID, current)
	})
	if err != nil {
		return nil, err
	}

	result := toModelProjectItemTask(updated)
	return &result, nil
}

func (s *Service) DeleteTask(itemID, taskID string) error {
	ctx := s.ctx()
	current, err := s.q.GetTask(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("task %q: %w", taskID, model.ErrNotFound)
		}
		return fmt.Errorf("getting task for delete: %w", err)
	}

	return s.inTx(func(qtx *generated.Queries) error {
		if err := qtx.DeleteTask(ctx, taskID); err != nil {
			return fmt.Errorf("deleting task: %w", err)
		}
		return s.logUndo(qtx, "delete", "task", taskID, current)
	})
}

// --- Search ---

func (s *Service) Search(query string) ([]model.ProjectItem, error) {
	q := sql.NullString{String: query, Valid: true}
	items, err := s.q.SearchItems(s.ctx(), generated.SearchItemsParams{
		Column1: q,
		Column2: q,
	})
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return toModelProjectItems(items), nil
}

// --- Filters ---

func (s *Service) ListBlocked() ([]model.ProjectItem, error) {
	items, err := s.q.ListBlockedItems(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing blocked: %w", err)
	}
	return toModelProjectItems(items), nil
}

func (s *Service) ListArchived(projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := s.q.ListArchivedItems(s.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing archived: %w", err)
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProjectFromArchived(r)
	}
	return out, nil
}

// inTx runs fn inside a transaction so a mutation and its undo-log entry
// commit together. Split apart, a failure between them leaves either an undo
// entry for a change that never happened or a change nothing can reverse.
func (s *Service) inTx(fn func(qtx *generated.Queries) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
