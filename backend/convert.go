package backend

import (
	"database/sql"
	"time"

	"github.com/datapointchris/projects/db/generated"
	"github.com/datapointchris/projects/model"
)

func parseTime(s string) time.Time {
	// Try RFC3339Nano first (ISO 8601 from API), then SQLite format as fallback
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}

// projectFields is what every project-shaped row has in common. sqlc generates a
// distinct struct per query, so without this the same eight-field mapping is
// written three times and only two of them get updated when a column is added.
type projectFields struct {
	ID           string
	Name         string
	Description  sql.NullString
	Status       string
	StatusReason sql.NullString
	ClosedAt     sql.NullString
	Position     int64
	CreatedAt    string
}

func toModelProjectFields(f projectFields) model.Project {
	proj := model.Project{
		ID:        f.ID,
		Name:      f.Name,
		Status:    f.Status,
		Position:  int(f.Position),
		CreatedAt: parseTime(f.CreatedAt),
	}
	if f.Description.Valid {
		proj.Description = &f.Description.String
	}
	if f.StatusReason.Valid {
		proj.StatusReason = &f.StatusReason.String
	}
	if f.ClosedAt.Valid {
		closed := parseTime(f.ClosedAt.String)
		proj.ClosedAt = &closed
	}
	return proj
}

func toModelProject(p generated.Project) model.Project {
	return toModelProjectFields(projectFields{
		ID: p.ID, Name: p.Name, Description: p.Description,
		Status: p.Status, StatusReason: p.StatusReason, ClosedAt: p.ClosedAt,
		Position: p.Position, CreatedAt: p.CreatedAt,
	})
}

func toModelProjects(ps []generated.Project) []model.Project {
	out := make([]model.Project, len(ps))
	for i, p := range ps {
		out[i] = toModelProject(p)
	}
	return out
}

func toModelProjectWithItemCount(row generated.ListProjectsWithItemCountRow) model.ProjectWithItemCount {
	return model.ProjectWithItemCount{
		Project: toModelProjectFields(projectFields{
			ID: row.ID, Name: row.Name, Description: row.Description,
			Status: row.Status, StatusReason: row.StatusReason, ClosedAt: row.ClosedAt,
			Position: row.Position, CreatedAt: row.CreatedAt,
		}),
		ItemCount:      int(row.ItemCount),
		CompletedCount: int(row.CompletedCount),
	}
}

func toModelProjectWithItemCountFromGet(row generated.GetProjectWithItemCountRow) model.ProjectWithItemCount {
	return model.ProjectWithItemCount{
		Project: toModelProjectFields(projectFields{
			ID: row.ID, Name: row.Name, Description: row.Description,
			Status: row.Status, StatusReason: row.StatusReason, ClosedAt: row.ClosedAt,
			Position: row.Position, CreatedAt: row.CreatedAt,
		}),
		ItemCount:      int(row.ItemCount),
		CompletedCount: int(row.CompletedCount),
	}
}

func toModelProjectItem(pi generated.ProjectItem) model.ProjectItem {
	item := model.ProjectItem{
		ID:        pi.ID,
		Number:    nullInt64ToIntPtr(pi.Number),
		Title:     pi.Title,
		Completed: pi.Completed != 0,
		Archived:  pi.Archived != 0,
		CreatedAt: parseTime(pi.CreatedAt),
		UpdatedAt: parseTime(pi.UpdatedAt),
	}
	if pi.Notes.Valid {
		item.Notes = &pi.Notes.String
	}
	if pi.Repo.Valid {
		item.Repo = &pi.Repo.String
	}
	return item
}

func toModelProjectItems(pis []generated.ProjectItem) []model.ProjectItem {
	out := make([]model.ProjectItem, len(pis))
	for i, pi := range pis {
		out[i] = toModelProjectItem(pi)
	}
	return out
}

func toModelProjectItemInProject(row generated.ListItemsByProjectRow) model.ProjectItemInProject {
	item := model.ProjectItem{
		ID:        row.ID,
		Number:    nullInt64ToIntPtr(row.Number),
		Title:     row.Title,
		Completed: row.Completed != 0,
		Archived:  row.Archived != 0,
		CreatedAt: parseTime(row.CreatedAt),
		UpdatedAt: parseTime(row.UpdatedAt),
	}
	if row.Notes.Valid {
		item.Notes = &row.Notes.String
	}
	if row.Repo.Valid {
		item.Repo = &row.Repo.String
	}
	return model.ProjectItemInProject{
		ProjectItem:  item,
		Position:     int(row.MembershipPosition),
		ProjectCount: int(row.ProjectCount),
	}
}

func toModelProjectItemInProjectFromArchived(row generated.ListArchivedItemsRow) model.ProjectItemInProject {
	item := model.ProjectItem{
		ID:        row.ID,
		Number:    nullInt64ToIntPtr(row.Number),
		Title:     row.Title,
		Completed: row.Completed != 0,
		Archived:  row.Archived != 0,
		CreatedAt: parseTime(row.CreatedAt),
		UpdatedAt: parseTime(row.UpdatedAt),
	}
	if row.Notes.Valid {
		item.Notes = &row.Notes.String
	}
	if row.Repo.Valid {
		item.Repo = &row.Repo.String
	}
	return model.ProjectItemInProject{
		ProjectItem: item,
		Position:    int(row.MembershipPosition),
	}
}

func toModelProjectItemTask(t generated.ProjectItemTask) model.ProjectItemTask {
	return model.ProjectItemTask{
		ID:        t.ID,
		ItemID:    t.ItemID,
		Title:     t.Title,
		Completed: t.Completed != 0,
		Position:  int(t.Position),
		CreatedAt: parseTime(t.CreatedAt),
	}
}

func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// repoToNullString stores an empty repo as NULL rather than "". A repo link is
// either a registry name or absent — "" is neither, and it would render as an
// empty "Repo:" line and never match a repo filter. The API server's own CLI
// reads an empty repo flag the same way, as an unlink, because a flag cannot
// send a JSON null.
func repoToNullString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func boolToInt64(b *bool) int64 {
	if b == nil || !*b {
		return 0
	}
	return 1
}

// nullInt64ToIntPtr converts a nullable number column to the model's optional
// int. Null means the item has not been given a number yet, which is different
// from having the number zero.
func nullInt64ToIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
