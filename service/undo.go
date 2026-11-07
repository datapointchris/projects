package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite/generated"
)

func (s *Service) Undo() (*model.UndoResult, error) {
	ctx := s.ctx()
	entry, err := s.q.GetLatestUndoLog(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrNothingToUndo
		}
		return nil, fmt.Errorf("getting undo log: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning undo transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := s.q.WithTx(tx)

	result := &model.UndoResult{
		Description: fmt.Sprintf("undid %s on %s %s", entry.Action, entry.EntityType, shortEntityID(entry.EntityID)),
		Action:      entry.Action,
		EntityType:  entry.EntityType,
		EntityID:    entry.EntityID,
	}

	// Every mutation that writes an undo entry needs a case here. A type that
	// falls through would leave the entry consumed and the change standing,
	// which reads to the user as undo silently doing nothing.
	var undoErr error
	switch entry.EntityType {
	case "project":
		undoErr = undoProject(ctx, qtx, entry, result)
	case "item":
		undoErr = undoItem(ctx, qtx, entry, result)
	case "task":
		undoErr = undoTask(ctx, qtx, entry, result)
	case "membership":
		undoErr = undoMembership(ctx, qtx, entry, result)
	case "dependency":
		undoErr = undoDependency(ctx, qtx, entry, result)
	case "item_position":
		undoErr = undoItemPosition(ctx, qtx, entry, result)
	case "project_position":
		undoErr = undoProjectPosition(ctx, qtx, entry, result)
	default:
		return nil, fmt.Errorf("undoing unknown entity type %q", entry.EntityType)
	}
	if undoErr != nil {
		return nil, undoErr
	}

	if err := qtx.DeleteUndoLog(ctx, entry.ID); err != nil {
		return nil, fmt.Errorf("deleting undo log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing undo: %w", err)
	}

	return result, nil
}

func undoItem(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	switch entry.Action {
	case "create":
		if err := qtx.DeleteItem(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing create: %w", err)
		}
	case "update":
		if entry.PreviousState.Valid {
			var prev generated.ProjectItem
			if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous state: %w", err)
			}
			restored, err := qtx.UpdateItem(ctx, generated.UpdateItemParams{
				Title:     prev.Title,
				Notes:     prev.Notes,
				Repo:      prev.Repo,
				Completed: prev.Completed,
				Archived:  prev.Archived,
				ID:        entry.EntityID,
			})
			if err != nil {
				return fmt.Errorf("restoring previous state: %w", err)
			}
			item := toModelProjectItem(restored)
			result.Restored = &item
		}
	case "delete":
		if entry.PreviousState.Valid {
			snapshot, err := decodeItemSnapshot(entry.PreviousState.String)
			if err != nil {
				return err
			}
			if err := restoreItemSnapshot(ctx, qtx, snapshot); err != nil {
				return err
			}
			item := toModelProjectItem(snapshot.Item)
			result.Restored = &item
			for _, membership := range snapshot.Memberships {
				result.RestoredProjectIDs = append(result.RestoredProjectIDs, membership.ProjectID)
			}
			for _, dependency := range snapshot.Dependencies {
				result.RestoredDependencies = append(result.RestoredDependencies, model.ItemDependency{
					ItemID:      dependency.ItemID,
					DependsOnID: dependency.DependsOnID,
				})
			}
			for _, task := range snapshot.Tasks {
				result.RestoredTasks = append(result.RestoredTasks, toModelProjectItemTask(task))
			}
		}
	}
	return nil
}

func undoTask(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("task undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemTask
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous task state: %w", err)
	}

	detail := &model.UndoDetail{ItemID: prev.ItemID}
	result.Detail = detail

	if entry.Action == "create" {
		if err := qtx.DeleteTask(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing task create: %w", err)
		}
		detail.Removed = true
		return nil
	}

	// Update and delete both reverse to the recorded row. Upsert covers both:
	// the row is still there after an update, and gone after a delete.
	if err := qtx.UpsertTask(ctx, generated.UpsertTaskParams(prev)); err != nil {
		return fmt.Errorf("restoring task: %w", err)
	}
	task := toModelProjectItemTask(prev)
	detail.Task = &task
	return nil
}

func undoMembership(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("membership undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemMembership
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous membership state: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Removed:   entry.Action == "create",
	}

	if entry.Action == "create" {
		if err := qtx.RemoveItemFromProject(ctx, generated.RemoveItemFromProjectParams{
			ItemID:    prev.ItemID,
			ProjectID: prev.ProjectID,
		}); err != nil {
			return fmt.Errorf("undoing membership create: %w", err)
		}
		return nil
	}

	if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(prev)); err != nil {
		return fmt.Errorf("restoring membership: %w", err)
	}
	return nil
}

