package sync

import (
	"fmt"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/model"
)

// SyncBackend wraps LocalBackend and queues sync operations after mutations.
// Read methods delegate directly; write methods delegate then queue + notify.
type SyncBackend struct {
	local  *backend.LocalBackend
	engine *Engine
}

// Compile-time check that SyncBackend implements Backend.
var _ backend.Backend = (*SyncBackend)(nil)

// NewSyncBackend creates a backend that writes locally and syncs to the remote API.
func NewSyncBackend(local *backend.LocalBackend, engine *Engine) *SyncBackend {
	return &SyncBackend{local: local, engine: engine}
}

// --- Read methods: pass through to local ---

func (s *SyncBackend) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	return s.local.ListProjectsByStatus(status)
}

func (s *SyncBackend) GetProject(id string) (*model.ProjectWithItemCount, error) {
	return s.local.GetProject(id)
}

func (s *SyncBackend) ListAllItemsIncludingArchived() ([]model.ProjectItem, error) {
	return s.local.ListAllItemsIncludingArchived()
}

func (s *SyncBackend) ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error) {
	return s.local.ListItemsByProject(projectID)
}

func (s *SyncBackend) GetItem(id string) (*model.ProjectItemDetail, error) {
	return s.local.GetItem(id)
}

func (s *SyncBackend) GetItemProjects(itemID string) ([]model.Project, error) {
	return s.local.GetItemProjects(itemID)
}

func (s *SyncBackend) GetBlockers(itemID string) ([]model.ProjectItem, error) {
	return s.local.GetBlockers(itemID)
}

func (s *SyncBackend) ListDependencies() ([]model.Dependency, error) {
	return s.local.ListDependencies()
}

func (s *SyncBackend) ListMemberships() ([]model.Membership, error) {
	return s.local.ListMemberships()
}

func (s *SyncBackend) ListTasks(itemID string) ([]model.ProjectItemTask, error) {
	return s.local.ListTasks(itemID)
}

func (s *SyncBackend) Search(query string) ([]model.ProjectItem, error) {
	return s.local.Search(query)
}

func (s *SyncBackend) ListBlocked() ([]model.ProjectItem, error) {
	return s.local.ListBlocked()
}

func (s *SyncBackend) ListArchived(projectID string) ([]model.ProjectItemInProject, error) {
	return s.local.ListArchived(projectID)
}

func (s *SyncBackend) CanUndo() (bool, error) {
	return s.local.CanUndo()
}

// --- Write methods: delegate to local, then queue + notify ---

func (s *SyncBackend) CreateProject(input model.CreateProject) (*model.Project, error) {
	result, err := s.local.CreateProject(input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpCreateProject, result.ID, createProjectPayload{
		ID:          result.ID,
		Name:        input.Name,
		Description: input.Description,
		Status:      result.Status,
		Reason:      result.StatusReason,
	})
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) UpdateProject(id string, input model.UpdateProject) (*model.Project, error) {
	result, err := s.local.UpdateProject(id, input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpUpdateProject, id, input)
	s.engine.Notify()
	return result, nil
}

