package model

import (
	"slices"
	"strings"
	"time"
)

// Project statuses. A project is a finite effort with a definition of done, so
// completing it is what hides it — there is no separate archived flag the way
// items have one. Dropped exists beside Done because Done alone would force you
// to lie about anything you merely stopped caring about, and it always carries a
// reason: "deferred" invites the same idea back, "dropped, and here is why"
// does not.
const (
	StatusActive = "active"
	// StatusCompleted and not "done". The API stores this value behind a foreign
	// key, so a write carrying any other word is rejected. It also matches the
	// `completed` boolean an item carries, so a project and its own items spell
	// one concept one way.
	StatusCompleted = "completed"
	StatusDropped   = "dropped"
	// StatusSomeday is a project set aside rather than finished: hidden like a
	// closed one and giving up its name, but with no close recorded, because
	// the project has not ended.
	StatusSomeday = "someday"

	// StatusAll asks for every project. Not a status a project can be in — it is
	// the absence of the filter, which is why the API keeps it out of its lookup
	// table too.
	StatusAll = "all"
)

// KindLife is the kind a project takes when nothing names one.
const KindLife = "life"

// Project represents a project that items are organized into.
type Project struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	// Kind is one of the names in the project_kinds table.
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status"`
	// Why a project was dropped rather than deferred. Nil for anything still
	// active, and cleared by the server on reopen.
	StatusReason *string `json:"status_reason,omitempty"`
	// When the project reached a terminal status, and nil while it is active or
	// someday. CreatedAt orders by when the project started, which is the wrong
	// axis for a list of finished things.
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	Position  int        `json:"position"`
	CreatedAt time.Time  `json:"created_at"`
}

// ProjectKinds and ProjectStatuses are the rows each store's lookup table holds. A new
// status also takes a decision in ProjectIsTerminal and SortProjectsForBoard, whose
// defaults otherwise treat it as open and as closed.
var (
	ProjectKinds    = []string{"build", "chore", "life"}
	ProjectStatuses = []string{StatusActive, StatusSomeday, StatusCompleted, StatusDropped}
)

// ProjectIsTerminal reports whether a status closes the project it is on.
func ProjectIsTerminal(status string) bool {
	return status == StatusCompleted || status == StatusDropped
}

// ProjectWithItemCount is a Project with what its items add up to. The counts
// partition ItemCount: archived beats completed, so an archived item is neither
// open nor completed and is what ItemCount has beyond the other two.
type ProjectWithItemCount struct {
	Project
	ItemCount      int `json:"item_count"`
	OpenCount      int `json:"open_count"`
	CompletedCount int `json:"completed_count"`
	// Repos is derived from the items rather than stored: an item's repo tag is
	// the one record of what code a piece of work touches.
	Repos []string `json:"repos"`
}

// UnarchivedCount is the items a project still shows, open or finished.
func (p ProjectWithItemCount) UnarchivedCount() int {
	return p.OpenCount + p.CompletedCount
}

// ProjectItemFact is what one membership adds to its project's counts.
type ProjectItemFact struct {
	ProjectID string
	Completed bool
	Archived  bool
	Repo      *string
}

// SortProjectsForBoard orders projects the way a board shows them: active first,
// then someday, then closed, each newest close first, then by position and name.
// A someday project has no closing date, so ordering on the date alone would mix
// it in among the active ones.
func SortProjectsForBoard(projects []ProjectWithItemCount) {
	statusRank := func(status string) int {
		switch status {
		case StatusActive:
			return 0
		case StatusSomeday:
			return 1
		default:
			return 2
		}
	}
	slices.SortStableFunc(projects, func(a, b ProjectWithItemCount) int {
		if byStatus := statusRank(a.Status) - statusRank(b.Status); byStatus != 0 {
			return byStatus
		}
		if byClose := CompareNewestCloseFirst(a.ClosedAt, b.ClosedAt); byClose != 0 {
			return byClose
		}
		if byPosition := a.Position - b.Position; byPosition != 0 {
			return byPosition
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// CompareNewestCloseFirst puts a project with no closing date ahead of any closed
// one, and the most recently closed next.
func CompareNewestCloseFirst(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return b.Compare(*a)
}

// ProjectFilter narrows a list of projects. Status is a status name or StatusAll.
// Repo keeps projects holding at least one item tagged with it. A nil Limit caps
// nothing, and zero returns no rows.
type ProjectFilter struct {
	Status string
	Repo   *string
	Limit  *int
}

// CreateProject is the input for creating a new project. Only Name is required.
// A nil ID is generated, a nil Kind is KindLife and a nil Status is StatusActive.
// A nil Position places the project after every other.
type CreateProject struct {
	ID           *string `json:"id,omitempty"`
	Name         string  `json:"name"`
	Description  *string `json:"description,omitempty"`
	Kind         *string `json:"kind,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
	Position     *int    `json:"position,omitempty"`
}

// UpdateProject is the input for updating a project. Only non-nil fields are
// applied. A pointer cannot say "set this to nothing", so the Clear flags do.
type UpdateProject struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Kind         *string `json:"kind,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
	Position     *int    `json:"position,omitempty"`

	ClearDescription  bool `json:"-"`
	ClearStatusReason bool `json:"-"`
}
