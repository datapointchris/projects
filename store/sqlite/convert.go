package sqlite

import (
	"database/sql"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite/generated"
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

func toModelProject(p generated.Project) model.Project {
	proj := model.Project{
		ID:        p.ID,
		Name:      p.Name,
		Kind:      p.Kind,
		Status:    p.Status,
		Position:  int(p.Position),
		CreatedAt: parseTime(p.CreatedAt),
	}
	if p.Description.Valid {
		proj.Description = &p.Description.String
	}
	if p.StatusReason.Valid {
		proj.StatusReason = &p.StatusReason.String
	}
	proj.ClosedAt = nullTimeToPointer(p.ClosedAt)
	return proj
}

func toModelProjects(ps []generated.Project) []model.Project {
	out := make([]model.Project, len(ps))
	for i, p := range ps {
		out[i] = toModelProject(p)
	}
	return out
}

func toModelProjectItem(pi generated.ProjectItem) model.ProjectItem {
	item := model.ProjectItem{
		ID:          pi.ID,
		Number:      nullInt64ToIntPtr(pi.Number),
		Title:       pi.Title,
		Completed:   pi.Completed != 0,
		CompletedAt: nullTimeToPointer(pi.CompletedAt),
		Archived:    pi.Archived != 0,
		CreatedAt:   parseTime(pi.CreatedAt),
		UpdatedAt:   parseTime(pi.UpdatedAt),
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

func toModelProjectItemInProject(row generated.ListItemsInProjectRow) model.ProjectItemInProject {
	return model.ProjectItemInProject{
		ProjectItem: toModelProjectItem(generated.ProjectItem{
			ID: row.ID, Number: row.Number, Title: row.Title, Notes: row.Notes, Repo: row.Repo,
			Completed: row.Completed, CompletedAt: row.CompletedAt, Archived: row.Archived,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}),
		Position:     int(row.MembershipPosition),
		ProjectCount: int(row.ProjectCount),
	}
}

func toModelProjectItemTask(t generated.ProjectItemTask) model.ProjectItemTask {
	return model.ProjectItemTask{
		ID:          t.ID,
		ItemID:      t.ItemID,
		Title:       t.Title,
		Completed:   t.Completed != 0,
		CompletedAt: nullTimeToPointer(t.CompletedAt),
		Position:    int(t.Position),
		CreatedAt:   parseTime(t.CreatedAt),
	}
}

func nullTimeToPointer(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	parsed := parseTime(s.String)
	return &parsed
}

func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
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

func toNullInt64(n *int) sql.NullInt64 {
	if n == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*n), Valid: true}
}

// formatTime writes the text the schema's strftime defaults write whenever the
// instant fits it. These columns sort as text, and RFC3339Nano trims trailing
// zeros, so a restored .340Z written as .34Z sorts after a neighboring .343Z.
func formatTime(t time.Time) string {
	if t.Nanosecond()%int(time.Millisecond) == 0 {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.Format(time.RFC3339Nano)
}

func toNullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

func toModelMembership(m generated.ProjectItemMembership) model.Membership {
	return model.Membership{ItemID: m.ItemID, ProjectID: m.ProjectID, Position: int(m.Position)}
}

func toModelMemberships(rows []generated.ProjectItemMembership) []model.Membership {
	out := make([]model.Membership, len(rows))
	for i, r := range rows {
		out[i] = toModelMembership(r)
	}
	return out
}

func toModelDependencies(rows []generated.ProjectItemDependency) []model.Dependency {
	out := make([]model.Dependency, len(rows))
	for i, r := range rows {
		out[i] = model.Dependency{ItemID: r.ItemID, DependsOnID: r.DependsOnID}
	}
	return out
}

func toModelUndoEntry(row generated.UndoLog) model.UndoEntry {
	entry := model.UndoEntry{
		ID:         row.ID,
		Action:     row.Action,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		CreatedAt:  parseTime(row.CreatedAt),
	}
	if row.PreviousState.Valid {
		entry.PreviousState = &row.PreviousState.String
	}
	return entry
}
