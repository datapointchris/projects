package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/graph"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/store"
)

// itemListSegment is what follows a project's name in the path of its item list.
const itemListSegment = "/items"

// Service holds every rule about projects, items, memberships, dependencies, tasks and undo.
type Service struct {
	store store.Store
	// assignsItemNumbers is set when nothing upstream will hand one out, which
	// is exactly the sync-disabled case. See AssigningItemNumbers.
	assignsItemNumbers bool
	// repoRegistryPath enables the repo-named-project ban. Empty means no
	// registry and no ban. See RefusingRepoNames.
	repoRegistryPath string
	// requestContext is what each store call runs under. See WithContext.
	requestContext context.Context
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
	s := &Service{store: st, requestContext: context.Background()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Compile-time check that Service implements Backend.
var _ backend.Backend = (*Service)(nil)

// WithContext returns a shallow copy whose store calls run under ctx, in the shape
// of http.Request.WithContext. A request's cancellation and its writer reach the
// store through it, and backend.Backend stays free of a context parameter no local
// caller would use.
func (s *Service) WithContext(ctx context.Context) *Service {
	scoped := *s
	scoped.requestContext = ctx
	return &scoped
}

func (s *Service) ctx() context.Context {
	return s.requestContext
}

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func now() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// idOrNew takes an id a caller chose in the spelling a store holds, or generates one.
func idOrNew(id *string) string {
	if id == nil {
		return newID()
	}
	if canonical, isUUID := canonicalUUID(*id); isUUID {
		return canonical
	}
	return *id
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

// refuseReservedName keeps a project addressable by name: `x/items` would read as
// project x's item list.
func refuseReservedName(name string) error {
	if !strings.HasSuffix(name, itemListSegment) {
		return nil
	}
	return &model.Refusal{
		Kind:    model.ErrReservedName,
		Field:   "name",
		Message: fmt.Sprintf("cannot end in %s, which /projects/<name>/items reads as an item list", quoteName(itemListSegment)),
	}
}

func refuseDropWithoutReason(status string, reason *string) error {
	if status == model.StatusDropped && (reason == nil || *reason == "") {
		return refuse(model.ErrDropReasonRequired, "Dropping a project requires a reason. Say why it is dropped rather than deferred.")
	}
	return nil
}

// refuseTakenName names the project already holding a name, which the index's own
// answer could not. Only active projects hold one.
func refuseTakenName(ctx context.Context, tx store.Operations, name, excludingID string) error {
	projects, err := tx.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	for _, project := range projects {
		if project.Name == name && project.Status == model.StatusActive && project.ID != excludingID {
			return refuse(model.ErrDuplicateName, "An active project named %s already exists (%s).", quoteName(name), project.ID)
		}
	}
	return nil
}

// ListProjectsByStatus takes a status name or model.StatusAll, in board order.
func (s *Service) ListProjectsByStatus(status string) ([]model.ProjectWithItemCount, error) {
	counted, _, err := s.projectsWithItemCounts(s.ctx())
	if err != nil {
		return nil, err
	}
	kept := make([]model.ProjectWithItemCount, 0, len(counted))
	for _, project := range counted {
		if status == model.StatusAll || project.Status == status {
			kept = append(kept, project)
		}
	}
	model.SortProjectsForBoard(kept)
	return kept, nil
}

func (s *Service) GetProject(ref string) (*model.ProjectWithItemCount, error) {
	ctx := s.ctx()
	project, err := resolveProject(ctx, s.store, ref)
	if err != nil {
		return nil, err
	}
	facts, err := s.store.ListProjectItemFacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing item counts: %w", err)
	}
	counted := withItemCounts([]model.Project{project}, facts)
	return &counted[0], nil
}

func (s *Service) CreateProject(input model.CreateProject) (*model.Project, error) {
	ctx := s.ctx()
	if err := refuseReservedName(input.Name); err != nil {
		return nil, err
	}
	if err := s.refuseRepoName(input.Name); err != nil {
		return nil, err
	}

	var created model.Project
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		next, err := newProject(ctx, tx, input)
		if err != nil {
			return err
		}
		p, err := tx.CreateProject(ctx, next)
		if errors.Is(err, model.ErrDuplicateID) {
			return refuse(model.ErrDuplicateID, "Project with id %s already exists", next.ID)
		}
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

// newProject applies the rules a create runs in the order a caller meets them. A
// project may arrive finished, so a client holding one can record it as it stands.
func newProject(ctx context.Context, tx store.Operations, input model.CreateProject) (model.Project, error) {
	next := model.Project{
		ID:           idOrNew(input.ID),
		Name:         input.Name,
		Description:  input.Description,
		Kind:         model.KindLife,
		Status:       model.StatusActive,
		StatusReason: input.StatusReason,
	}
	if input.Kind != nil {
		next.Kind = *input.Kind
	}
	if err := refuseUnknownKind(ctx, tx, next.Kind); err != nil {
		return model.Project{}, err
	}
	if input.Status != nil {
		next.Status = *input.Status
	}
	if err := refuseUnknownStatus(ctx, tx, next.Status); err != nil {
		return model.Project{}, err
	}
	if err := refuseDropWithoutReason(next.Status, next.StatusReason); err != nil {
		return model.Project{}, err
	}
	if next.Status == model.StatusActive {
		if err := refuseTakenName(ctx, tx, next.Name, ""); err != nil {
			return model.Project{}, err
		}
	}
	if model.ProjectIsTerminal(next.Status) {
		closedAt := now()
		next.ClosedAt = &closedAt
	}
	if input.Position != nil {
		next.Position = *input.Position
		return next, nil
	}
	projects, err := tx.ListProjects(ctx)
	if err != nil {
		return model.Project{}, fmt.Errorf("listing projects: %w", err)
	}
	for _, project := range projects {
		next.Position = max(next.Position, project.Position+1)
	}
	return next, nil
}

// UpdateProject is the one write path a project's fields and its status take, so a
// transition is validated in one place.
func (s *Service) UpdateProject(ref string, input model.UpdateProject) (*model.Project, error) {
	ctx := s.ctx()
	if input.Name != nil {
		// Checked on rename too: a ban one `edit` walks around is decoration.
		if err := s.refuseRepoName(*input.Name); err != nil {
			return nil, err
		}
	}

	var updated model.Project
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := resolveProject(ctx, tx, ref)
		if err != nil {
			return err
		}
		if input.Name != nil {
			if err := refuseReservedName(*input.Name); err != nil {
				return err
			}
		}
		next, err := projectAfterUpdate(ctx, tx, current, input)
		if err != nil {
			return err
		}
		p, err := tx.UpdateProject(ctx, next)
		if err != nil {
			if errors.Is(err, model.ErrDuplicateName) {
				return model.ErrDuplicateName
			}
			return fmt.Errorf("updating project: %w", err)
		}
		updated = p
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityProject, current.ID, projectRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// projectAfterUpdate derives what a transition carries with it, because a client
// free to send a closing date could leave an active project claiming one. A rename
// and a reopen both end with the project holding a name, which only an active
// project does, so both check the name they end with. Moving to someday clears the
// reason and the date as active does, because that work is set aside and not
// closed. A project already closed in this status keeps its date, because
// rewording the reason decides nothing new.
func projectAfterUpdate(ctx context.Context, tx store.Operations, current model.Project, input model.UpdateProject) (model.Project, error) {
	next := current
	if input.Kind != nil {
		if err := refuseUnknownKind(ctx, tx, *input.Kind); err != nil {
			return model.Project{}, err
		}
		next.Kind = *input.Kind
	}
	if input.Name != nil {
		next.Name = *input.Name
	}
	if input.Description != nil {
		next.Description = input.Description
	}
	if input.ClearDescription {
		next.Description = nil
	}
	if input.Position != nil {
		next.Position = *input.Position
	}
	if input.StatusReason != nil {
		next.StatusReason = input.StatusReason
	}
	if input.ClearStatusReason {
		next.StatusReason = nil
	}

	resultingStatus := current.Status
	if input.Status != nil {
		resultingStatus = *input.Status
	}
	if resultingStatus == model.StatusActive && (next.Name != current.Name || current.Status != model.StatusActive) {
		if err := refuseTakenName(ctx, tx, next.Name, current.ID); err != nil {
			return model.Project{}, err
		}
	}

	if input.Status == nil {
		return next, refuseDropWithoutReason(current.Status, next.StatusReason)
	}
	if err := refuseUnknownStatus(ctx, tx, *input.Status); err != nil {
		return model.Project{}, err
	}
	if err := refuseDropWithoutReason(*input.Status, next.StatusReason); err != nil {
		return model.Project{}, err
	}
	next.Status = *input.Status
	switch {
	case !model.ProjectIsTerminal(*input.Status):
		next.StatusReason = nil
		next.ClosedAt = nil
	case *input.Status != current.Status:
		closedAt := now()
		next.ClosedAt = &closedAt
	}
	return next, nil
}

// SetProjectStatus moves a project between active, someday, completed and
// dropped. A reason is required when dropping, kept when completing, and cleared
// on the way to active or someday.
func (s *Service) SetProjectStatus(ref, status string, reason *string) (*model.Project, error) {
	return s.UpdateProject(ref, model.UpdateProject{
		Status:            &status,
		StatusReason:      reason,
		ClearStatusReason: reason == nil,
	})
}

// DeleteProject refuses while an incomplete item would be left in no project, and
// takes finished items that only this project holds with it, because an item with
// no project cannot be reached.
func (s *Service) DeleteProject(ref string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		project, err := resolveProject(ctx, tx, ref)
		if err != nil {
			return err
		}
		held, err := tx.ListItemsInProject(ctx, project.ID)
		if err != nil {
			return fmt.Errorf("listing the project's items: %w", err)
		}
		var orphanIDs []string
		incomplete := 0
		for _, item := range held {
			if item.ProjectCount != 1 {
				continue
			}
			orphanIDs = append(orphanIDs, item.ID)
			if !item.Completed {
				incomplete++
			}
		}
		if incomplete > 0 {
			return refuse(model.ErrIncompleteOrphans,
				"Cannot delete project: %d incomplete item(s) belong only to this project. Complete, move, or delete them first.", incomplete)
		}

		snapshot, err := captureProject(ctx, tx, project.ID, orphanIDs)
		if err != nil {
			return err
		}
		for _, id := range orphanIDs {
			if err := tx.DeleteItem(ctx, id); err != nil {
				return fmt.Errorf("deleting the project's finished item: %w", err)
			}
		}
		if err := tx.DeleteProject(ctx, project.ID); err != nil {
			return fmt.Errorf("deleting project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityProject, project.ID, snapshot)
	})
}

func (s *Service) ReorderProject(ref string, newPosition int) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := resolveProject(ctx, tx, ref)
		if err != nil {
			return err
		}
		if err := tx.UpdateProjectPosition(ctx, current.ID, newPosition); err != nil {
			return fmt.Errorf("reordering project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityProjectPosition, current.ID, projectRecordOf(current))
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

// ListItemsByProject leaves out archived items, in project order.
func (s *Service) ListItemsByProject(projectRef string) ([]model.ProjectItemInProject, error) {
	ctx := s.ctx()
	project, err := resolveProject(ctx, s.store, projectRef)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListItemsInProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("listing items by project: %w", err)
	}
	return slices.DeleteFunc(items, func(item model.ProjectItemInProject) bool { return item.Archived }), nil
}

// GetItem answers an item with everything filed against it.
func (s *Service) GetItem(ref string) (*model.ProjectItemDetail, error) {
	ctx := s.ctx()
	item, err := resolveItem(ctx, s.store, ref)
	if err != nil {
		return nil, err
	}

	projects, err := s.store.GetItemProjects(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}
	memberships, err := s.store.ListItemMemberships(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting item memberships: %w", err)
	}
	dependsOn, err := s.store.ListDependencyItems(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting item dependencies: %w", err)
	}
	dependencyIDs := make([]string, len(dependsOn))
	for i, dependency := range dependsOn {
		dependencyIDs[i] = dependency.ID
	}
	tasks, err := s.store.ListTasksByItem(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting item tasks: %w", err)
	}

	return &model.ProjectItemDetail{
		ProjectItem:   item,
		Projects:      projects,
		Memberships:   memberships,
		DependencyIDs: dependencyIDs,
		DependsOn:     dependsOn,
		Tasks:         tasks,
	}, nil
}

// emptyRepoUnlinks stores an empty repo as absent rather than "". A repo link is
// either a registry name or absent — "" is neither, and it would render as an
// empty "Repo:" line and never match a repo filter. The CLI reads an empty repo
// flag the same way, as an unlink, because a flag cannot send a JSON null.
func emptyRepoUnlinks(repo *string) *string {
	if repo == nil || *repo == "" {
		return nil
	}
	return repo
}

func (s *Service) CreateItem(input model.CreateProjectItem) (*model.ProjectItemDetail, error) {
	if len(input.ProjectIDs) == 0 {
		return nil, refuse(model.ErrNoProjects, "At least one project_id is required")
	}

	ctx := s.ctx()
	var itemID string
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		projects := make([]model.Project, len(input.ProjectIDs))
		for i, ref := range input.ProjectIDs {
			project, err := resolveProject(ctx, tx, ref)
			if err != nil {
				return err
			}
			projects[i] = project
		}

		var number *int
		if s.assignsItemNumbers {
			next, err := tx.NextItemNumber(ctx)
			if err != nil {
				return fmt.Errorf("allocating item number: %w", err)
			}
			number = &next
		}

		id := idOrNew(input.ID)
		item, err := tx.CreateItem(ctx, model.ProjectItem{
			ID:     id,
			Number: number,
			Title:  input.Title,
			Notes:  input.Notes,
			Repo:   emptyRepoUnlinks(input.Repo),
		})
		if errors.Is(err, model.ErrDuplicateID) {
			return refuse(model.ErrDuplicateID, "Project item with id %s already exists", id)
		}
		if err != nil {
			return fmt.Errorf("creating item: %w", err)
		}
		itemID = item.ID

		for _, project := range projects {
			if err := addToBottomOfProject(ctx, tx, item.ID, project.ID); err != nil {
				return fmt.Errorf("adding item to project %s: %w", project.ID, err)
			}
		}

		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityItem, item.ID, nil)
	})
	if err != nil {
		return nil, err
	}

	return s.GetItem(itemID)
}

// addToBottomOfProject files an item after every other in the project, and at zero
// in an empty one.
func addToBottomOfProject(ctx context.Context, tx store.Operations, itemID, projectID string) error {
	memberships, err := tx.ListProjectMemberships(ctx, projectID)
	if err != nil {
		return err
	}
	position := 0
	for _, membership := range memberships {
		position = max(position, membership.Position+1)
	}
	return tx.AddItemToProject(ctx, model.Membership{ItemID: itemID, ProjectID: projectID, Position: position})
}

// stampCompletion answers when a row was finished after a write. Only a transition
// writes: re-sending completed on a finished row finished nothing, and on a row
// whose time is unknown it leaves the unknown alone rather than stamping today onto
// history.
func stampCompletion(wasCompleted, isCompleted bool, completedAt *time.Time) *time.Time {
	switch {
	case isCompleted == wasCompleted:
		return completedAt
	case isCompleted:
		stamped := now()
		return &stamped
	default:
		return nil
	}
}

func (s *Service) UpdateItem(ref string, input model.UpdateProjectItem) (*model.ProjectItem, error) {
	ctx := s.ctx()

	var updated model.ProjectItem
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := resolveItem(ctx, tx, ref)
		if err != nil {
			return err
		}

		if input.Completed != nil && *input.Completed && !current.Completed {
			if err := refuseIncompleteTasks(ctx, tx, current.ID); err != nil {
				return err
			}
		}

		next := current
		if input.Title != nil {
			next.Title = *input.Title
		}
		if input.Notes != nil {
			next.Notes = input.Notes
		}
		if input.ClearNotes {
			next.Notes = nil
		}
		if input.Repo != nil {
			next.Repo = emptyRepoUnlinks(input.Repo)
		}
		if input.ClearRepo {
			next.Repo = nil
		}
		if input.Completed != nil {
			next.Completed = *input.Completed
			next.CompletedAt = stampCompletion(current.Completed, next.Completed, current.CompletedAt)
		}
		if input.Archived != nil {
			next.Archived = *input.Archived
		}

		item, err := tx.UpdateItem(ctx, next)
		if err != nil {
			return fmt.Errorf("updating item: %w", err)
		}
		updated = item
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityItem, current.ID, itemRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func refuseIncompleteTasks(ctx context.Context, tx store.Operations, itemID string) error {
	tasks, err := tx.ListTasksByItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("listing the item's tasks: %w", err)
	}
	incomplete := 0
	for _, task := range tasks {
		if !task.Completed {
			incomplete++
		}
	}
	if incomplete > 0 {
		return refuse(model.ErrIncompleteTasks, "Cannot complete item with %d incomplete task(s). Finish all tasks first.", incomplete)
	}
	return nil
}

func (s *Service) DeleteItem(ref string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, err := resolveItem(ctx, tx, ref)
		if err != nil {
			return err
		}
		snapshot, err := captureItem(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		if err := logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityItem, item.ID, snapshot); err != nil {
			return err
		}
		if err := tx.DeleteItem(ctx, item.ID); err != nil {
			return fmt.Errorf("deleting item: %w", err)
		}
		return nil
	})
}

