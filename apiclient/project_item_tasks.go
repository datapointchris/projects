package apiclient

import (
	"context"
	"net/http"

	"github.com/datapointchris/projects/api/wire"
)

// NewTask is what a task create sends. A nil Position puts it first.
type NewTask struct {
	Title    string `json:"title"`
	Position *int   `json:"position,omitempty"`
}

// TaskChanges is a task edit. A nil field is left as it is.
type TaskChanges struct {
	Title     *string
	Completed *bool
	Position  *int
}

func (c TaskChanges) body() changes {
	body := changes{}
	body.text("title", c.Title, false)
	body.flag("completed", c.Completed)
	body.number("position", c.Position)
	return body
}

func taskPath(itemRef, taskID string) string {
	return itemPath(itemRef) + "/tasks/" + escaped(taskID)
}

func (c *Client) ListTasks(ctx context.Context, itemRef string) ([]wire.ProjectItemTask, error) {
	var tasks []wire.ProjectItemTask
	err := c.call(ctx, http.MethodGet, itemPath(itemRef)+"/tasks", nil, nil, &tasks)
	return tasks, err
}

func (c *Client) CreateTask(ctx context.Context, itemRef string, input NewTask) (wire.ProjectItemTask, error) {
	var task wire.ProjectItemTask
	err := c.call(ctx, http.MethodPost, itemPath(itemRef)+"/tasks", nil, input, &task)
	return task, err
}

func (c *Client) UpdateTask(ctx context.Context, itemRef, taskID string, edit TaskChanges) (wire.ProjectItemTask, error) {
	var task wire.ProjectItemTask
	err := c.call(ctx, http.MethodPatch, taskPath(itemRef, taskID), nil, edit.body(), &task)
	return task, err
}

func (c *Client) DeleteTask(ctx context.Context, itemRef, taskID string) error {
	return c.call(ctx, http.MethodDelete, taskPath(itemRef, taskID), nil, nil, nil)
}

// Undo reverses the latest write. A 409 says nothing is left to undo.
func (c *Client) Undo(ctx context.Context) (wire.UndoResult, error) {
	var result wire.UndoResult
	err := c.call(ctx, http.MethodPost, "/undo", nil, nil, &result)
	return result, err
}

func (c *Client) UndoState(ctx context.Context) (wire.UndoState, error) {
	var state wire.UndoState
	err := c.call(ctx, http.MethodGet, "/undo", nil, nil, &state)
	return state, err
}
