package apiclient

import (
	"context"
	"time"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/model"
)

// Backend is the TUI's port over the API. Each method is the route the service
// method of the same name answers through, converted to model, so the TUI reads
// the same rows and refusal sentences it reads from a database on this machine.
type Backend struct {
	client *Client
}

var _ backend.Backend = (*Backend)(nil)

// NewBackend serves the TUI from the API client calls.
func NewBackend(client *Client) *Backend { return &Backend{client: client} }

// The port takes no context, because no local caller would use one.
func (b *Backend) ctx() context.Context { return context.Background() }

func (b *Backend) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	answered, err := b.client.ListProjects(b.ctx(), ProjectQuery{Status: status})
	if err != nil {
		return nil, err
	}
	projects := wire.Map(answered, countedProjectFrom)
	model.SortProjectsForBoard(projects)
	return projects, nil
}

func (b *Backend) GetProject(id string) (*model.ProjectWithItemCount, error) {
	answered, err := b.client.GetProject(b.ctx(), id)
	if err != nil {
		return nil, err
	}
	project := countedProjectFrom(answered)
	return &project, nil
}

func (b *Backend) CreateProject(input model.CreateProject) (*model.Project, error) {
	answered, err := b.client.CreateProject(b.ctx(), NewProject{
		Name: input.Name, Description: input.Description, Kind: input.Kind, Position: input.Position,
	})
	if err != nil {
		return nil, err
	}
	project := projectFrom(answered)
	return &project, nil
}

func (b *Backend) UpdateProject(id string, input model.UpdateProject) (*model.Project, error) {
	return b.patchProject(id, ProjectChanges{
		Name: input.Name, Description: input.Description, Kind: input.Kind,
		Status: input.Status, StatusReason: input.StatusReason, Position: input.Position,
		ClearDescription: input.ClearDescription, ClearStatusReason: input.ClearStatusReason,
	})
}

func (b *Backend) SetProjectStatus(id, status string, reason *string) (*model.Project, error) {
	return b.patchProject(id, ProjectChanges{Status: &status, StatusReason: reason, ClearStatusReason: reason == nil})
}

func (b *Backend) patchProject(id string, edit ProjectChanges) (*model.Project, error) {
	answered, err := b.client.UpdateProject(b.ctx(), id, edit)
	if err != nil {
		return nil, err
	}
	project := projectFrom(answered)
	return &project, nil
}

func (b *Backend) DeleteProject(id string) error { return b.client.DeleteProject(b.ctx(), id) }

func (b *Backend) ReorderProject(projectID string, newPosition int) error {
	_, err := b.patchProject(projectID, ProjectChanges{Position: &newPosition})
	return err
}

func (b *Backend) ListAllItemsIncludingArchived() ([]model.ProjectItem, error) {
	answered, err := b.everyItem()
	if err != nil {
		return nil, err
	}
	return wire.Map(answered, listedItemFrom), nil
}

// everyItem is the whole list, archived items included, which carries each
// item's memberships and dependencies as well.
func (b *Backend) everyItem() ([]wire.ProjectItem, error) {
	return b.client.ListItems(b.ctx(), ItemQuery{Status: model.StatusAll})
}

func (b *Backend) ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error) {
	items, err := b.projectItems(projectID, model.StatusAll)
	if err != nil {
		return nil, err
	}
	return model.WithoutArchived(items), nil
}

func (b *Backend) projectItems(projectID, status string) ([]model.ProjectItemInProject, error) {
	answered, err := b.client.ListProjectItems(b.ctx(), projectID, ItemQuery{Status: status})
	if err != nil {
		return nil, err
	}
	return wire.Map(answered, heldItemFrom), nil
}

func (b *Backend) GetItem(id string) (*model.ProjectItemDetail, error) {
	answered, err := b.client.GetItem(b.ctx(), id)
	if err != nil {
		return nil, err
	}
	detail := detailFrom(answered)
	return &detail, nil
}

func (b *Backend) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	answered, err := b.client.CreateItem(b.ctx(), NewItem{
		Title: input.Title, Notes: input.Notes, Repo: input.Repo, ProjectIDs: input.ProjectIDs,
	})
	if err != nil {
		return nil, err
	}
	detail := detailFrom(answered)
	return &detail, nil
}