// resolveMembership resolves both references and refuses a pairing that is absent,
// because both can exist while the item is not in that project.
func resolveMembership(ctx context.Context, tx store.Operations, itemRef, projectRef string) (model.ProjectItem, model.Membership, error) {
	item, err := resolveItem(ctx, tx, itemRef)
	if err != nil {
		return model.ProjectItem{}, model.Membership{}, err
	}
	project, err := resolveProject(ctx, tx, projectRef)
	if err != nil {
		return model.ProjectItem{}, model.Membership{}, err
	}
	membership, err := tx.GetMembership(ctx, item.ID, project.ID)
	if errors.Is(err, model.ErrNotFound) {
		return model.ProjectItem{}, model.Membership{}, refuse(model.ErrNotFound, "Item %s is not in project %s", itemHandle(item), project.Name)
	}
	if err != nil {
		return model.ProjectItem{}, model.Membership{}, fmt.Errorf("getting membership: %w", err)
	}
	return item, membership, nil
}

// ReorderItem moves an item to a place in its project and dense-ranks the rest to
// 0..N-1, so moving one shifts its siblings instead of colliding with them. A
// position past the end is clamped, so a caller asking for the bottom of a list
// does not need its length.
func (s *Service) ReorderItem(itemRef, projectRef string, newPosition int) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, current, err := resolveMembership(ctx, tx, itemRef, projectRef)
		if err != nil {
			return err
		}
		held, err := tx.ListItemsInProject(ctx, current.ProjectID)
		if err != nil {
			return fmt.Errorf("listing the project's items: %w", err)
		}

		previous := make([]membershipRecord, len(held))
		remaining := make([]string, 0, len(held))
		wasAt := make(map[string]int, len(held))
		for i, sibling := range held {
			previous[i] = membershipRecordOf(model.Membership{ItemID: sibling.ID, ProjectID: current.ProjectID, Position: sibling.Position})
			wasAt[sibling.ID] = sibling.Position
			if sibling.ID != item.ID {
				remaining = append(remaining, sibling.ID)
			}
		}
		landed := max(0, min(newPosition, len(remaining)))
		ordered := slices.Insert(remaining, landed, item.ID)

		for position, id := range ordered {
			if wasAt[id] == position {
				continue
			}
			if err := tx.UpdateItemPosition(ctx, model.Membership{ItemID: id, ProjectID: current.ProjectID, Position: position}); err != nil {
				return fmt.Errorf("reordering item: %w", err)
			}
		}
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityItemPosition, item.ID, itemPositionRecord{
			membershipRecord: membershipRecordOf(current),
			Siblings:         previous,
		})
	})
}

