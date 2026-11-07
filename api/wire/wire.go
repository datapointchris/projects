// Package wire is what the API answers with. It is apart from model because a
// client depends on this contract and never on the server's types: every field of a
// row is always sent, an empty value as null and an empty list as [], so a reader
// indexing by name finds it, and a change to model cannot change an answer by
// accident. Each struct has its JSON Schema under api/schemas.
package wire

import (
	"fmt"
	"time"

	"github.com/datapointchris/projects/model"
)

// Time prints an instant the way clients have always read it: UTC with a Z, six
// fractional digits when there is a fraction, and none when there is not.
type Time struct{ time.Time }

func (t Time) MarshalJSON() ([]byte, error) {
	utc := t.UTC()
	layout := "2006-01-02T15:04:05Z"
	if utc.Nanosecond()/1000 != 0 {
		layout = "2006-01-02T15:04:05.000000Z"
	}
	return fmt.Appendf(nil, "%q", utc.Format(layout)), nil
}

func optionalTime(t *time.Time) *Time {
	if t == nil {
		return nil
	}
	return &Time{*t}
}

// Project is one project.
type Project struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	Kind         string  `json:"kind"`
	Status       string  `json:"status"`
	StatusReason *string `json:"status_reason"`
	ClosedAt     *Time   `json:"closed_at"`
	Position     int     `json:"position"`
	CreatedAt    Time    `json:"created_at"`
}

// NewProject is the wire form of a project.
func NewProject(p model.Project) Project {
	return Project{
		ID:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		Kind:         p.Kind,
		Status:       p.Status,
		StatusReason: p.StatusReason,
		ClosedAt:     optionalTime(p.ClosedAt),
		Position:     p.Position,
		CreatedAt:    Time{p.CreatedAt},
	}
}

// ProjectWithItemCount is a project and what its items add up to. The counts
// partition ItemCount: archived beats completed, so an archived item is what
// ItemCount holds beyond OpenCount and CompletedCount.
type ProjectWithItemCount struct {
	Project
	ItemCount      int      `json:"item_count"`
	OpenCount      int      `json:"open_count"`
	CompletedCount int      `json:"completed_count"`
	Repos          []string `json:"repos"`
}

// NewProjectWithItemCount is the wire form of a counted project.
func NewProjectWithItemCount(p model.ProjectWithItemCount) ProjectWithItemCount {
	return ProjectWithItemCount{
		Project:        NewProject(p.Project),
		ItemCount:      p.ItemCount,
		OpenCount:      p.OpenCount,
		CompletedCount: p.CompletedCount,
		Repos:          nonNil(p.Repos),
	}
}

// ProjectItemMembership is where an item sits in one of its projects.
type ProjectItemMembership struct {
	ProjectID string `json:"project_id"`
	Position  int    `json:"position"`
}

// ProjectItemTask is one task on an item.
type ProjectItemTask struct {
	ID          string `json:"id"`
	ItemID      string `json:"item_id"`
	Title       string `json:"title"`
	Completed   bool   `json:"completed"`
	CompletedAt *Time  `json:"completed_at"`
	Position    int    `json:"position"`
	CreatedAt   Time   `json:"created_at"`
}

// NewProjectItemTask is the wire form of a task.
func NewProjectItemTask(t model.ProjectItemTask) ProjectItemTask {
	return ProjectItemTask{
		ID:          t.ID,
		ItemID:      t.ItemID,
		Title:       t.Title,
		Completed:   t.Completed,
		CompletedAt: optionalTime(t.CompletedAt),
		Position:    t.Position,
		CreatedAt:   Time{t.CreatedAt},
	}
}

// ProjectItem is an item with what is filed against it, the shape a list returns,
// so a client reconciling the whole list needs no request per item.
type ProjectItem struct {
	ID            string                  `json:"id"`
	Number        *int                    `json:"number"`
	Title         string                  `json:"title"`
	Notes         *string                 `json:"notes"`
	Repo          *string                 `json:"repo"`
	Completed     bool                    `json:"completed"`
	CompletedAt   *Time                   `json:"completed_at"`
	Archived      bool                    `json:"archived"`
	Status        string                  `json:"status"`
	CreatedAt     Time                    `json:"created_at"`
	UpdatedAt     Time                    `json:"updated_at"`
	Projects      []Project               `json:"projects"`
	Memberships   []ProjectItemMembership `json:"memberships"`
	DependencyIDs []string                `json:"dependency_ids"`
	Tasks         []ProjectItemTask       `json:"tasks"`
}

// NewProjectItem is the wire form of an item with what is filed against it.
func NewProjectItem(d model.ProjectItemDetail) ProjectItem {
	return ProjectItem{
		ID:            d.ID,
		Number:        d.Number,
		Title:         d.Title,
		Notes:         d.Notes,
		Repo:          d.Repo,
		Completed:     d.Completed,
		CompletedAt:   optionalTime(d.CompletedAt),
		Archived:      d.Archived,
		Status:        d.Status(),
		CreatedAt:     Time{d.CreatedAt},
		UpdatedAt:     Time{d.UpdatedAt},
		Projects:      Map(d.Projects, NewProject),
		Memberships:   Map(d.Memberships, newProjectItemMembership),
		DependencyIDs: nonNil(d.DependencyIDs),
		Tasks:         Map(d.Tasks, NewProjectItemTask),
	}
}

