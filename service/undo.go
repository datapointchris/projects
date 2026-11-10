package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
)

func (s *Service) Undo() (*model.UndoResult, error) {
	ctx := s.ctx()

	var result *model.UndoResult
	err := s.store.InTx(ctx, func(tx store.Operations) error {
		entry, err := tx.GetLatestUndoEntry(ctx)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return model.ErrNothingToUndo
			}
			return fmt.Errorf("getting undo log: %w", err)
		}

		result = &model.UndoResult{
			Description: fmt.Sprintf("undid %s on %s %s", entry.Action, entry.EntityType, undoEntityHandle(ctx, tx, entry)),
			Action:      entry.Action,
			EntityType:  entry.EntityType,
			EntityID:    entry.EntityID,
		}

		// Every mutation that writes an undo entry needs a case here. A type that
		// falls through would leave the entry consumed and the change standing,
		// which reads to the user as undo silently doing nothing.
		var undoErr error
		switch entry.EntityType {
		case model.UndoEntityProject:
			undoErr = undoProject(ctx, tx, entry)
		case model.UndoEntityItem:
			undoErr = undoItem(ctx, tx, entry)
		case model.UndoEntityTask:
			undoErr = undoTask(ctx, tx, entry)
		case model.UndoEntityMembership:
			undoErr = undoMembership(ctx, tx, entry)
		case model.UndoEntityDependency:
			undoErr = undoDependency(ctx, tx, entry)
		case model.UndoEntityItemPosition:
			undoErr = undoItemPosition(ctx, tx, entry)
		case model.UndoEntityProjectPosition:
			undoErr = undoProjectPosition(ctx, tx, entry)
		default:
			return fmt.Errorf("undoing unknown entity type %q", entry.EntityType)
		}
		if undoErr != nil {
			return undoErr
		}

		if err := tx.DeleteUndoEntry(ctx, entry.ID); err != nil {
			return fmt.Errorf("deleting undo log: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func undoItem(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	switch entry.Action {
	case model.UndoActionCreate:
		if err := tx.DeleteItem(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing create: %w", err)
		}
	case model.UndoActionUpdate:
		if entry.PreviousState != nil {
			var prev itemRecord
			if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous state: %w", err)
			}
			item := prev.toModel()
			item.ID = entry.EntityID
			if _, err := tx.UpdateItem(ctx, item); err != nil {
				return fmt.Errorf("restoring previous state: %w", err)
			}
		}
	case model.UndoActionDelete:
		if entry.PreviousState != nil {
			snapshot, err := decodeItemSnapshot(*entry.PreviousState)
			if err != nil {
				return err
			}
			if err := restoreItemSnapshot(ctx, tx, snapshot); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("undoing unknown item action %q", entry.Action)
	}
	return nil
}

func undoTask(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	if entry.PreviousState == nil {
		return fmt.Errorf("task undo entry %d has no previous state", entry.ID)
	}
	var prev taskRecord
	if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous task state: %w", err)
	}

	if entry.Action == model.UndoActionCreate {
		if err := tx.DeleteTask(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing task create: %w", err)
		}
		return nil
	}

	// Update and delete both reverse to the recorded row. Restore covers both:
	// the row is still there after an update, and gone after a delete.
	if err := tx.RestoreTask(ctx, prev.toModel()); err != nil {
		return fmt.Errorf("restoring task: %w", err)
	}
	return nil
}

func undoMembership(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	if entry.PreviousState == nil {
		return fmt.Errorf("membership undo entry %d has no previous state", entry.ID)
	}
	var prev membershipRecord
	if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous membership state: %w", err)
	}

	if entry.Action == model.UndoActionCreate {
		if err := tx.RemoveItemFromProject(ctx, prev.ItemID, prev.ProjectID); err != nil {
			return fmt.Errorf("undoing membership create: %w", err)
		}
		return nil
	}

	if err := restoreMembership(ctx, tx, prev.toModel()); err != nil {
		return fmt.Errorf("restoring membership: %w", err)
	}
	return nil
}

func undoDependency(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	if entry.PreviousState == nil {
		return fmt.Errorf("dependency undo entry %d has no previous state", entry.ID)
	}
	var prev dependencyRecord
	if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous dependency state: %w", err)
	}

	if entry.Action == model.UndoActionCreate {
		if err := tx.RemoveDependency(ctx, model.Dependency(prev)); err != nil {
			return fmt.Errorf("undoing dependency create: %w", err)
		}
		return nil
	}

	if err := restoreDependency(ctx, tx, model.Dependency(prev)); err != nil {
		return fmt.Errorf("restoring dependency: %w", err)
	}
	return nil
}

