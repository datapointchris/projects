package model

import (
	"slices"
	"time"
)

// An item's status is derived from two stored booleans. These three words and
// StatusAll are the filter's whole vocabulary.
const (
	ItemStatusOpen      = "open"
	ItemStatusCompleted = "completed"
	ItemStatusArchived  = "archived"
)

// ProjectItem is the base representation of an item in the system.
type ProjectItem struct {
	ID string `json:"id"`
	// Number is the handle — short, server-assigned, and the only one of the two
	// identifiers meant to be read or typed. Nil for an item created while sync
	// was unreachable, which has no number until its create is pushed.
	Number    *int    `json:"number,omitempty"`
	Title     string  `json:"title"`
	Notes     *string `json:"notes,omitempty"`
	Repo      *string `json:"repo,omitempty"`
	Completed bool    `json:"completed"`
	// CompletedAt is nil where the completion time is unknown: never finished, or
	// finished by a write that recorded none.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Archived    bool       `json:"archived"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Status names the one word an item answers to. Archived beats completed, so an
// item finished and then archived answers to archived alone.
func (i ProjectItem) Status() string {
	switch {
	case i.Archived:
		return ItemStatusArchived
	case i.Completed:
		return ItemStatusCompleted
	default:
		return ItemStatusOpen
	}
}

// ProjectItemDetail is a ProjectItem with everything filed against it.
//
// DependencyIDs and Tasks decode to nil when a server omits the field and to an
// empty slice when it sends one, which is the only way a pull can tell a server
// predating the embed from an item that genuinely has neither. Keep them as plain
// slices and keep the API sending `[]`; anything that collapses nil and empty
// makes an old server look like an instruction to delete every task.
type ProjectItemDetail struct {
	ProjectItem
	Projects      []Project         `json:"projects"`
	Memberships   []Membership      `json:"memberships,omitempty"`
	DependencyIDs []string          `json:"dependency_ids"`
	DependsOn     []ProjectItem     `json:"depends_on,omitempty"`
	Tasks         []ProjectItemTask `json:"tasks"`
}

// ProjectItemInProject is a ProjectItem as seen within a specific project context,
// including its position within that project.
type ProjectItemInProject struct {
	ProjectItem
	Position     int `json:"position"`
	ProjectCount int `json:"project_count,omitempty"`
}

func (i ProjectItem) archived() bool { return i.Archived }

// WithoutArchived drops the archived items, which the board's item lists and its
// search leave out.
func WithoutArchived[T interface{ archived() bool }](items []T) []T {
	return slices.DeleteFunc(items, func(item T) bool { return item.archived() })
}

// SortArchivedForBoard orders archived items most recently updated first, as the
// board lists them.
func SortArchivedForBoard(items []ProjectItemInProject) {
	slices.SortStableFunc(items, func(a, b ProjectItemInProject) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
}

// ItemFilter narrows a list of items. Status is an item status or StatusAll. An
// empty Repo keeps the untagged items, and a nil one keeps every item. Since and
// Until bound CompletedAt, half-open, and an item with none is outside any
// window. A nil Limit caps nothing, and zero returns no rows.
type ItemFilter struct {
	Status string
	Repo   *string
	Since  *time.Time
	Until  *time.Time
	Limit  *int
}

// CreateProjectItem is the input for creating a new project item. ProjectIDs each
// hold an id or a name. A nil ID is generated.
type CreateProjectItem struct {
	ID         *string  `json:"id,omitempty"`
	Title      string   `json:"title"`
	Notes      *string  `json:"notes,omitempty"`
	Repo       *string  `json:"repo,omitempty"`
	ProjectIDs []string `json:"project_ids"`
}

// UpdateProjectItem is the input for updating an existing project item. Only
// non-nil fields are applied. A pointer cannot say "set this to nothing", so the
// Clear flags do.
type UpdateProjectItem struct {
	Title     *string `json:"title,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	Repo      *string `json:"repo,omitempty"`
	Completed *bool   `json:"completed,omitempty"`
	Archived  *bool   `json:"archived,omitempty"`

	ClearNotes bool `json:"-"`
	ClearRepo  bool `json:"-"`
}
