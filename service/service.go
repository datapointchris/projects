package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/graph"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/store"
)

// Service holds every rule about projects, items, memberships, dependencies, tasks and undo.
type Service struct {
	store store.Store
	// assignsItemNumbers is set when nothing upstream will hand one out, which
	// is exactly the sync-disabled case. See AssigningItemNumbers.
	assignsItemNumbers bool
	// repoRegistryPath enables the repo-named-project ban. Empty means no
	// registry and no ban. See RefusingRepoNames.
	repoRegistryPath string
}

// Option configures a Service at construction.
type Option func(*Service)

// AssigningItemNumbers makes this database the authority for item numbers.
//
// Pass it when sync is off, because that database is a disjoint universe no
// server will ever number. With sync on the server assigns instead, because a
// number guessed locally and a number assigned upstream would disagree and the
// handle would change under a user who had already written it down.
func AssigningItemNumbers() Option {
	return func(s *Service) { s.assignsItemNumbers = true }
}

// RefusingRepoNames bans naming a project after a repo, reading the registry at
// path on the create and rename paths only.
//
// A project name must be bounded work — something that ends — and a repo does
// not end, so a project named after one silently becomes the eternal bucket the
// next papercut falls into. The repo association is an item's repo tag, which
// already crosses project boundaries and outlives any single project.
//
// It lives on the service because every create and rename passes through here,
// whichever surface asked for it. Loaded lazily, so a session that never names
// a project does not pay to read the registry.
func RefusingRepoNames(path string) Option {
	return func(s *Service) { s.repoRegistryPath = path }
}

// New creates a service over a store.
func New(st store.Store, opts ...Option) *Service {
	s := &Service{store: st}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Compile-time check that Service implements Backend.
var _ backend.Backend = (*Service)(nil)

func (s *Service) ctx() context.Context {
	return context.Background()
}

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// --- Projects ---

// refuseRepoName rejects a project name the repo registry knows. A missing
// registry bans nothing: refusing to create a project on a machine without one
// is worse than a project named after a repo.
func (s *Service) refuseRepoName(name string) error {
	if s.repoRegistryPath == "" {
		return nil
	}
	registry, err := repos.Load(s.repoRegistryPath)
	if err != nil {
		return err
	}
	if registry.Knows(name) {
		return fmt.Errorf("%w: %q names a repo, which never ends — name the bounded work instead", model.ErrRepoNamedProject, name)
	}
	return nil
}

// ListProjectsByStatus takes a status name or model.StatusAll.
func (s *Service) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	projects, err := s.store.ListProjectsWithItemCount(s.ctx(), status)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return projects, nil
}

func (s *Service) GetProject(id string) (*model.ProjectWithItemCount, error) {
	project, err := s.store.GetProjectWithItemCount(s.ctx(), id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project: %w", err)
	}
	return &project, nil
}

