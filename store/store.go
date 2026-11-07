// Package store is the seam between the service's rules and a database. Every
// method takes and returns model types, so a store's generated code stays inside
// its own package.
package store

import (
	"context"

	"github.com/datapointchris/projects/model"
)

// Store is a database the service can run its rules over.
type Store interface {
	Operations

	// InTx runs fn inside one transaction, so a mutation and its undo entry commit
	// together. Split apart, a failure between them leaves either an undo entry
	// for a change that never happened or a change nothing can reverse.
	//
	// An error from fn rolls back and returns unchanged, so a sentinel a rule
	// returned still matches. fn gets Operations rather than a Store, so a
	// transaction cannot nest.
	InTx(ctx context.Context, fn func(tx Operations) error) error
}

// Operations is every read and write the service's rules make.
//
// A read of one row answers model.ErrNotFound when the row is absent, and so does
// a write that returns the row it changed. A list answers a non-nil empty slice
// when nothing matches, apart from GetDependencyIDs.
type Operations interface {
	// ListProjectsWithItemCount takes a status name or model.StatusAll. Counts
	// leave out archived items. Active projects come first, then someday, then
	// closed, each newest close first, then by position and name.
	ListProjectsWithItemCount(ctx context.Context, status string) ([]model.ProjectWithItemCount, error)
	GetProjectWithItemCount(ctx context.Context, id string) (model.ProjectWithItemCount, error)
	GetProject(ctx context.Context, id string) (model.Project, error)
	// CreateProject writes ID, Name and Description, and places the project after
	// every other. A name an active project holds answers model.ErrDuplicateName.
	CreateProject(ctx context.Context, project model.Project) (model.Project, error)
	// UpdateProject writes Name, Description and Position by ID.
	UpdateProject(ctx context.Context, project model.Project) (model.Project, error)
	// SetProjectStatus derives closed_at and status_reason from the transition.
	// Active and someday clear closed_at, and active clears the reason. A project
	// already closed in this status keeps its date, and any other close stamps
	// now. Reopening into a name a live project holds answers
	// model.ErrDuplicateName.
	SetProjectStatus(ctx context.Context, id, status string, reason *string) (model.Project, error)
	UpdateProjectPosition(ctx context.Context, id string, position int) error
	// DeleteProject cascades to the project's memberships.
	DeleteProject(ctx context.Context, id string) error
	// RestoreProject inserts the whole row, or overwrites it apart from CreatedAt.
	RestoreProject(ctx context.Context, project model.Project) error

	// ListAllItems includes archived items, newest first.
	ListAllItems(ctx context.Context) ([]model.ProjectItem, error)
	// ListItemsByProject leaves out archived items, ordered by membership position
	// and then creation time.
	ListItemsByProject(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error)
	// ListArchivedItems orders by most recently updated.
	ListArchivedItems(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error)
	// ListBlockedItems is every open, unarchived item with an incomplete blocker.
	ListBlockedItems(ctx context.Context) ([]model.ProjectItem, error)
	// SearchItems matches a substring of the title or notes, ignoring ASCII case,
	// over unarchived items, newest first.
	SearchItems(ctx context.Context, query string) ([]model.ProjectItem, error)
	GetItem(ctx context.Context, id string) (model.ProjectItem, error)
	// NextItemNumber allocates the number for an item about to be created in this
	// transaction. Only a service told it assigns numbers calls it.
	NextItemNumber(ctx context.Context) (int, error)
	// CreateItem writes ID, Number, Title, Notes and Repo. A nil Number stays
	// unset for whatever assigns it later.
	CreateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error)
	// UpdateItem writes Title, Notes, Repo, Completed and Archived by ID, and
	// stamps UpdatedAt.
	UpdateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error)
	// DeleteItem cascades to the item's memberships, tasks, and dependencies in
	// both directions.
	DeleteItem(ctx context.Context, id string) error
	// RestoreItem inserts the whole row, or overwrites it apart from CreatedAt.
	RestoreItem(ctx context.Context, item model.ProjectItem) error

	ListAllMemberships(ctx context.Context) ([]model.Membership, error)
	ListItemMemberships(ctx context.Context, itemID string) ([]model.Membership, error)
	ListProjectMemberships(ctx context.Context, projectID string) ([]model.Membership, error)
	GetMembership(ctx context.Context, itemID, projectID string) (model.Membership, error)
	// GetItemProjects orders by project name.
	GetItemProjects(ctx context.Context, itemID string) ([]model.Project, error)
	// AddItemToProject places the item after every other in that project.
	AddItemToProject(ctx context.Context, itemID, projectID string) error
	RemoveItemFromProject(ctx context.Context, itemID, projectID string) error
	UpdateItemPosition(ctx context.Context, membership model.Membership) error
	// RestoreMembership inserts the row, and leaves an existing pairing alone.
	RestoreMembership(ctx context.Context, membership model.Membership) error

	ListAllDependencies(ctx context.Context) ([]model.Dependency, error)
	// ListDependenciesInvolvingItem covers both directions: the item as dependent
	// and the item as blocker.
	ListDependenciesInvolvingItem(ctx context.Context, itemID string) ([]model.Dependency, error)
	// GetDependencyIDs may answer nil when the item has no blockers.
	GetDependencyIDs(ctx context.Context, itemID string) ([]string, error)
	// GetBlockers answers the item's incomplete blockers.
	GetBlockers(ctx context.Context, itemID string) ([]model.ProjectItem, error)
	AddDependency(ctx context.Context, edge model.Dependency) error
	RemoveDependency(ctx context.Context, edge model.Dependency) error
	// RestoreDependency inserts the edge, and leaves an existing one alone.
	RestoreDependency(ctx context.Context, edge model.Dependency) error

	// ListTasksByItem orders by position and then creation time.
	ListTasksByItem(ctx context.Context, itemID string) ([]model.ProjectItemTask, error)
	GetTask(ctx context.Context, id string) (model.ProjectItemTask, error)
	// CreateTask writes ID, ItemID and Title, and places the task after every
	// other on that item.
	CreateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error)
	// UpdateTask writes Title, Completed and Position by ID.
	UpdateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error)
	DeleteTask(ctx context.Context, id string) error
	// RestoreTask inserts the whole row, or overwrites it apart from ItemID and
	// CreatedAt.
	RestoreTask(ctx context.Context, task model.ProjectItemTask) error

	// InsertUndoEntry writes Action, EntityType, EntityID and PreviousState. The
	// store assigns ID, which rises with each entry, and CreatedAt.
	InsertUndoEntry(ctx context.Context, entry model.UndoEntry) error
	// GetLatestUndoEntry answers the entry with the highest ID.
	GetLatestUndoEntry(ctx context.Context) (model.UndoEntry, error)
	DeleteUndoEntry(ctx context.Context, id int64) error
	CountUndoEntries(ctx context.Context) (int, error)
}