func (b *Backend) UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	answered, err := b.client.UpdateItem(b.ctx(), id, ItemChanges{
		Title: input.Title, Notes: input.Notes, Repo: input.Repo,
		Completed: input.Completed, Archived: input.Archived,
		ClearNotes: input.ClearNotes, ClearRepo: input.ClearRepo,
	})
	if err != nil {
		return nil, err
	}
	item := listedItemFrom(answered)
	return &item, nil
}

func (b *Backend) DeleteItem(id string) error { return b.client.DeleteItem(b.ctx(), id) }

func (b *Backend) ReorderItem(itemID, projectID string, newPosition int) error {
	_, err := b.client.ReorderItem(b.ctx(), itemID, projectID, newPosition)
	return err
}

func (b *Backend) AddToProject(itemID, projectID string) error {
	_, err := b.client.AddItemToProject(b.ctx(), itemID, projectID)
	return err
}

func (b *Backend) RemoveFromProject(itemID, projectID string) error {
	return b.client.RemoveItemFromProject(b.ctx(), itemID, projectID)
}

func (b *Backend) GetItemProjects(itemID string) ([]model.Project, error) {
	answered, err := b.client.ListItemProjects(b.ctx(), itemID)
	if err != nil {
		return nil, err
	}
	return wire.Map(answered, projectFrom), nil
}

func (b *Backend) ListMemberships() ([]model.Membership, error) {
	answered, err := b.everyItem()
	if err != nil {
		return nil, err
	}
	var memberships []model.Membership
	for _, item := range answered {
		for _, held := range item.Memberships {
			memberships = append(memberships, model.Membership{ItemID: item.ID, ProjectID: held.ProjectID, Position: held.Position})
		}
	}
	return memberships, nil
}

func (b *Backend) AddDependency(itemID, dependsOn string) error {
	_, err := b.client.AddDependency(b.ctx(), itemID, dependsOn)
	return err
}

func (b *Backend) RemoveDependency(itemID, dependsOn string) error {
	return b.client.RemoveDependency(b.ctx(), itemID, dependsOn)
}

func (b *Backend) GetBlockers(itemID string) ([]model.ProjectItem, error) {
	return b.items(b.client.ListBlockers(b.ctx(), itemID))
}

func (b *Backend) ListDependencies() ([]model.Dependency, error) {
	answered, err := b.everyItem()
	if err != nil {
		return nil, err
	}
	var edges []model.Dependency
	for _, item := range answered {
		for _, dependsOn := range item.DependencyIDs {
			edges = append(edges, model.Dependency{ItemID: item.ID, DependsOnID: dependsOn})
		}
	}
	return edges, nil
}

func (b *Backend) ListAllTasks() ([]model.ProjectItemTask, error) {
	answered, err := b.everyItem()
	if err != nil {
		return nil, err
	}
	var tasks []model.ProjectItemTask
	for _, item := range answered {
		tasks = append(tasks, wire.Map(item.Tasks, taskFrom)...)
	}
	return tasks, nil
}

func (b *Backend) ListTasks(itemID string) ([]model.ProjectItemTask, error) {
	answered, err := b.client.ListTasks(b.ctx(), itemID)
	if err != nil {
		return nil, err
	}
	return wire.Map(answered, taskFrom), nil
}

func (b *Backend) CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	position := input.Position
	answered, err := b.client.CreateTask(b.ctx(), itemID, NewTask{Title: input.Title, Position: &position})
	if err != nil {
		return nil, err
	}
	task := taskFrom(answered)
	return &task, nil
}

func (b *Backend) UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	answered, err := b.client.UpdateTask(b.ctx(), itemID, taskID, TaskChanges{
		Title: input.Title, Completed: input.Completed, Position: input.Position,
	})
	if err != nil {
		return nil, err
	}
	task := taskFrom(answered)
	return &task, nil
}

func (b *Backend) DeleteTask(itemID, taskID string) error {
	return b.client.DeleteTask(b.ctx(), itemID, taskID)
}

func (b *Backend) Search(query string) ([]model.ProjectItem, error) {
	items, err := b.items(b.client.SearchItems(b.ctx(), query, nil))
	if err != nil {
		return nil, err
	}
	return model.WithoutArchived(items), nil
}

func (b *Backend) ListBlocked() ([]model.ProjectItem, error) {
	return b.items(b.client.ListBlockedItems(b.ctx(), nil))
}