// SetProjectStatus queues a PATCH rather than a complete/drop/reopen action
// endpoint, matching the API: one write path, so the transition is validated in
// one place. closed_at is left out of the payload because the server derives it
// from the status, exactly as the local statement does.
func (s *SyncBackend) SetProjectStatus(id, status string, reason *string) (*model.Project, error) {
	result, err := s.local.SetProjectStatus(id, status, reason)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpUpdateProject, id, projectStatusPayload{
		Status: status,
		Reason: result.StatusReason,
	})
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) DeleteProject(id string) error {
	if err := s.local.DeleteProject(id); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpDeleteProject, id, nil)
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) ReorderProject(projectID string, newPosition int) error {
	if err := s.local.ReorderProject(projectID, newPosition); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpReorderProject, projectID, projectReorderPayload{
		Position: newPosition,
	})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	result, err := s.local.CreateItem(input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpCreateItem, result.ID, createItemPayload{
		ID:         result.ID,
		Title:      input.Title,
		Notes:      input.Notes,
		Repo:       input.Repo,
		ProjectIDs: input.ProjectIDs,
	})
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	result, err := s.local.UpdateItem(id, input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpUpdateItem, id, input)
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) DeleteItem(id string) error {
	if err := s.local.DeleteItem(id); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpDeleteItem, id, nil)
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) ReorderItem(itemID, projectID string, newPosition int) error {
	if err := s.local.ReorderItem(itemID, projectID, newPosition); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpReorderItem, itemID, reorderPayload{
		ProjectID: projectID,
		Position:  newPosition,
	})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) AddToProject(itemID, projectID string) error {
	if err := s.local.AddToProject(itemID, projectID); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpAddToProject, itemID, projectIDPayload{ProjectID: projectID})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) RemoveFromProject(itemID, projectID string) error {
	if err := s.local.RemoveFromProject(itemID, projectID); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpRemoveFromProject, itemID, projectIDPayload{ProjectID: projectID})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) AddDependency(itemID, dependsOn string) error {
	if err := s.local.AddDependency(itemID, dependsOn); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpAddDependency, itemID, depPayload{DependsOnID: dependsOn})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) RemoveDependency(itemID, dependsOn string) error {
	if err := s.local.RemoveDependency(itemID, dependsOn); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpRemoveDependency, itemID, depPayload{DependsOnID: dependsOn})
	s.engine.Notify()
	return nil
}

func (s *SyncBackend) CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	result, err := s.local.CreateTask(itemID, input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpCreateTask, result.ID, taskPayload{
		ItemID: itemID,
		Title:  input.Title,
	})
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	result, err := s.local.UpdateTask(itemID, taskID, input)
	if err != nil {
		return nil, err
	}
	_ = s.engine.QueueOp(OpUpdateTask, taskID, taskUpdatePayload{
		ItemID:    itemID,
		Title:     input.Title,
		Completed: input.Completed,
		Position:  input.Position,
	})
	s.engine.Notify()
	return result, nil
}

func (s *SyncBackend) DeleteTask(itemID, taskID string) error {
	if err := s.local.DeleteTask(itemID, taskID); err != nil {
		return err
	}
	_ = s.engine.QueueOp(OpDeleteTask, taskID, taskRefPayload{ItemID: itemID})
	s.engine.Notify()
	return nil
}