func undoItemPosition(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	if entry.PreviousState == nil {
		return fmt.Errorf("item reorder undo entry %d has no previous state", entry.ID)
	}
	var prev itemPositionRecord
	if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous item position: %w", err)
	}

	if err := tx.UpdateItemPosition(ctx, prev.toModel()); err != nil {
		return fmt.Errorf("restoring item position: %w", err)
	}
	for _, sibling := range prev.Siblings {
		if err := tx.UpdateItemPosition(ctx, sibling.toModel()); err != nil {
			return fmt.Errorf("restoring a sibling's position: %w", err)
		}
	}
	return nil
}

func undoProjectPosition(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	if entry.PreviousState == nil {
		return fmt.Errorf("project reorder undo entry %d has no previous state", entry.ID)
	}
	var prev projectRecord
	if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
		return fmt.Errorf("unmarshaling previous project position: %w", err)
	}

	if err := tx.UpdateProjectPosition(ctx, prev.ID, int(prev.Position)); err != nil {
		return fmt.Errorf("restoring project position: %w", err)
	}
	return nil
}

// undoProject reverses a logged project mutation.
func undoProject(ctx context.Context, tx store.Operations, entry model.UndoEntry) error {
	switch entry.Action {
	case model.UndoActionCreate:
		if err := tx.DeleteProject(ctx, entry.EntityID); err != nil {
			return fmt.Errorf("undoing project create: %w", err)
		}
	case model.UndoActionUpdate:
		if entry.PreviousState != nil {
			var prev projectRecord
			if err := json.Unmarshal([]byte(*entry.PreviousState), &prev); err != nil {
				return fmt.Errorf("unmarshaling previous project state: %w", err)
			}
			// Restore rather than UpdateProject: the snapshot is the whole row,
			// and UpdateProject writes only name/description/position, so
			// undoing a complete or a drop would leave the status where it was.
			if err := tx.RestoreProject(ctx, prev.toModel()); err != nil {
				return fmt.Errorf("restoring previous project state: %w", err)
			}
		}
	case model.UndoActionDelete:
		if entry.PreviousState != nil {
			snapshot, err := decodeProjectSnapshot(*entry.PreviousState)
			if err != nil {
				return err
			}
			if err := restoreProjectSnapshot(ctx, tx, snapshot); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("undoing unknown project action %q", entry.Action)
	}
	return nil
}

// undoEntityHandle names what an undo entry points at, read before the undo
// runs, so an item undone out of existence can still be looked up. An item
// shows its number, as it does everywhere else. An item with no number, or one
// the store cannot read, falls back to the UUID tail, as does every other kind
// of entry, because projects and tasks have no number of their own.
func undoEntityHandle(ctx context.Context, tx store.Operations, entry model.UndoEntry) string {
	if entry.EntityType == model.UndoEntityItem {
		if item, err := tx.GetItem(ctx, entry.EntityID); err == nil && item.Number != nil {
			return strconv.Itoa(*item.Number)
		}
	}
	return shortEntityID(entry.EntityID)
}

// shortEntityID is the display handle for an id with no number.
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
	count, err := s.store.CountUndoEntries(s.ctx())
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
	Item         itemRecord         `json:"item"`
	Memberships  []membershipRecord `json:"memberships"`
	Dependencies []dependencyRecord `json:"dependencies"`
	Tasks        []taskRecord       `json:"tasks"`
}

// projectSnapshot is the project equivalent. Memberships cascade off a project
// delete, and the items on the other end of them stay unless the project was the
// last to hold them. Orphans are those.
type projectSnapshot struct {
	Project     projectRecord      `json:"project"`
	Memberships []membershipRecord `json:"memberships"`
	Orphans     []itemSnapshot     `json:"orphans,omitempty"`
}

func captureItem(ctx context.Context, reader store.Operations, id string) (*itemSnapshot, error) {
	item, err := reader.GetItem(ctx, id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, fmt.Errorf("item %q: %w", id, model.ErrNotFound)
		}
		return nil, fmt.Errorf("getting item for delete: %w", err)
	}
	memberships, err := reader.ListItemMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item memberships: %w", err)
	}
	dependencies, err := reader.ListDependenciesInvolvingItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item dependencies: %w", err)
	}
	tasks, err := reader.ListTasksByItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing item tasks: %w", err)
	}
	return &itemSnapshot{
		Item:         itemRecordOf(item),
		Memberships:  membershipRecordsOf(memberships),
		Dependencies: dependencyRecordsOf(dependencies),
		Tasks:        taskRecordsOf(tasks),
	}, nil
}

func captureProject(ctx context.Context, reader store.Operations, id string, orphanIDs []string) (*projectSnapshot, error) {
	project, err := reader.GetProject(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting project for delete: %w", err)
	}
	memberships, err := reader.ListProjectMemberships(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("capturing project memberships: %w", err)
	}
	snapshot := &projectSnapshot{Project: projectRecordOf(project), Memberships: membershipRecordsOf(memberships)}
	for _, orphanID := range orphanIDs {
		orphan, err := captureItem(ctx, reader, orphanID)
		if err != nil {
			return nil, err
		}
		snapshot.Orphans = append(snapshot.Orphans, *orphan)
	}
	return snapshot, nil
}