// GetItemInProject answers an item as its project sees it.
func (s *Service) GetItemInProject(itemRef, projectRef string) (*model.ProjectItemInProject, error) {
	ctx := s.ctx()
	item, membership, err := resolveMembership(ctx, s.store, itemRef, projectRef)
	if err != nil {
		return nil, err
	}
	held, err := s.store.ListItemsInProject(ctx, membership.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("listing the project's items: %w", err)
	}
	for _, candidate := range held {
		if candidate.ID == item.ID {
			return &candidate, nil
		}
	}
	return nil, refuse(model.ErrNotFound, "Item %s is not in project %s", itemHandle(item), projectRef)
}

// --- Multi-project membership ---

func (s *Service) AddToProject(itemRef, projectRef string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, err := resolveItem(ctx, tx, itemRef)
		if err != nil {
			return err
		}
		project, err := resolveProject(ctx, tx, projectRef)
		if err != nil {
			return err
		}
		_, err = tx.GetMembership(ctx, item.ID, project.ID)
		if err == nil {
			return refuse(model.ErrAlreadyInProject, "Item %s is already in project %s", itemHandle(item), project.Name)
		}
		if !errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("getting membership: %w", err)
		}
		if err := addToBottomOfProject(ctx, tx, item.ID, project.ID); err != nil {
			return fmt.Errorf("adding item to project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityMembership, item.ID, membershipRecord{
			ItemID:    item.ID,
			ProjectID: project.ID,
		})
	})
}

