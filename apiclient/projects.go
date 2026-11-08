package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/datapointchris/projects/api/wire"
)

// ProjectQuery narrows the project list. An empty Status leaves the API's
// default, active.
type ProjectQuery struct {
	Status string
	Repo   *string
	Limit  *int
}

// NewProject is what a create sends. Nil fields take the API's defaults.
type NewProject struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Kind        *string `json:"kind,omitempty"`
	Position    *int    `json:"position,omitempty"`
}

// ProjectChanges is a project edit. A nil field is left as it is, and a Clear
// flag empties its field.
type ProjectChanges struct {
	Name, Description, Kind, Status, StatusReason *string
	Position                                      *int
	ClearDescription, ClearStatusReason           bool
}

func (c ProjectChanges) body() changes {
	body := changes{}
	body.text("name", c.Name, false)
	body.text("description", c.Description, c.ClearDescription)
	body.text("kind", c.Kind, false)
	body.text("status", c.Status, false)
	body.text("status_reason", c.StatusReason, c.ClearStatusReason)
	body.number("position", c.Position)
	return body
}

func (c *Client) ListProjects(ctx context.Context, q ProjectQuery) ([]wire.ProjectWithItemCount, error) {
	query := url.Values{}
	if q.Status != "" {
		query.Set("status", q.Status)
	}
	if q.Repo != nil {
		query.Set("repo", *q.Repo)
	}
	if q.Limit != nil {
		query.Set("limit", strconv.Itoa(*q.Limit))
	}
	var projects []wire.ProjectWithItemCount
	err := c.call(ctx, http.MethodGet, "/projects", query, nil, &projects)
	return projects, err
}

func (c *Client) GetProject(ctx context.Context, ref string) (wire.ProjectWithItemCount, error) {
	var project wire.ProjectWithItemCount
	err := c.call(ctx, http.MethodGet, "/projects/"+escaped(ref), nil, nil, &project)
	return project, err
}

func (c *Client) CreateProject(ctx context.Context, input NewProject) (wire.Project, error) {
	var project wire.Project
	err := c.call(ctx, http.MethodPost, "/projects", nil, input, &project)
	return project, err
}

func (c *Client) UpdateProject(ctx context.Context, ref string, edit ProjectChanges) (wire.Project, error) {
	var project wire.Project
	err := c.call(ctx, http.MethodPatch, "/projects/"+escaped(ref), nil, edit.body(), &project)
	return project, err
}

func (c *Client) DeleteProject(ctx context.Context, ref string) error {
	return c.call(ctx, http.MethodDelete, "/projects/"+escaped(ref), nil, nil, nil)
}

// ListProjectItems is one project's items in project order.
func (c *Client) ListProjectItems(ctx context.Context, ref string, q ItemQuery) ([]wire.ProjectItemInProject, error) {
	var items []wire.ProjectItemInProject
	err := c.call(ctx, http.MethodGet, "/projects/"+escaped(ref)+"/items", q.values(), nil, &items)
	return items, err
}