// Undo reverses the local change and then reconciles the sync queue with it.
// Treating undo as local-only leaves the queued operations for that entity in
// place, so the next push recreates on the server exactly what was just undone.
func (s *SyncBackend) Undo() (*model.UndoResult, error) {
	result, err := s.local.Undo()
	if err != nil {
		return nil, err
	}

	// Memberships, dependencies, and item reorders all record the item as their
	// entity ID, so dropping by entity ID would take unrelated queued edits to
	// that item with them. Queue the inverse operation instead — a push that
	// finds nothing to reverse gets a 404 or 409, which the push loop already
	// treats as settled.
	switch result.EntityType {
	case "membership", "dependency", "item_position", "project_position":
		s.queueUndoInverse(result)
		s.engine.Notify()
		return result, nil
	}

	// Anything still queued describes the state the undo just reversed.
	dropped, dropErr := s.engine.DropOpsForEntity(result.EntityID)
	if dropErr != nil {
		return result, fmt.Errorf("reconciling sync queue: %w", dropErr)
	}

	if result.EntityType == "task" {
		s.queueTaskUndo(result, dropped)
		s.engine.Notify()
		return result, nil
	}

	// Undoing a delete when the delete had already been pushed means the row
	// is gone from the server, so it has to be recreated rather than updated —
	// and recreated with everything that cascaded off it, since a pull rebuilds
	// memberships and dependencies from server state alone.
	deleteWasPushed := result.Action == "delete" && dropped == 0

	if result.EntityType == "project" {
		switch {
		case result.RestoredProject != nil && deleteWasPushed:
			restored := result.RestoredProject
			_ = s.engine.QueueOp(OpCreateProject, result.EntityID, createProjectPayload{
				ID:          restored.ID,
				Name:        restored.Name,
				Description: restored.Description,
				Status:      restored.Status,
				Reason:      restored.StatusReason,
			})
			for _, itemID := range result.RestoredItemIDs {
				_ = s.engine.QueueOp(OpAddToProject, itemID, projectIDPayload{ProjectID: result.EntityID})
			}
		case result.RestoredProject != nil:
			restored := result.RestoredProject
			// One PATCH carrying the whole restored row, status included: the
			// undo may have reversed a complete or a drop, and a body that
			// omitted status would leave the server showing the project as
			// closed after it came back locally.
			_ = s.engine.QueueOp(OpUpdateProject, result.EntityID, projectRestorePayload{
				Name:        restored.Name,
				Description: restored.Description,
				Position:    restored.Position,
				Status:      restored.Status,
				Reason:      restored.StatusReason,
			})
		case dropped == 0:
			_ = s.engine.QueueOp(OpDeleteProject, result.EntityID, nil)
		}
		s.engine.Notify()
		return result, nil
	}

	switch {
	case result.Restored != nil && deleteWasPushed:
		restored := result.Restored
		_ = s.engine.QueueOp(OpCreateItem, result.EntityID, createItemPayload{
			ID:         restored.ID,
			Title:      restored.Title,
			Notes:      restored.Notes,
			Repo:       restored.Repo,
			ProjectIDs: result.RestoredProjectIDs,
		})
		s.queueRestoredChildren(result)
	case result.Restored != nil:
		// The row is back; push its restored state. Idempotent whether or not
		// the reversed change had already been pushed.
		restored := result.Restored
		unlinked := ""
		repo := restored.Repo
		if repo == nil {
			// A nil Repo is omitted from the payload and would leave a link the
			// undo removed. Empty string is how this API unlinks one.
			repo = &unlinked
		}
		_ = s.engine.QueueOp(OpUpdateItem, result.EntityID, model.UpdateProjectItem{
			Title:     &restored.Title,
			Notes:     restored.Notes,
			Repo:      repo,
			Completed: &restored.Completed,
			Archived:  &restored.Archived,
		})
	case dropped == 0:
		// The row is gone locally and its create had already been pushed, so
		// the server still has it. With a queued create there is nothing on the
		// server to delete.
		_ = s.engine.QueueOp(OpDeleteItem, result.EntityID, nil)
	}

	s.engine.Notify()
	return result, nil
}

// queueUndoInverse pushes the reversal of a membership, dependency, or reorder
// undo. Detail carries which way the reversal went; Removed means the undo took
// the row away rather than putting it back.
func (s *SyncBackend) queueUndoInverse(result *model.UndoResult) {
	detail := result.Detail
	if detail == nil {
		return
	}

	switch result.EntityType {
	case "membership":
		if detail.Removed {
			_ = s.engine.QueueOp(OpRemoveFromProject, detail.ItemID, projectIDPayload{ProjectID: detail.ProjectID})
			return
		}
		_ = s.engine.QueueOp(OpAddToProject, detail.ItemID, projectIDPayload{ProjectID: detail.ProjectID})

	case "dependency":
		if detail.Removed {
			_ = s.engine.QueueOp(OpRemoveDependency, detail.ItemID, depPayload{DependsOnID: detail.DependsOnID})
			return
		}
		_ = s.engine.QueueOp(OpAddDependency, detail.ItemID, depPayload{DependsOnID: detail.DependsOnID})

	case "item_position":
		_ = s.engine.QueueOp(OpReorderItem, detail.ItemID, reorderPayload{
			ProjectID: detail.ProjectID,
			Position:  detail.Position,
		})

	case "project_position":
		_ = s.engine.QueueOp(OpReorderProject, result.EntityID, projectReorderPayload{Position: detail.Position})
	}
}

