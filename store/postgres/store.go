package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
	"github.com/datapointchris/projects/store/postgres/generated"
)

// writeLockKey names the one advisory lock every transaction takes, chosen for
// this database alone, since an advisory lock's key is shared by whoever else uses
// the same server's database.
const writeLockKey = 7_370_001

// The Postgres error codes a store names for its callers.
const (
	uniqueViolation  = "23505"
	invalidTextInput = "22P02"
)

// The constraints a unique violation can name that store.Store promises a sentinel on.
const (
	activeNameIndex = "uq_projects_name_active"
	projectIDKey    = "pk_projects"
	itemIDKey       = "pk_project_items"
	taskIDKey       = "pk_project_item_tasks"
)

// Store is store.Store over a connection pool.
type Store struct {
	operations
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// New wraps a pool Connect returned. The caller keeps ownership of closing it.
func New(pool *pgxpool.Pool) *Store {
	return &Store{operations: operations{q: generated.New(unavailableNamingDB{pool})}, pool: pool}
}

// Ping reports whether the database answers.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// InTx implements store.Store. It holds the write lock from the start of each
// transaction, which meets the promise that what fn read holds at commit without a
// retry loop, at no cost a person's write rate can notice. The writer on ctx binds
// to transaction-local settings, which the audit trigger reads.
func (s *Store) InTx(ctx context.Context, fn func(tx store.Operations) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", unavailableError(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := generated.New(unavailableNamingDB{tx})
	if err := q.TakeWriteLock(ctx, writeLockKey); err != nil {
		return fmt.Errorf("taking the write lock: %w", err)
	}
	writer := store.WriterFrom(ctx)
	if err := q.BindWriter(ctx, generated.BindWriterParams{
		Actor:       writer.Actor,
		RequestID:   writer.RequestID,
		JobRunID:    writer.JobRunID,
		Traceparent: writer.Traceparent,
		Baggage:     writer.Baggage,
	}); err != nil {
		return fmt.Errorf("binding the writer: %w", err)
	}

	if err := fn(operations{q: q}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", unavailableError(err))
	}
	return nil
}

// operations runs each store.Operations method over whichever pool or transaction
// its queries were bound to.
type operations struct {
	q *generated.Queries
}

// rowError gives the driver's no-row answer the name every store answers with. An
// id that does not parse names no row either.
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	var driverErr *pgconn.PgError
	if errors.As(err, &driverErr) && driverErr.Code == invalidTextInput {
		return model.ErrNotFound
	}
	return err
}

// duplicateError names a unique violation for the writes store.Store promises a
// sentinel on.
func duplicateError(err error) error {
	var driverErr *pgconn.PgError
	if errors.As(err, &driverErr) && driverErr.Code == uniqueViolation {
		switch driverErr.ConstraintName {
		case activeNameIndex:
			return model.ErrDuplicateName
		case projectIDKey, itemIDKey, taskIDKey:
			return model.ErrDuplicateID
		}
	}
	return rowError(err)
}

func (o operations) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := o.q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelProject), nil
}

func (o operations) ListProjectItemFacts(ctx context.Context) ([]model.ProjectItemFact, error) {
	rows, err := o.q.ListProjectItemFacts(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, func(r generated.ListProjectItemFactsRow) model.ProjectItemFact {
		return model.ProjectItemFact{ProjectID: r.ProjectID, Completed: r.Completed, Archived: r.Archived, Repo: r.Repo}
	}), nil
}

func (o operations) ListProjectKinds(ctx context.Context) ([]string, error) {
	return o.q.ListProjectKinds(ctx)
}

func (o operations) ListProjectStatuses(ctx context.Context) ([]string, error) {
	return o.q.ListProjectStatuses(ctx)
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
		ID:              project.ID,
		Name:            project.Name,
		Description:     project.Description,
		Kind:            project.Kind,
		Status:          project.Status,
		StatusReason:    project.StatusReason,
		ClosedAt:        project.ClosedAt,
		ProjectPosition: int32(project.Position),
	})
	if err != nil {
		return model.Project{}, duplicateError(err)
	}
	return toModelProject(row), nil
}

func (o operations) UpdateProject(ctx context.Context, project model.Project) (model.Project, error) {
	row, err := o.q.UpdateProject(ctx, generated.UpdateProjectParams{
		ID:              project.ID,
		Name:            project.Name,
		Description:     project.Description,
		Kind:            project.Kind,
		Status:          project.Status,
		StatusReason:    project.StatusReason,
		ClosedAt:        project.ClosedAt,
		ProjectPosition: int32(project.Position),
	})
	if err != nil {
		return model.Project{}, duplicateError(err)
	}
	return toModelProject(row), nil
}

func (o operations) UpdateProjectPosition(ctx context.Context, id string, position int) error {
	return o.q.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{ID: id, ProjectPosition: int32(position)})
}