func (s *Service) CreateProject(input model.CreateProject) (*model.Project, error) {
	ctx := s.ctx()
	if err := s.refuseRepoName(input.Name); err != nil {
		return nil, err
	}

	var created model.Project
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		p, err := tx.CreateProject(ctx, model.Project{
			ID:          newID(),
			Name:        input.Name,
			Description: input.Description,
		})
		if err != nil {
			if errors.Is(err, model.ErrDuplicateName) {
				return model.ErrDuplicateName
			}
			return fmt.Errorf("creating project: %w", err)
		}
		created = p
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityProject, p.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *Service) UpdateProject(id string, input model.UpdateProject) (*model.Project, error) {
	ctx := s.ctx()

	if input.Name != nil {
		// Checked on rename too: a ban one `edit` walks around is decoration.
		if err := s.refuseRepoName(*input.Name); err != nil {
			return nil, err
		}
	}

	var updated model.Project
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetProject(ctx, id)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("project %q: %w", id, model.ErrNotFound)
			}
			return fmt.Errorf("getting project for update: %w", err)
		}

		next := current
		if input.Name != nil {
			next.Name = *input.Name
		}
		if input.Description != nil {
			next.Description = input.Description
		}
		if input.Position != nil {
			next.Position = *input.Position
		}

		p, err := tx.UpdateProject(ctx, next)
		if err != nil {
			if errors.Is(err, model.ErrDuplicateName) {
				return model.ErrDuplicateName
			}
			return fmt.Errorf("updating project: %w", err)
		}
		updated = p
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityProject, id, projectRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// SetProjectStatus moves a project between active, someday, completed and
// dropped. A reason is required when dropping, optional when shelving and
// ignored when reopening.
func (s *Service) SetProjectStatus(id, status string, reason *string) (*model.Project, error) {
	ctx := s.ctx()
	if status == model.StatusDropped && (reason == nil || strings.TrimSpace(*reason) == "") {
		return nil, model.ErrDropReasonRequired
	}

	var updated model.Project
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetProject(ctx, id)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("project %q: %w", id, model.ErrNotFound)
			}
			return fmt.Errorf("getting project for status change: %w", err)
		}
		next, err := projectAfterStatusChange(current, status, reason)
		if err != nil {
			return err
		}
		p, err := tx.SetProjectStatus(ctx, next)
		if err != nil {
			if errors.Is(err, model.ErrDuplicateName) {
				return model.ErrDuplicateName
			}
			return fmt.Errorf("setting project status: %w", err)
		}
		updated = p
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityProject, id, projectRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// projectAfterStatusChange derives what a transition carries with it, so an
// active project can never hold a reason or a closing date. A someday project is
// set aside rather than closed, so it records no close. A project already closed
// in this status keeps its date, because rewording the reason decides nothing new.
func projectAfterStatusChange(current model.Project, status string, reason *string) (model.Project, error) {
	next := current
	next.Status = status
	next.StatusReason = reason
	switch status {
	case model.StatusActive:
		next.StatusReason = nil
		next.ClosedAt = nil
	case model.StatusSomeday:
		next.ClosedAt = nil
	case model.StatusCompleted, model.StatusDropped:
		if current.ClosedAt == nil || current.Status != status {
			now := time.Now().UTC().Truncate(time.Millisecond)
			next.ClosedAt = &now
		}
	default:
		return model.Project{}, fmt.Errorf("unknown project status %q", status)
	}
	return next, nil
}

func (s *Service) DeleteProject(id string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		snapshot, err := captureProject(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := tx.DeleteProject(ctx, id); err != nil {
			return fmt.Errorf("deleting project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityProject, id, snapshot)
	})
}

func (s *Service) ReorderProject(projectID string, newPosition int) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetProject(ctx, projectID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("project %q: %w", projectID, model.ErrNotFound)
			}
			return fmt.Errorf("getting project for reorder: %w", err)
		}
		if err := tx.UpdateProjectPosition(ctx, projectID, newPosition); err != nil {
			return fmt.Errorf("reordering project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityProjectPosition, projectID, projectRecordOf(current))
	})
}

// --- Items ---

func (s *Service) ListAllItemsIncludingArchived() ([]model.ProjectItem, error) {
	items, err := s.store.ListAllItems(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing all items: %w", err)
	}
	return items, nil
}

func (s *Service) ListItemsByProject(projectID string) ([]model.ProjectItemInProject, error) {
	items, err := s.store.ListItemsByProject(s.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing items by project: %w", err)
	}
	return items, nil
}

func (s *Service) GetItem(id string) (*model.ProjectItemDetail, error) {
	ctx := s.ctx()
	item, err := s.store.GetItem(ctx, id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item: %w", err)
	}

	projects, err := s.store.GetItemProjects(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}

	depIDs, err := s.store.GetDependencyIDs(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting dependency IDs: %w", err)
	}
	if depIDs == nil {
		depIDs = []string{}
	}

	return &model.ProjectItemDetail{
		ProjectItem:   item,
		Projects:      projects,
		DependencyIDs: depIDs,
	}, nil
}

// emptyRepoUnlinks stores an empty repo as absent rather than "". A repo link is
// either a registry name or absent — "" is neither, and it would render as an
// empty "Repo:" line and never match a repo filter. The API server's own CLI
// reads an empty repo flag the same way, as an unlink, because a flag cannot
// send a JSON null.
func emptyRepoUnlinks(repo *string) *string {
	if repo == nil || *repo == "" {
		return nil
	}
	return repo
}