func undoDependency(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("dependency undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemDependency
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous dependency state: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:      prev.ItemID,
		DependsOnID: prev.DependsOnID,
		Removed:     entry.Action == "create",
	}

	if entry.Action == "create" {
		if err := qtx.RemoveDependency(ctx, generated.RemoveDependencyParams(prev)); err != nil {
			return fmt.Errorf("undoing dependency create: %w", err)
		}
		return nil
	}

	if err := qtx.UpsertDependency(ctx, generated.UpsertDependencyParams(prev)); err != nil {
		return fmt.Errorf("restoring dependency: %w", err)
	}
	return nil
}

func undoItemPosition(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("item reorder undo entry %d has no previous state", entry.ID)
	}
	var prev generated.ProjectItemMembership
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous item position: %w", err)
	}

	if err := qtx.UpdateItemPosition(ctx, generated.UpdateItemPositionParams{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Position:  prev.Position,
	}); err != nil {
		return fmt.Errorf("restoring item position: %w", err)
	}

	result.Detail = &model.UndoDetail{
		ItemID:    prev.ItemID,
		ProjectID: prev.ProjectID,
		Position:  int(prev.Position),
	}
	return nil
}

func undoProjectPosition(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	if !entry.PreviousState.Valid {
		return fmt.Errorf("project reorder undo entry %d has no previous state", entry.ID)
	}
	var prev generated.Project
	if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous project position: %w", err)
	}

	if err := qtx.UpdateProjectPosition(ctx, generated.UpdateProjectPositionParams{
		ID:       prev.ID,
		Position: prev.Position,
	}); err != nil {
		return fmt.Errorf("restoring project position: %w", err)
	}

	result.Detail = &model.UndoDetail{Position: int(prev.Position)}
	return nil
}

// undoProject reverses a logged project mutation.
func undoProject(ctx context.Context, qtx *generated.Queries, entry generated.UndoLog, result *model.UndoResult) error {
	switch entry.Action {
	case "create":
		if err := qtx.DeleteProject(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing project create: %w", err)
		}
	case "update":
		if entry.PreviousState.Valid {
			var prev generated.Project
			if err := json.Unmarshal([]byte(entry.PreviousState.String), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous project state: %w", err)
			}
			// Upsert rather than UpdateProject: the snapshot is the whole row,
			// and UpdateProject writes only name/description/position, so
			// undoing a complete or a drop would leave the status where it was.
			if err := qtx.UpsertProject(ctx, generated.UpsertProjectParams(prev)); err != nil {
				return fmt.Errorf("restoring previous project state: %w", err)
			}
			restored, err := qtx.GetProject(ctx, entry.EntityID)
			if err != nil {
				return fmt.Errorf("reading back the restored project: %w", err)
			}
			project := toModelProject(restored)
			result.RestoredProject = &project
		}
	case "delete":
		if entry.PreviousState.Valid {
			snapshot, err := decodeProjectSnapshot(entry.PreviousState.String)
			if err != nil {
				return err
			}
			if err := restoreProjectSnapshot(ctx, qtx, snapshot); err != nil {
				return err
			}
			project := toModelProject(snapshot.Project)
			result.RestoredProject = &project
			for _, membership := range snapshot.Memberships {
				result.RestoredItemIDs = append(result.RestoredItemIDs, membership.ItemID)
			}
		}
	}
	return nil
}

// shortEntityID names whatever an undo entry points at. It stays the UUID tail
// even for items, which display their number everywhere else: undoing a create
// leaves nothing to look the number up on, so the one identifier that works for
// every entry is the id the entry already carries.
//
// UUIDv7 front-loads the millisecond timestamp, so a prefix is identical for
// everything created in the same ~65s window. The entropy is in the tail.
func shortEntityID(id string) string {
	if len(id) >= 8 {
		return id[len(id)-8:]
	}
	return id
}

func (s *Service) CanUndo() (bool, error) {
	count, err := s.q.CountUndoLogs(s.ctx())
	if err != nil {
		return false, fmt.Errorf("counting undo logs: %w", err)
	}
	return count > 0, nil
}

// itemSnapshot is everything a delete destroys. The row alone is not enough
// to undo one: memberships, dependencies, and tasks all cascade. An item
// restored without its memberships belongs to no project, which the TUI
// cannot show at all, because it builds every view by walking projects.
type itemSnapshot struct {
	Item         generated.ProjectItem             `json:"item"`
	Memberships  []generated.ProjectItemMembership `json:"memberships"`
	Dependencies []generated.ProjectItemDependency `json:"dependencies"`
	Tasks        []generated.ProjectItemTask       `json:"tasks"`
}

