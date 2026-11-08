package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"github.com/datapointchris/projects/api/wire"
)

// NewItem is what an item create sends. ProjectIDs takes names or ids.
type NewItem struct {
	Title      string   `json:"title"`
	Notes      *string  `json:"notes,omitempty"`
	Repo       *string  `json:"repo,omitempty"`
	ProjectIDs []string `json:"project_ids"`
}

// ItemChanges is an item edit. A nil field is left as it is, and a Clear flag
// empties its field. An empty Repo unlinks too.
type ItemChanges struct {
	Title, Notes, Repo    *string
	Completed, Archived   *bool
	ClearNotes, ClearRepo bool
}

func (c ItemChanges) body() changes {
	body := changes{}
	body.text("title", c.Title, false)
	body.text("notes", c.Notes, c.ClearNotes)
	body.text("repo", c.Repo, c.ClearRepo)
	body.flag("completed", c.Completed)
	body.flag("archived", c.Archived)
	return body
}

func itemPath(ref string) string { return "/project-items/" + escaped(ref) }

func repoQuery(repo *string) url.Values {
	query := url.Values{}
	if repo != nil {
		query.Set("repo", *repo)
	}
	return query
}

// ListItems is every item the query keeps, newest first.
func (c *Client) ListItems(ctx context.Context, q ItemQuery) ([]wire.ProjectItem, error) {
	var items []wire.ProjectItem
	err := c.call(ctx, http.MethodGet, "/project-items", q.values(), nil, &items)
	return items, err
}

// ListBlockedItems is every item with an incomplete dependency.
func (c *Client) ListBlockedItems(ctx context.Context, repo *string) ([]wire.ProjectItem, error) {
	var items []wire.ProjectItem
	err := c.call(ctx, http.MethodGet, "/project-items/blocked", repoQuery(repo), nil, &items)
	return items, err
}

// SearchItems matches titles and notes, archived items included.
func (c *Client) SearchItems(ctx context.Context, words string, repo *string) ([]wire.ProjectItem, error) {
	query := repoQuery(repo)
	query.Set("q", words)
	var items []wire.ProjectItem
	err := c.call(ctx, http.MethodGet, "/project-items/search", query, nil, &items)
	return items, err
}

func (c *Client) GetItem(ctx context.Context, ref string) (wire.ProjectItemDetail, error) {
	var item wire.ProjectItemDetail
	err := c.call(ctx, http.MethodGet, itemPath(ref), nil, nil, &item)
	return item, err
}

func (c *Client) CreateItem(ctx context.Context, input NewItem) (wire.ProjectItemDetail, error) {
	var item wire.ProjectItemDetail
	err := c.call(ctx, http.MethodPost, "/project-items", nil, input, &item)
	return item, err
}

func (c *Client) UpdateItem(ctx context.Context, ref string, edit ItemChanges) (wire.ProjectItem, error) {
	var item wire.ProjectItem
	err := c.call(ctx, http.MethodPatch, itemPath(ref), nil, edit.body(), &item)
	return item, err
}

func (c *Client) DeleteItem(ctx context.Context, ref string) error {
	return c.call(ctx, http.MethodDelete, itemPath(ref), nil, nil, nil)
}

// ReorderItem moves an item within one of its projects, and answers it there.
func (c *Client) ReorderItem(ctx context.Context, ref, projectRef string, position int) (wire.ProjectItemInProject, error) {
	var item wire.ProjectItemInProject
	body := map[string]any{"project_id": projectRef, "position": position}
	err := c.call(ctx, http.MethodPatch, itemPath(ref)+"/reorder", nil, body, &item)
	return item, err
}

func (c *Client) ListItemProjects(ctx context.Context, ref string) ([]wire.Project, error) {
	var projects []wire.Project
	err := c.call(ctx, http.MethodGet, itemPath(ref)+"/projects", nil, nil, &projects)
	return projects, err
}

// AddItemToProject files an item in one more project, and answers that project.
func (c *Client) AddItemToProject(ctx context.Context, ref, projectRef string) (wire.Project, error) {
	var project wire.Project
	body := map[string]any{"project_id": projectRef}
	err := c.call(ctx, http.MethodPost, itemPath(ref)+"/projects", nil, body, &project)
	return project, err
}

func (c *Client) RemoveItemFromProject(ctx context.Context, ref, projectRef string) error {
	return c.call(ctx, http.MethodDelete, itemPath(ref)+"/projects/"+escaped(projectRef), nil, nil, nil)
}

// AddDependency records that ref waits on dependsOn, and answers ref.
func (c *Client) AddDependency(ctx context.Context, ref, dependsOn string) (wire.ProjectItemDetail, error) {
	var item wire.ProjectItemDetail
	body := map[string]any{"depends_on_id": dependsOn}
	err := c.call(ctx, http.MethodPost, itemPath(ref)+"/dependencies", nil, body, &item)
	return item, err
}

func (c *Client) RemoveDependency(ctx context.Context, ref, dependsOn string) error {
	return c.call(ctx, http.MethodDelete, itemPath(ref)+"/dependencies/"+escaped(dependsOn), nil, nil, nil)
}

// ListBlockers is what an item waits on that is not completed.
func (c *Client) ListBlockers(ctx context.Context, ref string) ([]wire.ProjectItem, error) {
	var items []wire.ProjectItem
	err := c.call(ctx, http.MethodGet, itemPath(ref)+"/blockers", nil, nil, &items)
	return items, err
}
