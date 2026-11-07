package service

import (
	"time"

	"github.com/datapointchris/projects/model"
)

// The types below are the JSON an undo entry's previous_state holds. Entries are
// never pruned, so this shape is a format on disk: a field renamed here strands
// every entry already written. Its nullable fields read {"String":…,"Valid":…}
// because that is the JSON a nullable SQL column's Go type marshals to, and the
// oldest entries hold exactly that. No store reads any of it.

type nullableText struct {
	String string
	Valid  bool
}

type nullableInt struct {
	Int64 int64
	Valid bool
}

type projectRecord struct {
	ID           string
	Name         string
	Description  nullableText
	Status       string
	StatusReason nullableText
	ClosedAt     nullableText
	Position     int64
	CreatedAt    string
}

type itemRecord struct {
	ID        string
	Number    nullableInt
	Title     string
	Notes     nullableText
	Repo      nullableText
	Completed int64
	Archived  int64
	CreatedAt string
	UpdatedAt string
}

type membershipRecord struct {
	ItemID    string
	ProjectID string
	Position  int64
}

type dependencyRecord struct {
	ItemID      string
	DependsOnID string
}

type taskRecord struct {
	ID        string
	ItemID    string
	Title     string
	Completed int64
	Position  int64
	CreatedAt string
}

func projectRecordOf(p model.Project) projectRecord {
	return projectRecord{
		ID:           p.ID,
		Name:         p.Name,
		Description:  textOf(p.Description),
		Status:       p.Status,
		StatusReason: textOf(p.StatusReason),
		ClosedAt:     timeTextOf(p.ClosedAt),
		Position:     int64(p.Position),
		CreatedAt:    p.CreatedAt.Format(time.RFC3339Nano),
	}
}

func (r projectRecord) toModel() model.Project {
	return model.Project{
		ID:           r.ID,
		Name:         r.Name,
		Description:  r.Description.pointer(),
		Status:       r.Status,
		StatusReason: r.StatusReason.pointer(),
		ClosedAt:     r.ClosedAt.timePointer(),
		Position:     int(r.Position),
		CreatedAt:    parseRecordTime(r.CreatedAt),
	}
}

func itemRecordOf(i model.ProjectItem) itemRecord {
	record := itemRecord{
		ID:        i.ID,
		Title:     i.Title,
		Notes:     textOf(i.Notes),
		Repo:      textOf(i.Repo),
		Completed: intOf(i.Completed),
		Archived:  intOf(i.Archived),
		CreatedAt: i.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: i.UpdatedAt.Format(time.RFC3339Nano),
	}
	if i.Number != nil {
		record.Number = nullableInt{Int64: int64(*i.Number), Valid: true}
	}
	return record
}

func (r itemRecord) toModel() model.ProjectItem {
	item := model.ProjectItem{
		ID:        r.ID,
		Title:     r.Title,
		Notes:     r.Notes.pointer(),
		Repo:      r.Repo.pointer(),
		Completed: r.Completed != 0,
		Archived:  r.Archived != 0,
		CreatedAt: parseRecordTime(r.CreatedAt),
		UpdatedAt: parseRecordTime(r.UpdatedAt),
	}
	if r.Number.Valid {
		number := int(r.Number.Int64)
		item.Number = &number
	}
	return item
}

func membershipRecordOf(m model.Membership) membershipRecord {
	return membershipRecord{ItemID: m.ItemID, ProjectID: m.ProjectID, Position: int64(m.Position)}
}

func (r membershipRecord) toModel() model.Membership {
	return model.Membership{ItemID: r.ItemID, ProjectID: r.ProjectID, Position: int(r.Position)}
}

func taskRecordOf(t model.ProjectItemTask) taskRecord {
	return taskRecord{
		ID:        t.ID,
		ItemID:    t.ItemID,
		Title:     t.Title,
		Completed: intOf(t.Completed),
		Position:  int64(t.Position),
		CreatedAt: t.CreatedAt.Format(time.RFC3339Nano),
	}
}

func (r taskRecord) toModel() model.ProjectItemTask {
	return model.ProjectItemTask{
		ID:        r.ID,
		ItemID:    r.ItemID,
		Title:     r.Title,
		Completed: r.Completed != 0,
		Position:  int(r.Position),
		CreatedAt: parseRecordTime(r.CreatedAt),
	}
}

// An empty list is left nil, so a snapshot holding none writes null as it always has.

func membershipRecordsOf(memberships []model.Membership) []membershipRecord {
	var records []membershipRecord
	for _, m := range memberships {
		records = append(records, membershipRecordOf(m))
	}
	return records
}

func dependencyRecordsOf(dependencies []model.Dependency) []dependencyRecord {
	var records []dependencyRecord
	for _, d := range dependencies {
		records = append(records, dependencyRecord(d))
	}
	return records
}

func taskRecordsOf(tasks []model.ProjectItemTask) []taskRecord {
	var records []taskRecord
	for _, t := range tasks {
		records = append(records, taskRecordOf(t))
	}
	return records
}

func textOf(s *string) nullableText {
	if s == nil {
		return nullableText{}
	}
	return nullableText{String: *s, Valid: true}
}

func timeTextOf(t *time.Time) nullableText {
	if t == nil {
		return nullableText{}
	}
	return nullableText{String: t.Format(time.RFC3339Nano), Valid: true}
}

func intOf(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (n nullableText) pointer() *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

func (n nullableText) timePointer() *time.Time {
	if !n.Valid {
		return nil
	}
	t := parseRecordTime(n.String)
	return &t
}

// parseRecordTime reads each form a record's timestamp has been written in: this
// file's RFC3339Nano, and the plain form the oldest rows carried.
func parseRecordTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}