func (s *Service) RemoveFromProject(itemRef, projectRef string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, current, err := resolveMembership(ctx, tx, itemRef, projectRef)
		if err != nil {
			return err
		}
		memberships, err := tx.ListItemMemberships(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("checking project count: %w", err)
		}
		if len(memberships) <= 1 {
			return refuse(model.ErrLastProject, "Cannot remove item from its last project. Delete the item instead.")
		}
		if err := tx.RemoveItemFromProject(ctx, item.ID, current.ProjectID); err != nil {
			return fmt.Errorf("removing item from project: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityMembership, item.ID, membershipRecordOf(current))
	})
}

func (s *Service) GetItemProjects(itemRef string) ([]model.Project, error) {
	ctx := s.ctx()
	item, err := resolveItem(ctx, s.store, itemRef)
	if err != nil {
		return nil, err
	}
	projects, err := s.store.GetItemProjects(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting item projects: %w", err)
	}
	return projects, nil
}

// --- Dependencies ---

func (s *Service) AddDependency(itemRef, dependsOnRef string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, err := resolveItem(ctx, tx, itemRef)
		if err != nil {
			return err
		}
		dependsOn, err := resolveItem(ctx, tx, dependsOnRef)
		if err != nil {
			return err
		}
		if item.ID == dependsOn.ID {
			return refuse(model.ErrSelfDependency, "An item cannot depend on itself")
		}

		deps, err := tx.ListAllDependencies(ctx)
		if err != nil {
			return fmt.Errorf("getting dependencies for cycle check: %w", err)
		}
		adj := make(map[string][]string)
		for _, d := range deps {
			if d.ItemID == item.ID && d.DependsOnID == dependsOn.ID {
				return refuse(model.ErrDuplicateDependency, "Dependency already exists: item %s depends on %s", itemHandle(item), itemHandle(dependsOn))
			}
			adj[d.DependsOnID] = append(adj[d.DependsOnID], d.ItemID)
		}
		if graph.WouldCycle(adj, dependsOn.ID, item.ID) {
			return refuse(model.ErrCyclicDependency, "Adding dependency would create a cycle: %s -> %s", itemHandle(item), itemHandle(dependsOn))
		}

		edge := model.Dependency{ItemID: item.ID, DependsOnID: dependsOn.ID}
		if err := tx.AddDependency(ctx, edge); err != nil {
			return fmt.Errorf("adding dependency: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionCreate, model.UndoEntityDependency, item.ID, dependencyRecord(edge))
	})
}