// projectSnapshot is the project equivalent. Memberships cascade off a
// project delete; the items on the other end of them do not.
type projectSnapshot struct {
	Project     generated.Project                 `json:"project"`
	Memberships []generated.ProjectItemMembership `json:"memberships"`
}

func captureItem(ctx context.Context, q *generated.Queries, id string) (*itemSnapshot, error) {
	item, err := q.GetItem(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item for delete: %w", err)
	}
	memberships, err := q.GetItemMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item memberships: %w", err)
	}
	dependencies, err := q.GetDependenciesInvolvingItem(ctx, generated.GetDependenciesInvolvingItemParams{
		ItemID:      id,
		DependsOnID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("capturing item dependencies: %w", err)
	}
	tasks, err := q.ListTasksByItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item tasks: %w", err)
	}
	return &itemSnapshot{
		Item:         item,
		Memberships:  memberships,
		Dependencies: dependencies,
		Tasks:        tasks,
	}, nil
}

func captureProject(ctx context.Context, q *generated.Queries, id string) (*projectSnapshot, error) {
	project, err := q.GetProject(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting project for delete: %w", err)
	}
	memberships, err := q.GetProjectMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing project memberships: %w", err)
	}
	return &projectSnapshot{Project: project, Memberships: memberships}, nil
}

// decodeItemSnapshot accepts either shape of previous_state: the snapshot
// written now, or the bare item row written before snapshots existed. Undo
// logs are not pruned, so pre-existing databases still hold the old shape.
func decodeItemSnapshot(raw string) (*itemSnapshot, error) {
	var snapshot itemSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err == nil && snapshot.Item.ID != "" {
		return &snapshot, nil
	}
	var item generated.ProjectItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, fmt.Errorf("unmarshaling deleted item state: %w", err)
	}
	return &itemSnapshot{Item: item}, nil
}

func decodeProjectSnapshot(raw string) (*projectSnapshot, error) {
	var snapshot projectSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err == nil && snapshot.Project.ID != "" {
		return &snapshot, nil
	}
	var project generated.Project
	if err := json.Unmarshal([]byte(raw), &project); err != nil {
		return nil, fmt.Errorf("unmarshaling deleted project state: %w", err)
	}
	return &projectSnapshot{Project: project}, nil
}

// restoreItemSnapshot puts the row back with its original timestamps, then
// reattaches whatever cascaded off it.
func restoreItemSnapshot(ctx context.Context, qtx *generated.Queries, snapshot *itemSnapshot) error {
	if err := qtx.UpsertItem(ctx, generated.UpsertItemParams(snapshot.Item)); err != nil {
		return fmt.Errorf("recreating deleted item: %w", err)
	}
	for _, membership := range snapshot.Memberships {
		if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(membership)); err != nil {
			return fmt.Errorf("restoring item membership: %w", err)
		}
	}
	for _, dependency := range snapshot.Dependencies {
		if err := qtx.UpsertDependency(ctx, generated.UpsertDependencyParams(dependency)); err != nil {
			return fmt.Errorf("restoring item dependency: %w", err)
		}
	}
	for _, task := range snapshot.Tasks {
		if err := qtx.UpsertTask(ctx, generated.UpsertTaskParams(task)); err != nil {
			return fmt.Errorf("restoring item task: %w", err)
		}
	}
	return nil
}

func restoreProjectSnapshot(ctx context.Context, qtx *generated.Queries, snapshot *projectSnapshot) error {
	if err := qtx.UpsertProject(ctx, generated.UpsertProjectParams(snapshot.Project)); err != nil {
		return fmt.Errorf("recreating deleted project: %w", err)
	}
	for _, membership := range snapshot.Memberships {
		if err := qtx.RestoreMembership(ctx, generated.RestoreMembershipParams(membership)); err != nil {
			return fmt.Errorf("restoring project membership: %w", err)
		}
	}
	return nil
}

func (s *Service) logUndo(qtx *generated.Queries, action, entityType string, entityID string, previousState any) error {
	var prevJSON sql.NullString
	if previousState != nil {
		data, err := json.Marshal(previousState)
		if err != nil {
			return fmt.Errorf("marshaling previous state: %w", err)
		}
		prevJSON = sql.NullString{String: string(data), Valid: true}
	}
	return qtx.InsertUndoLog(s.ctx(), generated.InsertUndoLogParams{
		Action:        action,
		EntityType:    entityType,
		EntityID:      entityID,
		PreviousState: prevJSON,
	})
}
