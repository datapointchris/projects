package model

import "errors"

var (
	ErrNotFound         = errors.New("not found")
	ErrDuplicateName    = errors.New("duplicate name")
	ErrDuplicateID      = errors.New("id already exists")
	ErrCyclicDependency = errors.New("would create a cyclic dependency")
	ErrLastProject      = errors.New("item must belong to at least one project")
	ErrNoProjects       = errors.New("a new item needs a project")
	ErrNothingToUndo    = errors.New("nothing to undo")
	// An undo reverses its own writer's write. A later entry by another writer
	// changed a row the undo would change, so carrying it out would revert theirs.
	ErrUndoSuperseded = errors.New("a later write by another writer changed what the undo would change")
	// A dropped project with no reason is indistinguishable from a deferred one,
	// and deferred invites the same idea back next month.
	ErrDropReasonRequired = errors.New("dropping a project requires a reason")
	// A project name must be bounded work. A repo does not end, so a project
	// named after one becomes the bucket every later papercut falls into.
	ErrRepoNamedProject = errors.New("a project may not be named after a repo")
	// A project is addressed by name in a path, so a name ending in the item-list
	// segment would read as that list.
	ErrReservedName = errors.New("a project name may not end in the item-list segment")
	// Several closed projects can share a name, and nothing picks between them.
	ErrAmbiguousName       = errors.New("name matches several closed projects")
	ErrInvalidReference    = errors.New("not an item id or number")
	ErrUnknownKind         = errors.New("unknown project kind")
	ErrUnknownStatus       = errors.New("unknown project status")
	ErrUnknownItemStatus   = errors.New("unknown item status")
	ErrInvertedWindow      = errors.New("window starts after it ends")
	ErrAlreadyInProject    = errors.New("item is already in that project")
	ErrDuplicateDependency = errors.New("dependency already exists")
	ErrSelfDependency      = errors.New("an item cannot depend on itself")
	ErrIncompleteOrphans   = errors.New("project holds incomplete items no other project holds")
	ErrIncompleteTasks     = errors.New("item has incomplete tasks")
)

// Refusal is a rule saying no, with the sentence a person reads. Kind is the
// sentinel a caller matches, so errors.Is sees through it to one of the errors
// above. Field names the input field at fault, where one input is, and Input holds
// what it was sent.
type Refusal struct {
	Kind    error
	Message string
	Field   string
	Input   any
}

func (r *Refusal) Error() string { return r.Message }

func (r *Refusal) Unwrap() error { return r.Kind }