func (o operations) DeleteProject(ctx context.Context, id string) error {
	return o.q.DeleteProject(ctx, id)
}

func (o operations) RestoreProject(ctx context.Context, project model.Project) error {
	return duplicateError(o.q.RestoreProject(ctx, generated.RestoreProjectParams{
		ID:              project.ID,
		Name:            project.Name,
		Description:     project.Description,
		Kind:            project.Kind,
		Status:          project.Status,
		StatusReason:    project.StatusReason,
		ClosedAt:        project.ClosedAt,
		ProjectPosition: int32(project.Position),
		CreatedAt:       project.CreatedAt,
	}))
}

func (o operations) ListAllItems(ctx context.Context) ([]model.ProjectItem, error) {
	rows, err := o.q.ListAllItems(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelItem), nil
}

func (o operations) ListItemsInProject(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error) {
	rows, err := o.q.ListItemsInProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelItemInProject), nil
}

func (o operations) ListBlockedItems(ctx context.Context) ([]model.ProjectItem, error) {
	rows, err := o.q.ListBlockedItems(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelItem), nil
}

func (o operations) SearchItems(ctx context.Context, query string) ([]model.ProjectItem, error) {
	rows, err := o.q.SearchItems(ctx, query)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelItem), nil
}

func (o operations) GetItem(ctx context.Context, id string) (model.ProjectItem, error) {
	row, err := o.q.GetItem(ctx, id)
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelItem(row), nil
}

func (o operations) GetItemByNumber(ctx context.Context, number int) (model.ProjectItem, error) {
	row, err := o.q.GetItemByNumber(ctx, int64(number))
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelItem(row), nil
}

func (o operations) NextItemNumber(ctx context.Context) (int, error) {
	next, err := o.q.NextItemNumber(ctx)
	if err != nil {
		return 0, err
	}
	return int(next), nil
}

// errItemNeedsNumber is an undo record restoring an item with no number, which
// the column cannot hold.
var errItemNeedsNumber = errors.New("an item on Postgres needs a number")

func (o operations) CreateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error) {
	row, err := o.q.CreateItem(ctx, generated.CreateItemParams{
		ID:         item.ID,
		ItemNumber: int64(*item.Number),
		Title:      item.Title,
		Notes:      item.Notes,
		Repo:       item.Repo,
	})
	if err != nil {
		return model.ProjectItem{}, duplicateError(err)
	}
	return toModelItem(row), nil
}

func (o operations) UpdateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error) {
	row, err := o.q.UpdateItem(ctx, generated.UpdateItemParams{
		ID:          item.ID,
		Title:       item.Title,
		Notes:       item.Notes,
		Repo:        item.Repo,
		Completed:   item.Completed,
		CompletedAt: item.CompletedAt,
		Archived:    item.Archived,
	})
	if err != nil {
		return model.ProjectItem{}, rowError(err)
	}
	return toModelItem(row), nil
}

func (o operations) DeleteItem(ctx context.Context, id string) error {
	return o.q.DeleteItem(ctx, id)
}

func (o operations) RestoreItem(ctx context.Context, item model.ProjectItem) error {
	if item.Number == nil {
		return errItemNeedsNumber
	}
	return o.q.RestoreItem(ctx, generated.RestoreItemParams{
		ID:          item.ID,
		ItemNumber:  int64(*item.Number),
		Title:       item.Title,
		Notes:       item.Notes,
		Repo:        item.Repo,
		Completed:   item.Completed,
		CompletedAt: item.CompletedAt,
		Archived:    item.Archived,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	})
}

func (o operations) ListAllMemberships(ctx context.Context) ([]model.Membership, error) {
	rows, err := o.q.ListAllMemberships(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelMembership), nil
}

func (o operations) ListItemMemberships(ctx context.Context, itemID string) ([]model.Membership, error) {
	rows, err := o.q.ListItemMemberships(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelMembership), nil
}

func (o operations) ListProjectMemberships(ctx context.Context, projectID string) ([]model.Membership, error) {
	rows, err := o.q.ListProjectMemberships(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelMembership), nil
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
	return listOf(rows, toModelProject), nil
}

func (o operations) AddItemToProject(ctx context.Context, membership model.Membership) error {
	return o.q.AddItemToProject(ctx, generated.AddItemToProjectParams{
		ItemID:       membership.ItemID,
		ProjectID:    membership.ProjectID,
		ItemPosition: int32(membership.Position),
	})
}

func (o operations) RemoveItemFromProject(ctx context.Context, itemID, projectID string) error {
	return o.q.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{ItemID: itemID, ProjectID: projectID})
}

func (o operations) UpdateItemPosition(ctx context.Context, membership model.Membership) error {
	return o.q.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
		ItemID:       membership.ItemID,
		ProjectID:    membership.ProjectID,
		ItemPosition: int32(membership.Position),
	})
}

