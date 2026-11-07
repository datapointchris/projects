package postgres

import (
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/postgres/generated"
)

// listOf converts every row, and answers an empty list rather than nil.
func listOf[Row, Model any](rows []Row, convert func(Row) Model) []Model {
	out := make([]Model, len(rows))
	for i, row := range rows {
		out[i] = convert(row)
	}
	return out
}

func utcPointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	inUTC := t.UTC()
	return &inUTC
}

func toModelProject(p generated.Project) model.Project {
	return model.Project{
		ID:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		Kind:         p.Kind,
		Status:       p.Status,
		StatusReason: p.StatusReason,
		ClosedAt:     utcPointer(p.ClosedAt),
		Position:     int(p.Position),
		CreatedAt:    p.CreatedAt.UTC(),
	}
}

func toModelItem(i generated.ProjectItem) model.ProjectItem {
	number := int(i.Number)
	return model.ProjectItem{
		ID:          i.ID,
		Number:      &number,
		Title:       i.Title,
		Notes:       i.Notes,
		Repo:        i.Repo,
		Completed:   i.Completed,
		CompletedAt: utcPointer(i.CompletedAt),
		Archived:    i.Archived,
		CreatedAt:   i.CreatedAt.UTC(),
		UpdatedAt:   i.UpdatedAt.UTC(),
	}
}

func toModelItemInProject(r generated.ListItemsInProjectRow) model.ProjectItemInProject {
	return model.ProjectItemInProject{
		ProjectItem: toModelItem(generated.ProjectItem{
			ID: r.ID, Number: r.Number, Title: r.Title, Notes: r.Notes, Repo: r.Repo,
			Completed: r.Completed, CompletedAt: r.CompletedAt, Archived: r.Archived,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}),
		Position:     int(r.MembershipPosition),
		ProjectCount: int(r.ProjectCount),
	}
}

func toModelMembership(m generated.ProjectItemMembership) model.Membership {
	return model.Membership{ItemID: m.ItemID, ProjectID: m.ProjectID, Position: int(m.Position)}
}

func toModelDependency(d generated.ProjectItemDependency) model.Dependency {
	return model.Dependency{ItemID: d.ItemID, DependsOnID: d.DependsOnID}
}

func toModelTask(t generated.ProjectItemTask) model.ProjectItemTask {
	return model.ProjectItemTask{
		ID:          t.ID,
		ItemID:      t.ItemID,
		Title:       t.Title,
		Completed:   t.Completed,
		CompletedAt: utcPointer(t.CompletedAt),
		Position:    int(t.Position),
		CreatedAt:   t.CreatedAt.UTC(),
	}
}
