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
	// A rule reads what it decides on through tx, so what fn read has to hold when
	// it commits. A store gives that with serializable isolation or by holding the
	// write lock from the start.
	//
	// An error from fn rolls back and returns unchanged, so a sentinel a rule
	// returned still matches. fn gets Operations rather than a Store, so a
	// transaction cannot nest.
	InTx(ctx context.Context, fn func(tx Operations) error) error
}

// Writer names who a transaction writes for. A store that keeps an audit trail
// records it beside each change, and one that keeps none ignores it.
type Writer struct {
	Actor       string
	RequestID   string
	JobRunID    string
	Traceparent string
	Baggage     string
}

type writerKey struct{}

// WithWriter returns a context whose transactions write as writer.
func WithWriter(ctx context.Context, writer Writer) context.Context {
	return context.WithValue(ctx, writerKey{}, writer)
}

// WriterFrom answers the writer a context carries, or the zero Writer.
func WriterFrom(ctx context.Context) Writer {
	writer, _ := ctx.Value(writerKey{}).(Writer)
	return writer
}

// Operations is every read and write the service's rules make. An id here is the
// row's own id, because resolving what a person typed is a rule.
//
// A read of one row answers model.ErrNotFound when the row is absent, and so does
// a write that returns the row it changed. A list answers a non-nil empty slice
// when nothing matches.
type Operations interface {
	// ListProjects orders by position and then name.
	ListProjects(ctx context.Context) ([]model.Project, error)
	// ListProjectItemFacts answers one row per membership, in no promised order.
	ListProjectItemFacts(ctx context.Context) ([]model.ProjectItemFact, error)
	// ListProjectKinds orders by name, and so does ListProjectStatuses.
	ListProjectKinds(ctx context.Context) ([]string, error)
	ListProjectStatuses(ctx context.Context) ([]string, error)
	GetProject(ctx context.Context, id string) (model.Project, error)
	// CreateProject writes every field apart from CreatedAt. A name an active
	// project holds answers model.ErrDuplicateName, and an id already taken answers
	// model.ErrDuplicateID.
	CreateProject(ctx context.Context, project model.Project) (model.Project, error)
	// UpdateProject writes every field apart from CreatedAt, by ID. A name an active
	// project holds answers model.ErrDuplicateName.
	UpdateProject(ctx context.Context, project model.Project) (model.Project, error)
	UpdateProjectPosition(ctx context.Context, id string, position int) error
	// DeleteProject cascades to the project's memberships.
	DeleteProject(ctx context.Context, id string) error
	// RestoreProject inserts the whole row, or overwrites it apart from CreatedAt.
	// Restoring an active project into a name an active project holds answers
	// model.ErrDuplicateName.
	RestoreProject(ctx context.Context, project model.Project) error

	// ListAllItems includes archived items, newest first.
	ListAllItems(ctx context.Context) ([]model.ProjectItem, error)
	// ListItemsInProject includes archived items, ordered by membership position
	// and then creation time. ProjectCount is how many projects hold each.
	ListItemsInProject(ctx context.Context, projectID string) ([]model.ProjectItemInProject, error)
	// ListBlockedItems is every open, unarchived item with an incomplete blocker,
	// newest first.
	ListBlockedItems(ctx context.Context) ([]model.ProjectItem, error)
	// SearchItems matches a substring of the title or notes, ignoring ASCII case,
	// over archived items too, newest first.
	SearchItems(ctx context.Context, query string) ([]model.ProjectItem, error)
	GetItem(ctx context.Context, id string) (model.ProjectItem, error)
	GetItemByNumber(ctx context.Context, number int) (model.ProjectItem, error)
	// NextItemNumber allocates the number for an item about to be created in this
	// transaction. Only a service told it assigns numbers calls it.
	NextItemNumber(ctx context.Context) (int, error)
	// CreateItem writes ID, Number, Title, Notes and Repo. A nil Number stays
	// unset for whatever assigns it later. An id already taken answers
	// model.ErrDuplicateID.
	CreateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error)
	// UpdateItem writes Title, Notes, Repo, Completed, CompletedAt and Archived by
	// ID, and stamps UpdatedAt.
	UpdateItem(ctx context.Context, item model.ProjectItem) (model.ProjectItem, error)
	// DeleteItem cascades to the item's memberships, tasks, and dependencies in
	// both directions.
	DeleteItem(ctx context.Context, id string) error
	// RestoreItem inserts the whole row, or overwrites it apart from CreatedAt.
	RestoreItem(ctx context.Context, item model.ProjectItem) error

	// ListAllMemberships orders by project and then item.
	ListAllMemberships(ctx context.Context) ([]model.Membership, error)
	// ListItemMemberships orders by project.
	ListItemMemberships(ctx context.Context, itemID string) ([]model.Membership, error)
	// ListProjectMemberships orders by position and then item.
	ListProjectMemberships(ctx context.Context, projectID string) ([]model.Membership, error)
	GetMembership(ctx context.Context, itemID, projectID string) (model.Membership, error)
	// GetItemProjects orders by project position, creation time and id.
	GetItemProjects(ctx context.Context, itemID string) ([]model.Project, error)
	AddItemToProject(ctx context.Context, membership model.Membership) error
	RemoveItemFromProject(ctx context.Context, itemID, projectID string) error
	UpdateItemPosition(ctx context.Context, membership model.Membership) error
	// RestoreMembership inserts the row, and leaves an existing pairing alone.
	RestoreMembership(ctx context.Context, membership model.Membership) error

	// ListAllDependencies orders by item and then what it depends on.
	ListAllDependencies(ctx context.Context) ([]model.Dependency, error)
	// ListDependenciesInvolvingItem covers both directions: the item as dependent
	// and the item as blocker.
	ListDependenciesInvolvingItem(ctx context.Context, itemID string) ([]model.Dependency, error)
	// ListDependencyItems answers what the item depends on, finished or not, by
	// number.
	ListDependencyItems(ctx context.Context, itemID string) ([]model.ProjectItem, error)
	AddDependency(ctx context.Context, edge model.Dependency) error
	RemoveDependency(ctx context.Context, edge model.Dependency) error
	// RestoreDependency inserts the edge, and leaves an existing one alone.
	RestoreDependency(ctx context.Context, edge model.Dependency) error

	// ListAllTasks orders by item, position and then creation time.
	ListAllTasks(ctx context.Context) ([]model.ProjectItemTask, error)
	// ListTasksByItem orders by position and then creation time.
	ListTasksByItem(ctx context.Context, itemID string) ([]model.ProjectItemTask, error)
	GetTask(ctx context.Context, id string) (model.ProjectItemTask, error)
	// CreateTask writes ID, ItemID, Title and Position. An id already taken answers
	// model.ErrDuplicateID.
	CreateTask(ctx context.Context, task model.ProjectItemTask) (model.ProjectItemTask, error)
	// UpdateTask writes Title, Completed, CompletedAt and Position by ID.
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
