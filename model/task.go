package model

import "time"

// ProjectItemTask is a sub-task within a project item.
type ProjectItemTask struct {
	ID          string     `json:"id"`
	ItemID      string     `json:"item_id"`
	Title       string     `json:"title"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Position    int        `json:"position"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateProjectItemTask is the input for creating a new task on a project item. A
// nil ID is generated. A Position of zero places the task after every other.
type CreateProjectItemTask struct {
	ID       *string `json:"id,omitempty"`
	Title    string  `json:"title"`
	Position int     `json:"position,omitempty"`
}

// UpdateProjectItemTask is the input for updating an existing task.
// All fields are optional — only non-nil fields are applied.
type UpdateProjectItemTask struct {
	Title     *string `json:"title,omitempty"`
	Completed *bool   `json:"completed,omitempty"`
	Position  *int    `json:"position,omitempty"`
}
