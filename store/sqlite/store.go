package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
	"github.com/datapointchris/projects/store/sqlite/generated"
)

// Store is store.Store over a database Open returned.
type Store struct {
	operations
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// New wraps a database Open returned. The caller keeps ownership of closing it.
func New(db *sql.DB) *Store {
	return &Store{operations: operations{q: generated.New(db)}, db: db}
}

// InTx implements store.Store.
func (s *Store) InTx(ctx context.Context, fn func(tx store.Operations) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(operations{q: s.q.WithTx(tx)}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// operations runs each store.Operations method over whichever connection or
// transaction its queries were bound to.
type operations struct {
	q *generated.Queries
}

// rowError gives the driver's no-row answer the name every store answers with.
func rowError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

// duplicateNameError names a unique violation for the writes store.Store promises
// ErrDuplicateName on. A project's only unique index beyond its id is the one on
// an active project's name, and the driver answers an id collision with a
// different code. Only active projects hold a name, so a status change fires this
// only on the way back to active.
func duplicateNameError(err error) error {
	var driverErr *modernc.Error
	if errors.As(err, &driverErr) && driverErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return model.ErrDuplicateName
	}
	return rowError(err)
}

func (o operations) ListProjectsWithItemCount(ctx context.Context, status string) ([]model.ProjectWithItemCount, error) {
	rows, err := o.q.ListProjectsWithItemCount(ctx, status)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProjectWithItemCount, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectWithItemCount(r)
	}
	return out, nil
}

func (o operations) GetProjectWithItemCount(ctx context.Context, id string) (model.ProjectWithItemCount, error) {
	row, err := o.q.GetProjectWithItemCount(ctx, id)
	if err != nil {
		return model.ProjectWithItemCount{}, rowError(err)
	}
	return toModelProjectWithItemCountFromGet(row), nil
}

func (o operations) GetProject(ctx context.Context, id string) (model.Project, error) {
	row, err := o.q.GetProject(ctx, id)
	if err != nil {
		return model.Project{}, rowError(err)
	}
	return toModelProject(row), nil
}

func (o operations) CreateProject(ctx context.Context, project model.Project) (model.Project, error) {
	row, err := o.q.CreateProject(ctx, generated.CreateProjectParams{
		ID:          project.ID,
		Name:        project.Name,
		Description: toNullString(project.Description),
	})
	if err != nil {
		return model.Project{}, duplicateNameError(err)
	}
	return toModelProject(row), nil
}

func (o operations) UpdateProject(ctx context.Context, project model.Project) (model.Project, error) {
	row, err := o.q.UpdateProject(ctx, generated.UpdateProjectParams{
		Name:        project.Name,
		Description: toNullString(project.Description),
		Position:    int64(project.Position),
		ID:          project.ID,
	})
	if err != nil {
		return model.Project{}, duplicateNameError(err)
	}
	return toModelProject(row), nil
}

func (o operations) SetProjectStatus(ctx context.Context, id, status string, reason *string) (model.Project, error) {
	row, err := o.q.SetProjectStatus(ctx, generated.SetProjectStatusParams{
		NewStatus: status,
		Reason:    toNullString(reason),
		ID:        id,
	})
	if err != nil {
		return model.Project{}, duplicateNameError(err)
	}
	return toModelProject(row), nil
}

func (o operations) UpdateProjectPosition(ctx context.Context, id string, position int) error {
	return o.q.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{
		Position: int64(position),
		ID:       id,
	})
}

func (o operations) DeleteProject(ctx context.Context, id string) error {
	return o.q.DeleteProject(ctx, id)
}

func (o operations) RestoreProject(ctx context.Context, project model.Project) error {
	return duplicateNameError(o.q.UpsertProject(ctx, generated.UpsertProjectParams{
		ID:           project.ID,
		Name:         project.Name,
		Description:  toNullString(project.Description),
		Status:       project.Status,
		StatusReason: toNullString(project.StatusReason),
		ClosedAt:     toNullTime(project.ClosedAt),
		Position:     int64(project.Position),
		CreatedAt:    formatTime(project.CreatedAt),
	}))
}

func (o operations) ListAllItems(ctx context.Context) ([]model.ProjectItem, error) {
	rows, err := o.q.ListAllItemsRaw(ctx)
	if err != nil {
		return nil, err
	}
	return toModelProjectItems(rows), nil
}

func (o operations) ListItemsByProject(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := o.q.ListItemsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProject(r)
	}
	return out, nil
}