// decodeItemSnapshot accepts either shape of previous_state: the snapshot
// written now, or the bare item row written before snapshots existed. Undo
// logs are not pruned, so pre-existing databases still hold the old shape.
func decodeItemSnapshot(raw string) (*itemSnapshot, error) {
	var snapshot itemSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err == nil && snapshot.Item.ID != "" {
		return &snapshot, nil
	}
	var item itemRecord
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
	var project projectRecord
	if err := json.Unmarshal([]byte(raw), &project); err != nil {
		return nil, fmt.Errorf("unmarshaling deleted project state: %w", err)
	}
	return &projectSnapshot{Project: project}, nil
}

// restoreItemSnapshot puts the row back with its original timestamps, then
// reattaches whatever cascaded off it.
func restoreItemSnapshot(ctx context.Context, tx store.Operations, snapshot *itemSnapshot) error {
	if err := tx.RestoreItem(ctx, snapshot.Item.toModel()); err != nil {
		return fmt.Errorf("recreating deleted item: %w", err)
	}
	return restoreWhatCascadedOffItem(ctx, tx, snapshot)
}

func restoreWhatCascadedOffItem(ctx context.Context, tx store.Operations, snapshot *itemSnapshot) error {
	for _, membership := range snapshot.Memberships {
		if err := restoreMembership(ctx, tx, membership.toModel()); err != nil {
			return fmt.Errorf("restoring item membership: %w", err)
		}
	}
	for _, dependency := range snapshot.Dependencies {
		if err := restoreDependency(ctx, tx, model.Dependency(dependency)); err != nil {
			return fmt.Errorf("restoring item dependency: %w", err)
		}
	}
	for _, task := range snapshot.Tasks {
		if err := tx.RestoreTask(ctx, task.toModel()); err != nil {
			return fmt.Errorf("restoring item task: %w", err)
		}
	}
	return nil
}

// restoreProjectSnapshot puts every row back before any child, because one orphan
// may depend on another.
func restoreProjectSnapshot(ctx context.Context, tx store.Operations, snapshot *projectSnapshot) error {
	if err := tx.RestoreProject(ctx, snapshot.Project.toModel()); err != nil {
		return fmt.Errorf("recreating deleted project: %w", err)
	}
	for i := range snapshot.Orphans {
		if err := tx.RestoreItem(ctx, snapshot.Orphans[i].Item.toModel()); err != nil {
			return fmt.Errorf("recreating the project's finished item: %w", err)
		}
	}
	for i := range snapshot.Orphans {
		if err := restoreWhatCascadedOffItem(ctx, tx, &snapshot.Orphans[i]); err != nil {
			return err
		}
	}
	for _, membership := range snapshot.Memberships {
		if err := restoreMembership(ctx, tx, membership.toModel()); err != nil {
			return fmt.Errorf("restoring project membership: %w", err)
		}
	}
	return nil
}

// restoreMembership puts a membership back only while its item and its project
// both exist. A row can leave with no undo entry of its own, when something
// outside the service deletes it, and a link to it would fail the foreign key.
// That failure would roll back the undo and leave its entry at the top of the
// log, so every later undo would fail the same way. Skipping the link restores
// what is left and lets the entry go.
func restoreMembership(ctx context.Context, tx store.Operations, membership model.Membership) error {
	itemThere, err := itemExists(ctx, tx, membership.ItemID)
	if err != nil || !itemThere {
		return err
	}
	if _, err := tx.GetProject(ctx, membership.ProjectID); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		return err
	}
	return tx.RestoreMembership(ctx, membership)
}

// restoreDependency puts an edge back only while both its items exist, for the
// reason restoreMembership gives.
func restoreDependency(ctx context.Context, tx store.Operations, edge model.Dependency) error {
	for _, id := range []string{edge.ItemID, edge.DependsOnID} {
		there, err := itemExists(ctx, tx, id)
		if err != nil || !there {
			return err
		}
	}
	return tx.RestoreDependency(ctx, edge)
}

func itemExists(ctx context.Context, tx store.Operations, id string) (bool, error) {
	if _, err := tx.GetItem(ctx, id); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// logUndo records a mutation in the transaction that made it, because undo reads
// the log newest first and a mutation left out would let it reach past that one.
func logUndo(ctx context.Context, tx store.Operations, action, entityType, entityID string, previousState any) error {
	entry := model.UndoEntry{Action: action, EntityType: entityType, EntityID: entityID}
	if previousState != nil {
		data, err := json.Marshal(previousState)
		if err != nil {
			return fmt.Errorf("marshaling previous state: %w", err)
		}
		text := string(data)
		entry.PreviousState = &text
	}
	return tx.InsertUndoEntry(ctx, entry)
}