func (s *Service) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	if len(input.ProjectIDs) == 0 {
		return nil, model.ErrLastProject
	}

	ctx := s.ctx()
	var itemID string
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		var number *int
		if s.assignsItemNumbers {
			next, err := tx.NextItemNumber(ctx)
			if err != nil {
				return fmt.Errorf("allocating item number: %w", err)
			}
			number = &next
		}

		item, err := tx.CreateItem(ctx, model.ProjectItem{
			ID:     newID(),
			Number: number,
			Title:  input.Title,
			Notes:  input.Notes,
			Repo:   emptyRepoUnlinks(input.Repo),
		})
		if err != nil {
			return fmt.Errorf("creating item: %w", err)
		}
		itemID = item.ID

		for _, pid := range input.ProjectIDs {
			if err := tx.AddItemToProject(ctx, item.ID, pid); err != nil {
				return fmt.Errorf("adding item to project %s: %w", pid, err)
			}
		}

		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityItem, item.ID, nil)
	})
	if err != nil {
		return nil, err
	}

	return s.GetItem(itemID)
}

func (s *Service) UpdateItem(id string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	ctx := s.ctx()

	var updated model.ProjectItem
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetItem(ctx, id)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("item %q: %w", id, model.ErrNotFound)
			}
			return fmt.Errorf("getting item for update: %w", err)
		}

		next := current
		if input.Title != nil {
			next.Title = *input.Title
		}
		if input.Notes != nil {
			next.Notes = input.Notes
		}
		if input.Repo != nil {
			next.Repo = emptyRepoUnlinks(input.Repo)
		}
		if input.Completed != nil {
			next.Completed = *input.Completed
		}
		if input.Archived != nil {
			next.Archived = *input.Archived
		}

		item, err := tx.UpdateItem(ctx, next)
		if err != nil {
			return fmt.Errorf("updating item: %w", err)
		}
		updated = item
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityItem, id, itemRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *Service) DeleteItem(id string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		snapshot, err := captureItem(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityItem, id, snapshot); err != nil {
			return err
		}
		if err := tx.DeleteItem(ctx, id); err != nil {
			return fmt.Errorf("deleting item: %w", err)
		}
		return nil
	})
}

func (s *Service) ReorderItem(itemID, projectID string, newPosition int) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetMembership(ctx, itemID, projectID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				// The pairing is what is absent, not either id on its own: both can
				// exist while the item is not in that project.
				return fmt.Errorf("item %q is not in project %q: %w", itemID, projectID, model.ErrNotFound)
			}
			return fmt.Errorf("getting membership for reorder: %w", err)
		}
		if err := tx.UpdateItemPosition(ctx, model.Membership{
			ItemID:    itemID,
			ProjectID: projectID,
			Position:  newPosition,
		}); err != nil {
			return fmt.Errorf("reordering item: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityItemPosition, itemID, membershipRecordOf(current))
	})
}

// --- Multi-project membership ---

func (s *Service) AddToProject(itemID, projectID string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		if err := tx.AddItemToProject(ctx, itemID, projectID); err != nil {
			return fmt.Errorf("adding item to project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityMembership, itemID, membershipRecord{
			ItemID:    itemID,
			ProjectID: projectID,
		})
	})
}

func (s *Service) RemoveFromProject(itemID, projectID string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		projects, err := tx.GetItemProjects(ctx, itemID)
		if err != nil {
			return fmt.Errorf("checking project count: %w", err)
		}
		if len(projects) <= 1 {
			return model.ErrLastProject
		}

		current, err := tx.GetMembership(ctx, itemID, projectID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("item %q is not in project %q: %w", itemID, projectID, model.ErrNotFound)
			}
			return fmt.Errorf("getting membership for remove: %w", err)
		}

		if err := tx.RemoveItemFromProject(ctx, itemID, projectID); err != nil {
			return fmt.Errorf("removing item from project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityMembership, itemID, membershipRecordOf(current))
	})
}

func (s *Service) GetItemProjects(itemID string) ([]model.Project, error) {
	projects, err := s.store.GetItemProjects(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}
	return projects, nil
}

// --- Dependencies ---

