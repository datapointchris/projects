package backend

import "github.com/datapointchris/projects/model"

// Backend defines the interface for all data operations. service.Service holds
// the rules over a store, and apiclient.Backend reaches them through the API.
type Backend interface {
	// Projects
	// ListProjectsByStatus takes a status name or model.StatusAll.
	ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error)
	GetProject(id string) (*model.ProjectWithItemCount, error)
	CreateProject(input model.CreateProject) (*model.Project, error)
	UpdateProject(id string, input model.UpdateProject) (*model.Project, error)
	// SetProjectStatus moves a project between active, someday, completed and
	// dropped. The closing timestamp and the reason are derived from the
	// transition rather than sent, so they cannot contradict the status they
	// describe.
	SetProjectStatus(id, status string, reason *string) (*model.Project, error)
	DeleteProject(id string) error
	ReorderProject(projectID string, newPosition int) error

	// Items
	ListAllItemsIncludingArchived() ([]model.ProjectItem, error)
	ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error)
	GetItem(id string) (*model.ProjectItemDetail, error)
	CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error)
	UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error)
	DeleteItem(id string) error
	ReorderItem(itemID, projectID string, newPosition int) error

	// Multi-project membership
	AddToProject(itemID, projectID string) error
	RemoveFromProject(itemID, projectID string) error
	GetItemProjects(itemID string) ([]model.Project, error)
	// ListMemberships is every item's filing in one read, for the views that
	// need project names across the whole store rather than for one item.
	ListMemberships() ([]model.Membership, error)

	// Dependencies
	AddDependency(itemID, dependsOn string) error
	RemoveDependency(itemID, dependsOn string) error
	GetBlockers(itemID string) ([]model.ProjectItem, error)
	// ListDependencies is every edge in one read. GetBlockers answers for one
	// item and returns only the incomplete ones, so building a whole graph out
	// of it costs a query per item and loses every finished node — which are
	// the ones holding a chain together once part of it is done.
	ListDependencies() ([]model.Dependency, error)

	// Tasks
	ListTasks(itemID string) ([]model.ProjectItemTask, error)
	// ListAllTasks is every item's tasks in one read, so a view counting tasks
	// across a list costs one read rather than one per item.
	ListAllTasks() ([]model.ProjectItemTask, error)
	CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error)
	UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error)
	DeleteTask(itemID, taskID string) error

	// Search
	Search(query string) ([]model.ProjectItem, error)

	// Filters
	ListBlocked() ([]model.ProjectItem, error)
	ListArchived(projectID string) ([]model.ProjectItemInProject, error)

	// Undo
	Undo() (*model.UndoResult, error)
	CanUndo() (bool, error)
}