func (s *Service) RemoveDependency(itemRef, dependsOnRef string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		item, err := resolveItem(ctx, tx, itemRef)
		if err != nil {
			return err
		}
		dependsOn, err := resolveItem(ctx, tx, dependsOnRef)
		if err != nil {
			return err
		}
		edge := model.Dependency{ItemID: item.ID, DependsOnID: dependsOn.ID}
		deps, err := tx.ListDependenciesInvolvingItem(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("getting dependencies: %w", err)
		}
		if !slices.Contains(deps, edge) {
			return refuse(model.ErrNotFound, "No dependency: item %s -> %s", itemHandle(item), itemHandle(dependsOn))
		}
		if err := tx.RemoveDependency(ctx, edge); err != nil {
			return fmt.Errorf("removing dependency: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityDependency, item.ID, dependencyRecord(edge))
	})
}

// GetBlockers answers what an item waits on: its incomplete dependencies, archived
// ones included, since an archived dependency still is not finished.
func (s *Service) GetBlockers(itemRef string) ([]model.ProjectItem, error) {
	ctx := s.ctx()
	item, err := resolveItem(ctx, s.store, itemRef)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListDependencyItems(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}
	return slices.DeleteFunc(items, func(dependency model.ProjectItem) bool { return dependency.Completed }), nil
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

func (s *Service) ListTasks(itemRef string) ([]model.ProjectItemTask, error) {
	ctx := s.ctx()
	item, err := resolveItem(ctx, s.store, itemRef)
	if err != nil {
		return nil, err
	}
	tasks, err := s.store.ListTasksByItem(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	return tasks, nil
}

func (s *Service) CreateTask(itemRef string, input model.CreateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()
	id := idOrNew(input.ID)

	var created model.ProjectItemTask
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		item, err := resolveItem(ctx, tx, itemRef)
		if err != nil {
			return err
		}
		position := input.Position
		if position == 0 {
			tasks, err := tx.ListTasksByItem(ctx, item.ID)
			if err != nil {
				return fmt.Errorf("listing the item's tasks: %w", err)
			}
			for _, task := range tasks {
				position = max(position, task.Position+1)
			}
		}
		t, err := tx.CreateTask(ctx, model.ProjectItemTask{
			ID:       id,
			ItemID:   item.ID,
			Title:    input.Title,
			Position: position,
		})
		if errors.Is(err, model.ErrDuplicateID) {
			return refuse(model.ErrDuplicateID, "Task with id %s already exists", id)
		}
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

// resolveTask refuses a task that exists on another item, because a task is
// addressed through its item.
func resolveTask(ctx context.Context, tx store.Operations, itemRef, taskID string) (model.ProjectItemTask, error) {
	item, err := resolveItem(ctx, tx, itemRef)
	if err != nil {
		return model.ProjectItemTask{}, err
	}
	task, err := tx.GetTask(ctx, taskID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && task.ItemID != item.ID) {
		return model.ProjectItemTask{}, refuse(model.ErrNotFound, "project item task %s not found", taskID)
	}
	if err != nil {
		return model.ProjectItemTask{}, fmt.Errorf("getting task: %w", err)
	}
	return task, nil
}

func (s *Service) UpdateTask(itemRef, taskID string, input model.UpdateProjectItemTask) (*model.ProjectItemTask, error) {
	ctx := s.ctx()

	var updated model.ProjectItemTask
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := resolveTask(ctx, tx, itemRef, taskID)
		if err != nil {
			return err
		}

		next := current
		if input.Title != nil {
			next.Title = *input.Title
		}
		if input.Completed != nil {
			next.Completed = *input.Completed
			next.CompletedAt = stampCompletion(current.Completed, next.Completed, current.CompletedAt)
		}
		if input.Position != nil {
			next.Position = *input.Position
		}

		t, err := tx.UpdateTask(ctx, next)
		if err != nil {
			return fmt.Errorf("updating task: %w", err)
		}
		updated = t
		return logUndo(ctx, tx, model.UndoActionUpdate, model.UndoEntityTask, current.ID, taskRecordOf(current))
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *Service) DeleteTask(itemRef, taskID string) error {
	ctx := s.ctx()
	return s.store.InTx(ctx, func(tx store.Operations) error {
		current, err := resolveTask(ctx, tx, itemRef, taskID)
		if err != nil {
			return err
		}
		if err := tx.DeleteTask(ctx, current.ID); err != nil {
			return fmt.Errorf("deleting task: %w", err)
		}
		return logUndo(ctx, tx, model.UndoActionDelete, model.UndoEntityTask, current.ID, taskRecordOf(current))
	})
}

// --- Search ---

// Search leaves out archived items, which SearchItems keeps.
func (s *Service) Search(query string) ([]model.ProjectItem, error) {
	items, err := s.store.SearchItems(s.ctx(), query)
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return slices.DeleteFunc(items, func(item model.ProjectItem) bool { return item.Archived }), nil
}

// --- Filters ---

func (s *Service) ListBlocked() ([]model.ProjectItem, error) {
	items, err := s.store.ListBlockedItems(s.ctx())
	if err != nil {
		return nil, fmt.Errorf("listing blocked: %w", err)
	}
	return items, nil
}

// ListArchived orders by most recently updated.
func (s *Service) ListArchived(projectRef string) ([]model.ProjectItemInProject, error) {
	ctx := s.ctx()
	project, err := resolveProject(ctx, s.store, projectRef)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListItemsInProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("listing archived: %w", err)
	}
	archived := slices.DeleteFunc(items, func(item model.ProjectItemInProject) bool { return !item.Archived })
	slices.SortStableFunc(archived, func(a, b model.ProjectItemInProject) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return archived, nil
}