func (o operations) ListArchivedItems(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := o.q.ListArchivedItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProjectItemInProject, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemInProjectFromArchived(r)
	}
	return out, nil
}

func (o operations) ListBlockedItems(ctx context.Context) ([]model.ProjectItem, error) {
	rows, err := o.q.ListBlockedItems(ctx)
	if err != nil {
		return nil, err
	}
	return toModelProjectItems(rows), nil
}

func (o operations) SearchItems(ctx context.Context, query string) ([]model.ProjectItem, error) {
	q := sql.NullString{String: query, Valid: true}
	rows, err := o.q.SearchItems(ctx, generated.SearchItemsParams{Column1: q, Column2: q})
	if err != nil {
		return nil, err
	}
	return toModelProjectItems(rows), nil
}

func (o operations) GetItem(ctx context.Context, id string) (model.ProjectItem, error) {
	row, err := o.q.GetItem(ctx, id)
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelProjectItem(row), nil
}

func (o operations) NextItemNumber(ctx context.Context) (int, error) {
	next, err := o.q.NextItemNumber(ctx)
	if err != nil {
		return 0, err
	}
	return int(next), nil
}

func (o operations) CreateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error) {
	row, err := o.q.CreateItem(ctx, generated.CreateItemParams{
		ID:     item.ID,
		Number: toNullInt64(item.Number),
		Title:  item.Title,
		Notes:  toNullString(item.Notes),
		Repo:   toNullString(item.Repo),
	})
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelProjectItem(row), nil
}

func (o operations) UpdateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error) {
	row, err := o.q.UpdateItem(ctx, generated.UpdateItemParams{
		Title:     item.Title,
		Notes:     toNullString(item.Notes),
		Repo:      toNullString(item.Repo),
		Completed: boolToInt64(item.Completed),
		Archived:  boolToInt64(item.Archived),
		ID:        item.ID,
	})
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelProjectItem(row), nil
}

func (o operations) DeleteItem(ctx context.Context, id string) error {
	return o.q.DeleteItem(ctx, id)
}

func (o operations) RestoreItem(ctx context.Context, item model.ProjectItem) error {
	return o.q.UpsertItem(ctx, generated.UpsertItemParams{
		ID:        item.ID,
		Number:    toNullInt64(item.Number),
		Title:     item.Title,
		Notes:     toNullString(item.Notes),
		Repo:      toNullString(item.Repo),
		Completed: boolToInt64(item.Completed),
		Archived:  boolToInt64(item.Archived),
		CreatedAt: formatTime(item.CreatedAt),
		UpdatedAt: formatTime(item.UpdatedAt),
	})
}

func (o operations) ListAllMemberships(ctx context.Context) ([]model.Membership, error) {
	rows, err := o.q.ListAllMemberships(ctx)
	if err != nil {
		return nil, err
	}
	return toModelMemberships(rows), nil
}

func (o operations) ListItemMemberships(ctx context.Context, itemID string) ([]model.Membership, error) {
	rows, err := o.q.GetItemMemberships(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return toModelMemberships(rows), nil
}

func (o operations) ListProjectMemberships(ctx context.Context, projectID string) ([]model.Membership, error) {
	rows, err := o.q.GetProjectMemberships(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return toModelMemberships(rows), nil
}

func (o operations) GetMembership(ctx context.Context, itemID, projectID string) (model.Membership, error) {
	row, err := o.q.GetMembership(ctx, generated.GetMembershipParams{ItemID: itemID, ProjectID: projectID})
	if err != nil {
		return model.Membership{}, rowError(err)
	}
	return toModelMembership(row), nil
}

func (o operations) GetItemProjects(ctx context.Context, itemID string) ([]model.Project, error) {
	rows, err := o.q.GetItemProjects(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return toModelProjects(rows), nil
}

func (o operations) AddItemToProject(ctx context.Context, itemID, projectID string) error {
	return o.q.AddItemToProject(ctx, generated.AddItemToProjectParams{
		ItemID:      itemID,
		ProjectID:   projectID,
		ProjectID_2: projectID,
	})
}

func (o operations) RemoveItemFromProject(ctx context.Context, itemID, projectID string) error {
	return o.q.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{ItemID: itemID, ProjectID: projectID})
}

func (o operations) UpdateItemPosition(ctx context.Context, membership model.Membership) error {
	return o.q.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
		Position:  int64(membership.Position),
		ItemID:    membership.ItemID,
		ProjectID: membership.ProjectID,
	})
}