// queueTaskUndo pushes the reversal of a task undo. dropped counts operations
// still queued for the task, so a non-zero count means the change being undone
// never reached the server and needs no reversal there.
func (s *SyncBackend) queueTaskUndo(result *model.UndoResult, dropped int64) {
	detail := result.Detail
	if detail == nil {
		return
	}

	switch {
	case detail.Removed:
		if dropped == 0 {
			_ = s.engine.QueueOp(OpDeleteTask, result.EntityID, taskRefPayload{ItemID: detail.ItemID})
		}

	case result.Action == "delete":
		if dropped > 0 || detail.Task == nil {
			return
		}
		task := detail.Task
		_ = s.engine.QueueOp(OpCreateTask, task.ID, taskPayload{ItemID: task.ItemID, Title: task.Title})
		if task.Completed {
			_ = s.engine.QueueOp(OpCompleteTask, task.ID, taskRefPayload{ItemID: task.ItemID})
		}

	case detail.Task != nil:
		task := detail.Task
		position := task.Position
		_ = s.engine.QueueOp(OpUpdateTask, task.ID, taskUpdatePayload{
			ItemID:    task.ItemID,
			Title:     &task.Title,
			Completed: &task.Completed,
			Position:  &position,
		})
	}
}

// queueRestoredChildren pushes the tasks and dependencies that came back with
// a restored item. The item's own create carries its memberships.
func (s *SyncBackend) queueRestoredChildren(result *model.UndoResult) {
	for _, task := range result.RestoredTasks {
		_ = s.engine.QueueOp(OpCreateTask, task.ID, taskPayload{
			ItemID: task.ItemID,
			Title:  task.Title,
		})
		if task.Completed {
			_ = s.engine.QueueOp(OpCompleteTask, task.ID, taskRefPayload{ItemID: task.ItemID})
		}
	}
	for _, dependency := range result.RestoredDependencies {
		_ = s.engine.QueueOp(OpAddDependency, dependency.ItemID, depPayload{
			DependsOnID: dependency.DependsOnID,
		})
	}
}

// --- Payload types for JSON serialization into pending_sync ---

type createProjectPayload struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	// Sent even when active so a project completed offline does not come back
	// alive on the push that first tells the server it exists.
	Status string  `json:"status"`
	Reason *string `json:"status_reason,omitempty"`
}

// projectStatusPayload is the PATCH body for a transition. Reason is whatever
// the local statement settled on — the value sent when dropping, and null after
// a reopen cleared it — so the two databases agree without a second round trip.
type projectStatusPayload struct {
	Status string  `json:"status"`
	Reason *string `json:"status_reason"`
}

// projectRestorePayload is an undo's PATCH: the whole row as it was, so one
// request reverses whichever field the undone mutation had changed.
type projectRestorePayload struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Position    int     `json:"position"`
	Status      string  `json:"status"`
	Reason      *string `json:"status_reason"`
}

type createItemPayload struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Notes      *string  `json:"notes,omitempty"`
	Repo       *string  `json:"repo,omitempty"`
	ProjectIDs []string `json:"project_ids"`
}

type reorderPayload struct {
	ProjectID string `json:"project_id"`
	Position  int    `json:"position"`
}

type projectReorderPayload struct {
	Position int `json:"position"`
}

type projectIDPayload struct {
	ProjectID string `json:"project_id"`
}

type depPayload struct {
	DependsOnID string `json:"depends_on_id"`
}

type taskPayload struct {
	ItemID string `json:"item_id"`
	Title  string `json:"title"`
}

type taskUpdatePayload struct {
	ItemID    string  `json:"item_id"`
	Title     *string `json:"title,omitempty"`
	Completed *bool   `json:"completed,omitempty"`
	Position  *int    `json:"position,omitempty"`
}

type taskRefPayload struct {
	ItemID string `json:"item_id"`
}