func (o operations) RestoreMembership(ctx context.Context, membership model.Membership) error {
	return o.q.RestoreMembership(ctx, generated.RestoreMembershipParams{
		ItemID:       membership.ItemID,
		ProjectID:    membership.ProjectID,
		ItemPosition: int32(membership.Position),
	})
}

func (o operations) ListAllDependencies(ctx context.Context) ([]model.Dependency, error) {
	rows, err := o.q.ListAllDependencies(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelDependency), nil
}

func (o operations) ListDependenciesInvolvingItem(ctx context.Context, itemID string) ([]model.Dependency, error) {
	rows, err := o.q.ListDependenciesInvolvingItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelDependency), nil
}

func (o operations) ListDependencyItems(ctx context.Context, itemID string) ([]model.ProjectItem, error) {
	rows, err := o.q.ListDependencyItems(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelItem), nil
}

func (o operations) AddDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.AddDependency(ctx, generated.AddDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) RemoveDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.RemoveDependency(ctx, generated.RemoveDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) RestoreDependency(ctx context.Context, edge model.Dependency) error {
	return o.q.RestoreDependency(ctx, generated.RestoreDependencyParams{ItemID: edge.ItemID, DependsOnID: edge.DependsOnID})
}

func (o operations) ListAllTasks(ctx context.Context) ([]model.ProjectItemTask, error) {
	rows, err := o.q.ListAllTasks(ctx)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelTask), nil
}

func (o operations) ListTasksByItem(ctx context.Context, itemID string) ([]model.ProjectItemTask, error) {
	rows, err := o.q.ListTasksByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelTask), nil
}

func (o operations) GetTask(ctx context.Context, id string) (model.ProjectItemTask, error) {
	row, err := o.q.GetTask(ctx, id)
	if err != nil {
		return model.ProjectItemTask{}, rowError(err)
	}
	return toModelTask(row), nil
}

func (o operations) CreateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error) {
	row, err := o.q.CreateTask(ctx, generated.CreateTaskParams{
		ID:           task.ID,
		ItemID:       task.ItemID,
		Title:        task.Title,
		TaskPosition: int32(task.Position),
	})
	if err != nil {
		return model.ProjectItemTask{}, duplicateError(err)
	}
	return toModelTask(row), nil
}

func (o operations) UpdateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error) {
	row, err := o.q.UpdateTask(ctx, generated.UpdateTaskParams{
		ID:           task.ID,
		Title:        task.Title,
		Completed:    task.Completed,
		CompletedAt:  task.CompletedAt,
		TaskPosition: int32(task.Position),
	})
	if err != nil {
		return model.ProjectItemTask{}, rowError(err)
	}
	return toModelTask(row), nil
}

func (o operations) DeleteTask(ctx context.Context, id string) error {
	return o.q.DeleteTask(ctx, id)
}

func (o operations) RestoreTask(ctx context.Context, task model.ProjectItemTask) error {
	return o.q.RestoreTask(ctx, generated.RestoreTaskParams{
		ID:           task.ID,
		ItemID:       task.ItemID,
		Title:        task.Title,
		Completed:    task.Completed,
		CompletedAt:  task.CompletedAt,
		TaskPosition: int32(task.Position),
		CreatedAt:    task.CreatedAt,
	})
}

func (o operations) InsertUndoEntry(ctx context.Context, entry model.UndoEntry) error {
	return o.q.InsertUndoEntry(ctx, generated.InsertUndoEntryParams{
		Action:        entry.Action,
		EntityType:    entry.EntityType,
		EntityID:      entry.EntityID,
		Actor:         entry.Actor,
		Client:        entry.Client,
		PreviousState: entry.PreviousState,
	})
}

func (o operations) GetLatestUndoEntry(ctx context.Context, actor, client string) (model.UndoEntry, error) {
	row, err := o.q.GetLatestUndoEntry(ctx, generated.GetLatestUndoEntryParams{Actor: actor, Client: client})
	if err != nil {
		return model.UndoEntry{}, rowError(err)
	}
	return toModelUndoEntry(row), nil
}

func (o operations) ListUndoEntriesAfter(ctx context.Context, id int64) ([]model.UndoEntry, error) {
	rows, err := o.q.ListUndoEntriesAfter(ctx, id)
	if err != nil {
		return nil, err
	}
	return listOf(rows, toModelUndoEntry), nil
}

func (o operations) DeleteUndoEntry(ctx context.Context, id int64) error {
	return o.q.DeleteUndoEntry(ctx, id)
}

func (o operations) CountUndoEntries(ctx context.Context, actor, client string) (int, error) {
	count, err := o.q.CountUndoEntries(ctx, generated.CountUndoEntriesParams{Actor: actor, Client: client})
	if err != nil {
		return 0, err
	}
	return int(count), nil
}