func (b *Backend) ListArchived(projectID string) ([]model.ProjectItemInProject, error) {
	archived, err := b.projectItems(projectID, model.ItemStatusArchived)
	if err != nil {
		return nil, err
	}
	model.SortArchivedForBoard(archived)
	return archived, nil
}

func (b *Backend) Undo() (*model.UndoResult, error) {
	answered, err := b.client.Undo(b.ctx())
	if err != nil {
		return nil, err
	}
	return &model.UndoResult{
		Description: answered.Description, Action: answered.Action,
		EntityType: answered.EntityType, EntityID: answered.EntityID,
	}, nil
}

func (b *Backend) CanUndo() (bool, error) {
	state, err := b.client.UndoState(b.ctx())
	return state.CanUndo, err
}

func (b *Backend) items(answered []wire.ProjectItem, err error) ([]model.ProjectItem, error) {
	if err != nil {
		return nil, err
	}
	return wire.Map(answered, listedItemFrom), nil
}

func timeFrom(t *wire.Time) *time.Time {
	if t == nil {
		return nil
	}
	return &t.Time
}

func projectFrom(p wire.Project) model.Project {
	return model.Project{
		ID: p.ID, Name: p.Name, Description: p.Description, Kind: p.Kind,
		Status: p.Status, StatusReason: p.StatusReason, ClosedAt: timeFrom(p.ClosedAt),
		Position: p.Position, CreatedAt: p.CreatedAt.Time,
	}
}

func countedProjectFrom(p wire.ProjectWithItemCount) model.ProjectWithItemCount {
	return model.ProjectWithItemCount{
		Project: projectFrom(p.Project), ItemCount: p.ItemCount, OpenCount: p.OpenCount,
		CompletedCount: p.CompletedCount, Repos: p.Repos,
	}
}

func listedItemFrom(i wire.ProjectItem) model.ProjectItem {
	return model.ProjectItem{
		ID: i.ID, Number: i.Number, Title: i.Title, Notes: i.Notes, Repo: i.Repo,
		Completed: i.Completed, CompletedAt: timeFrom(i.CompletedAt), Archived: i.Archived,
		CreatedAt: i.CreatedAt.Time, UpdatedAt: i.UpdatedAt.Time,
	}
}

func heldItemFrom(i wire.ProjectItemInProject) model.ProjectItemInProject {
	return model.ProjectItemInProject{
		ProjectItem: model.ProjectItem{
			ID: i.ID, Number: i.Number, Title: i.Title, Notes: i.Notes, Repo: i.Repo,
			Completed: i.Completed, CompletedAt: timeFrom(i.CompletedAt), Archived: i.Archived,
			CreatedAt: i.CreatedAt.Time, UpdatedAt: i.UpdatedAt.Time,
		},
		Position: i.Position, ProjectCount: i.ProjectCount,
	}
}

// detailFrom leaves Tasks empty, because a detail's tasks are their own route.
func detailFrom(d wire.ProjectItemDetail) model.ProjectItemDetail {
	memberships := make([]model.Membership, len(d.Memberships))
	for i, held := range d.Memberships {
		memberships[i] = model.Membership{ItemID: d.ID, ProjectID: held.ProjectID, Position: held.Position}
	}
	dependsOn := make([]model.ProjectItem, len(d.DependsOn))
	for i, summary := range d.DependsOn {
		dependsOn[i] = model.ProjectItem{
			ID: summary.ID, Number: summary.Number, Title: summary.Title,
			Completed: summary.Completed, Archived: summary.Archived,
		}
	}
	return model.ProjectItemDetail{
		ProjectItem: model.ProjectItem{
			ID: d.ID, Number: d.Number, Title: d.Title, Notes: d.Notes, Repo: d.Repo,
			Completed: d.Completed, CompletedAt: timeFrom(d.CompletedAt), Archived: d.Archived,
			CreatedAt: d.CreatedAt.Time, UpdatedAt: d.UpdatedAt.Time,
		},
		Projects:      wire.Map(d.Projects, projectFrom),
		Memberships:   memberships,
		DependencyIDs: d.DependencyIDs,
		DependsOn:     dependsOn,
	}
}

func taskFrom(t wire.ProjectItemTask) model.ProjectItemTask {
	return model.ProjectItemTask{
		ID: t.ID, ItemID: t.ItemID, Title: t.Title, Completed: t.Completed,
		CompletedAt: timeFrom(t.CompletedAt), Position: t.Position, CreatedAt: t.CreatedAt.Time,
	}
}
