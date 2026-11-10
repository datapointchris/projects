package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
)

// rowKey names one row: a project, an item or a task by its id, a membership by
// its item and project, and a dependency by its two items.
type rowKey struct {
	table string
	id    string
}

// entryRows is what one undo entry reaches. wrote is the rows its mutation wrote,
// which are the rows its undo writes back. needs is the rows those hang off, which
// a delete of either takes with it.
type entryRows struct {
	wrote []rowKey
	needs []rowKey
}

// undoRefusal answers why undoing mine would revert work that is not its own, or
// nil. Every entry after mine is another writer's, because mine is the newest its
// writer holds. A later write conflicts with mine when it wrote a row mine did,
// when undoing mine deletes a project or item it hangs off, or when it deleted a
// project or item mine hangs off.
//
// Undoing a project's create is refused while the project holds an item too. A
// writer's own later entries are undone first, so an item still there was put
// there by another writer, or by an entry of this writer's that a refusal dropped.
// Deleting the project would leave it in no project, where no board shows it.
func undoRefusal(ctx context.Context, tx store.Operations, mine model.UndoEntry) (*model.Refusal, error) {
	later, err := tx.ListUndoEntriesAfter(ctx, mine.ID)
	if err != nil {
		return nil, fmt.Errorf("reading the undo log after entry %d: %w", mine.ID, err)
	}
	undoDeletesParent := mine.Action == model.UndoActionCreate && holdsChildren(mine)
	reach, err := rowsOf(mine)
	if err != nil {
		return nil, err
	}
	for _, theirs := range later {
		theirReach, err := rowsOf(theirs)
		if err != nil {
			return nil, err
		}
		deletedParent := theirs.Action == model.UndoActionDelete && holdsChildren(theirs)
		if overlaps(reach.wrote, theirReach.wrote) ||
			(undoDeletesParent && overlaps(reach.wrote, theirReach.needs)) ||
			(deletedParent && overlaps(reach.needs, theirReach.wrote)) {
			return superseded("Dropped the undo of %s: %s ran %s after it, at %s.",
				describeEntry(ctx, tx, mine), writerName(theirs), describeEntry(ctx, tx, theirs),
				theirs.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")), nil
		}
	}

	if mine.Action != model.UndoActionCreate || mine.EntityType != model.UndoEntityProject {
		return nil, nil
	}
	held, err := tx.ListProjectMemberships(ctx, mine.EntityID)
	if err != nil {
		return nil, fmt.Errorf("reading what project %s holds: %w", mine.EntityID, err)
	}
	if len(held) == 0 {
		return nil, nil
	}
	return superseded("Dropped the undo of %s: it holds item %s, which it would leave in no project.",
		describeEntry(ctx, tx, mine),
		undoEntityHandle(ctx, tx, model.UndoEntry{EntityType: model.UndoEntityItem, EntityID: held[0].ItemID})), nil
}

// superseded is the refusal of an undo that is dropped rather than carried out, so
// the writer's next undo reaches the change before it.
func superseded(format string, args ...any) *model.Refusal {
	return &model.Refusal{Kind: model.ErrUndoSuperseded, Message: fmt.Sprintf(format, args...)}
}

// holdsChildren reports whether an entry's row is one others hang off, so deleting
// it deletes them too.
func holdsChildren(entry model.UndoEntry) bool {
	return entry.EntityType == model.UndoEntityProject || entry.EntityType == model.UndoEntityItem
}

// rowsOf reads what an entry reaches from its previous state.
func rowsOf(entry model.UndoEntry) (entryRows, error) {
	var rows entryRows
	state := func(into any) error {
		if entry.PreviousState == nil {
			return fmt.Errorf("%s undo entry %d has no previous state", entry.EntityType, entry.ID)
		}
		if err := json.Unmarshal([]byte(*entry.PreviousState), into); err != nil {
			return fmt.Errorf("reading undo entry %d: %w", entry.ID, err)
		}
		return nil
	}

	switch entry.EntityType {
	case model.UndoEntityProject, model.UndoEntityProjectPosition:
		rows.wrote = []rowKey{projectRow(entry.EntityID)}
		if entry.EntityType == model.UndoEntityProject && entry.Action == model.UndoActionDelete && entry.PreviousState != nil {
			snapshot, err := decodeProjectSnapshot(*entry.PreviousState)
			if err != nil {
				return rows, err
			}
			for _, orphan := range snapshot.Orphans {
				rows.wrote = append(rows.wrote, itemRow(orphan.Item.ID))
			}
		}
	case model.UndoEntityItem:
		rows.wrote = []rowKey{itemRow(entry.EntityID)}
		if entry.Action == model.UndoActionDelete && entry.PreviousState != nil {
			snapshot, err := decodeItemSnapshot(*entry.PreviousState)
			if err != nil {
				return rows, err
			}
			for _, membership := range snapshot.Memberships {
				rows.needs = append(rows.needs, projectRow(membership.ProjectID))
			}
			for _, edge := range snapshot.Dependencies {
				rows.needs = append(rows.needs, itemRow(edge.ItemID), itemRow(edge.DependsOnID))
			}
		}
	case model.UndoEntityTask:
		var task taskRecord
		if err := state(&task); err != nil {
			return rows, err
		}
		rows.wrote = []rowKey{{model.UndoEntityTask, entry.EntityID}}
		rows.needs = []rowKey{itemRow(task.ItemID)}
	case model.UndoEntityMembership:
		var membership membershipRecord
		if err := state(&membership); err != nil {
			return rows, err
		}
		rows.wrote = []rowKey{membershipRow(membership)}
		rows.needs = []rowKey{itemRow(membership.ItemID), projectRow(membership.ProjectID)}
	case model.UndoEntityDependency:
		var edge dependencyRecord
		if err := state(&edge); err != nil {
			return rows, err
		}
		rows.wrote = []rowKey{{model.UndoEntityDependency, edge.ItemID + "|" + edge.DependsOnID}}
		rows.needs = []rowKey{itemRow(edge.ItemID), itemRow(edge.DependsOnID)}
	case model.UndoEntityItemPosition:
		var position itemPositionRecord
		if err := state(&position); err != nil {
			return rows, err
		}
		rows.wrote = []rowKey{membershipRow(position.membershipRecord)}
		rows.needs = []rowKey{itemRow(position.ItemID), projectRow(position.ProjectID)}
		for _, sibling := range position.Siblings {
			rows.wrote = append(rows.wrote, membershipRow(sibling))
			rows.needs = append(rows.needs, itemRow(sibling.ItemID))
		}
	default:
		return rows, fmt.Errorf("undo entry %d has unknown entity type %q", entry.ID, entry.EntityType)
	}
	return rows, nil
}

func projectRow(id string) rowKey { return rowKey{model.UndoEntityProject, id} }

func itemRow(id string) rowKey { return rowKey{model.UndoEntityItem, id} }

func membershipRow(m membershipRecord) rowKey {
	return rowKey{model.UndoEntityMembership, m.ItemID + "|" + m.ProjectID}
}

func overlaps(a, b []rowKey) bool {
	return slices.ContainsFunc(a, func(key rowKey) bool { return slices.Contains(b, key) })
}

// describeEntry names an entry's mutation the way a finished undo's description
// does: its action, its kind of row, and that row's handle.
func describeEntry(ctx context.Context, tx store.Operations, entry model.UndoEntry) string {
	return fmt.Sprintf("%s on %s %s", entry.Action, entry.EntityType, undoEntityHandle(ctx, tx, entry))
}

// writerName names who wrote an entry: the client it came through, or the actor
// where it names none.
func writerName(entry model.UndoEntry) string {
	switch {
	case entry.Client != "":
		return entry.Client
	case entry.Actor != "":
		return entry.Actor
	default:
		return "another client"
	}
}