func newProjectItemMembership(m model.Membership) ProjectItemMembership {
	return ProjectItemMembership{ProjectID: m.ProjectID, Position: m.Position}
}

// ProjectItemSummary is enough of another item to print it beside this one without
// a request of its own.
type ProjectItemSummary struct {
	ID        string `json:"id"`
	Number    *int   `json:"number"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	Archived  bool   `json:"archived"`
	Status    string `json:"status"`
}

func newProjectItemSummary(i model.ProjectItem) ProjectItemSummary {
	return ProjectItemSummary{ID: i.ID, Number: i.Number, Title: i.Title, Completed: i.Completed, Archived: i.Archived, Status: i.Status()}
}

// ProjectItemDetail is one item read alone. It names what the item waits on, and
// its tasks are their own route.
type ProjectItemDetail struct {
	ID            string                  `json:"id"`
	Number        *int                    `json:"number"`
	Title         string                  `json:"title"`
	Notes         *string                 `json:"notes"`
	Repo          *string                 `json:"repo"`
	Completed     bool                    `json:"completed"`
	CompletedAt   *Time                   `json:"completed_at"`
	Archived      bool                    `json:"archived"`
	Status        string                  `json:"status"`
	CreatedAt     Time                    `json:"created_at"`
	UpdatedAt     Time                    `json:"updated_at"`
	Projects      []Project               `json:"projects"`
	Memberships   []ProjectItemMembership `json:"memberships"`
	DependencyIDs []string                `json:"dependency_ids"`
	DependsOn     []ProjectItemSummary    `json:"depends_on"`
}

// NewProjectItemDetail is the wire form of one item read alone.
func NewProjectItemDetail(d model.ProjectItemDetail) ProjectItemDetail {
	return ProjectItemDetail{
		ID:            d.ID,
		Number:        d.Number,
		Title:         d.Title,
		Notes:         d.Notes,
		Repo:          d.Repo,
		Completed:     d.Completed,
		CompletedAt:   optionalTime(d.CompletedAt),
		Archived:      d.Archived,
		Status:        d.Status(),
		CreatedAt:     Time{d.CreatedAt},
		UpdatedAt:     Time{d.UpdatedAt},
		Projects:      Map(d.Projects, NewProject),
		Memberships:   Map(d.Memberships, newProjectItemMembership),
		DependencyIDs: nonNil(d.DependencyIDs),
		DependsOn:     Map(d.DependsOn, newProjectItemSummary),
	}
}

// ProjectItemInProject is an item as one project holds it. ProjectCount is how many
// projects hold it, which is what a client needs to refuse removing it from its
// last one before asking.
type ProjectItemInProject struct {
	ID           string  `json:"id"`
	Number       *int    `json:"number"`
	Title        string  `json:"title"`
	Notes        *string `json:"notes"`
	Repo         *string `json:"repo"`
	Completed    bool    `json:"completed"`
	CompletedAt  *Time   `json:"completed_at"`
	Archived     bool    `json:"archived"`
	Status       string  `json:"status"`
	CreatedAt    Time    `json:"created_at"`
	UpdatedAt    Time    `json:"updated_at"`
	Position     int     `json:"position"`
	ProjectCount int     `json:"project_count"`
}

// NewProjectItemInProject is the wire form of an item as one project holds it.
func NewProjectItemInProject(i model.ProjectItemInProject) ProjectItemInProject {
	return ProjectItemInProject{
		ID:           i.ID,
		Number:       i.Number,
		Title:        i.Title,
		Notes:        i.Notes,
		Repo:         i.Repo,
		Completed:    i.Completed,
		CompletedAt:  optionalTime(i.CompletedAt),
		Archived:     i.Archived,
		Status:       i.Status(),
		CreatedAt:    Time{i.CreatedAt},
		UpdatedAt:    Time{i.UpdatedAt},
		Position:     i.Position,
		ProjectCount: i.ProjectCount,
	}
}

// UndoResult says what an undo reversed.
type UndoResult struct {
	Description string `json:"description"`
	Action      string `json:"action"`
	EntityType  string `json:"entity_type"`
	EntityID    string `json:"entity_id"`
}

// NewUndoResult is the wire form of an undo.
func NewUndoResult(r model.UndoResult) UndoResult {
	return UndoResult{Description: r.Description, Action: r.Action, EntityType: r.EntityType, EntityID: r.EntityID}
}

// UndoState says whether anything is left to undo.
type UndoState struct {
	CanUndo bool `json:"can_undo"`
}

// ValidationError is one refused input, in the shape clients mark fields from.
type ValidationError struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

// Error is every refusal. Message always says why in one string. Errors lists each
// refused input on a validation 422.
type Error struct {
	Error   string            `json:"error"`
	Message string            `json:"message"`
	Errors  []ValidationError `json:"errors,omitempty"`
}

// Probe answers /health and /ready. Reason says what is not ready, where something
// is not.
type Probe struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Map converts each row, and answers an empty list rather than null for none.
func Map[From, To any](rows []From, convert func(From) To) []To {
	out := make([]To, len(rows))
	for i, row := range rows {
		out[i] = convert(row)
	}
	return out
}

func nonNil(rows []string) []string {
	if rows == nil {
		return []string{}
	}
	return rows
}