func (o operations) RestoreMembership(ctx context.Context, membership model.Membership) error {
	return o.q.RestoreMembership(ctx, generated.RestoreMembershipParams{
		ItemID:    membership.ItemID,
		ProjectID: membership.ProjectID,
		Position:  int64(membership.Position),
	})
}

func (o operations) ListAllDependencies(ctx context.Context) ([]model.Dependency, error) {
	rows, err := o.q.GetAllDependencies(ctx)
	if err != nil {
		return nil, err
	}
	return toModelDependencies(rows), nil
}

func (o operations) ListDependenciesInvolvingItem(ctx context.Context, itemID string) ([]model.Dependency, error) {
	rows, err := o.q.GetDependenciesInvolvingItem(ctx, generated.GetDependenciesInvolvingItemParams{
		ItemID:      itemID,
		DependsOnID: itemID,
	})
	if err != nil {
		return nil, err
	}
	return toModelDependencies(rows), nil
}

func (o operations) GetDependencyIDs(ctx context.Context, itemID string) ([]string, error) {
	return o.q.GetDependencyIDs(ctx, itemID)
}

func (o operations) GetBlockers(ctx context.Context, itemID string) ([]model.ProjectItem, error) {
	rows, err := o.q.GetBlockers(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return toModelProjectItems(rows), nil
}

func (o operations) AddDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.AddDependency(ctx, generated.AddDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) RemoveDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.RemoveDependency(ctx, generated.RemoveDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) RestoreDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.UpsertDependency(ctx, generated.UpsertDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) ListTasksByItem(ctx context.Context, itemID string) ([]model.ProjectItemTask, error) {
	rows, err := o.q.ListTasksByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProjectItemTask, len(rows))
	for i, r := range rows {
		out[i] = toModelProjectItemTask(r)
	}
	return out, nil
}

func (o operations) GetTask(ctx context.Context, id string) (model.ProjectItemTask, error) {
	row, err := o.q.GetTask(ctx, id)
	if err != nil {
		return model.ProjectItemTask{}, rowError(err)
	}
	return toModelProjectItemTask(row), nil
}

func (o operations) CreateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error) {
	row, err := o.q.CreateTask(ctx, generated.CreateTaskParams{
		ID:       task.ID,
		ItemID:   task.ItemID,
		Title:    task.Title,
		ItemID_2: task.ItemID,
	})
	if err != nil {
		return model.ProjectItemTask{}, rowError(err)
	}
	return toModelProjectItemTask(row), nil
}

func (o operations) UpdateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error) {
	row, err := o.q.UpdateTask(ctx, generated.UpdateTaskParams{
		Title:     task.Title,
		Completed: boolToInt64(task.Completed),
		Position:  int64(task.Position),
		ID:        task.ID,
	})
	if err != nil {
		return model.ProjectItemTask{}, rowError(err)
	}
	return toModelProjectItemTask(row), nil
}

func (o operations) DeleteTask(ctx context.Context, id string) error {
	return o.q.DeleteTask(ctx, id)
}

func (o operations) RestoreTask(ctx context.Context, task model.ProjectItemTask) error {
	return o.q.UpsertTask(ctx, generated.UpsertTaskParams{
		ID:        task.ID,
		ItemID:    task.ItemID,
		Title:     task.Title,
		Completed: boolToInt64(task.Completed),
		Position:  int64(task.Position),
		CreatedAt: formatTime(task.CreatedAt),
	})
}

func (o operations) InsertUndoEntry(ctx context.Context, entry model.UndoEntry) error {
	return o.q.InsertUndoLog(ctx, generated.InsertUndoLogParams{
		Action:        entry.Action,
		EntityType:    entry.EntityType,
		EntityID:      entry.EntityID,
		PreviousState: toNullString(entry.PreviousState),
	})
}

func (o operations) GetLatestUndoEntry(ctx context.Context) (model.UndoEntry, error) {
	row, err := o.q.GetLatestUndoLog(ctx)
	if err != nil {
		return model.UndoEntry{}, rowError(err)
	}
	return toModelUndoEntry(row), nil
}

func (o operations) DeleteUndoEntry(ctx context.Context, id int64) error {
	return o.q.DeleteUndoLog(ctx, id)
}

func (o operations) CountUndoEntries(ctx context.Context) (int, error) {
	count, err := o.q.CountUndoLogs(ctx)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}