func (s *Service) AddDependency(itemID, dependsOn string) error {
	ctx := s.ctx()
	edge := model.Dependency{ItemID: itemID, DependsOnID: dependsOn}
	return s.store.InTx(ctx, func(tx store.Operations) error {
		deps, err := tx.ListAllDependencies(ctx)
		if err != nil {
			return fmt.Errorf("getting dependencies for cycle check: %w", err)
		}

		adj := make(map[string][]string)
		for _, d := range deps {
			adj[d.DependsOnID] = append(adj[d.DependsOnID], d.ItemID)
		}

		if graph.WouldCycle(adj, dependsOn, itemID) {
			return model.ErrCyclicDependency
		}

		if err := tx.AddDependency(ctx, edge); err != nil {
			return fmt.Errorf("adding dependency: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityDependency, itemID, dependencyRecord(edge))
	})
}

func (s *Service) RemoveDependency(itemID, dependsOn string) error {
	ctx := s.ctx()
	edge := model.Dependency{ItemID: itemID, DependsOnID: dependsOn}
	return s.store.InTx(ctx, func(tx store.Operations) error {
		if err := tx.RemoveDependency(ctx, edge); err != nil {
			return fmt.Errorf("removing dependency: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityDependency, itemID, dependencyRecord(edge))
	})
}

func (s *Service) GetBlockers(itemID string) ([]model.ProjectItem, error) {
	items, err := s.store.GetBlockers(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}
	return items, nil
}

func (s *Service) ListDependencies() ([]model.Dependency, error) {
	deps, err := s.store.ListAllDependencies(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing dependencies: %w", err)
	}
	return deps, nil
}

func (s *Service) ListMemberships() ([]model.Membership, error) {
	memberships, err := s.store.ListAllMemberships(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing memberships: %w", err)
	}
	return memberships, nil
}

// --- Tasks ---

func (s *Service) ListTasks(itemID string) ([]model.ProjectItemTask, error) {
	tasks, err := s.store.ListTasksByItem(s.ctx(), itemID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	return tasks, nil
}

func (s *Service) CreateTask(itemID string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()
	id := newID()

	var created model.ProjectItemTask
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		t, err := tx.CreateTask(ctx, model.ProjectItemTask{
			ID:     id,
			ItemID: itemID,
			Title:  input.Title,
		})
		if err != nil {
			return fmt.Errorf("creating task: %w", err)
		}
		created = t
		// The row, not nil: undoing a task create still has to know which item
		// owned it so the reversal can be pushed.
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityTask, id, taskRecordOf(t))
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *Service) UpdateTask(itemID, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()

	var updated model.ProjectItemTask
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetTask(ctx, taskID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("task %q: %w", taskID, model.ErrNotFound)
			}
			return fmt.Errorf("getting task for update: %w", err)
		}

		next := current
		if input.Title != nil {
			next.Title = *input.Title
		}
		if input.Completed != nil {
			next.Completed = *input.Completed
		}
		if input.Position != nil {
			next.Position = *input.Position
		}

		t, err := tx.UpdateTask(ctx, next)
		if err != nil {
			return fmt.Errorf("updating task: %w", err)
		}
		updated = t
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityTask, taskID, taskRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *Service) DeleteTask(itemID, taskID string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := tx.GetTask(ctx, taskID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("task %q: %w", taskID, model.ErrNotFound)
			}
			return fmt.Errorf("getting task for delete: %w", err)
		}
		if err := tx.DeleteTask(ctx, taskID); err != nil {
			return fmt.Errorf("deleting task: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityTask, taskID, taskRecordOf(current))
	})
}

// --- Search ---

func (s *Service) Search(query string) ([]model.ProjectItem, error) {
	items, err := s.store.SearchItems(s.ctx(), query)
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return items, nil
}

// --- Filters ---

func (s *Service) ListBlocked() ([]model.ProjectItem, error) {
	items, err := s.store.ListBlockedItems(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing blocked: %w", err)
	}
	return items, nil
}

func (s *Service) ListArchived(projectID string) ([]model.ProjectItemInProject, error) {
	items, err := s.store.ListArchivedItems(s.ctx(), projectID)
	if err != nil {
		return nil, fmt.Errorf("listing archived: %w", err)
	}
	return items, nil
}
