package model

import "time"

// The action an undo entry records. Entries are never pruned, so each value is a
// format on disk.
const (
	UndoActionCreate = "create"
	UndoActionUpdate = "update"
	UndoActionDelete = "delete"
)

// What an undo entry acts on. Each value is a format on disk, as the actions are.
const (
	UndoEntityProject         = "project"
	UndoEntityItem            = "item"
	UndoEntityTask            = "task"
	UndoEntityMembership      = "membership"
	UndoEntityDependency      = "dependency"
	UndoEntityItemPosition    = "item_position"
	UndoEntityProjectPosition = "project_position"
)

// UndoEntry is one row of the undo log: which mutation ran, on what, and the state
// reversing it needs. PreviousState is the service's own JSON, which a store holds
// without reading. It is nil for a mutation whose reversal needs no state.
type UndoEntry struct {
	ID            int64     `json:"id"`
	Action        string    `json:"action"`
	EntityType    string    `json:"entity_type"`
	EntityID      string    `json:"entity_id"`
	PreviousState *string   `json:"previous_state,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// UndoResult describes what an undo actually did. A sync layer needs more than
// a display string: the pending queue still holds operations describing the
// state the undo just reversed, and reconciling them requires knowing which
// entity changed and what it looks like now.
type UndoResult struct {
	Description string
	Action      string
	EntityType  string
	EntityID    string

	// Restored is the local row after the undo, or nil when the undo removed it.
	// Exactly one of Restored / RestoredProject can be set, matching EntityType.
	Restored        *ProjectItem
	RestoredProject *Project

	// Rows that cascaded off a delete and came back with it. Sync has to push
	// these, not just the row: a pull replaces memberships and dependencies
	// wholesale from server state and drops tasks the server does not know
	// about, so anything restored only locally is erased on the next pull.
	RestoredProjectIDs   []string // projects an item was restored into
	RestoredItemIDs      []string // items a restored project held
	RestoredTasks        []ProjectItemTask
	RestoredDependencies []ItemDependency

	// Detail is set for entity types other than item and project.
	Detail *UndoDetail
}

// ItemDependency is one edge of the blocking graph: ItemID is blocked until
// DependsOnID is complete.
type ItemDependency struct {
	ItemID      string `json:"item_id"`
	DependsOnID string `json:"depends_on_id"`
}

// UndoDetail describes a reversal of anything other than an item or project
// row — a task, a membership, a dependency, or a reorder. Only the fields
// relevant to the result's EntityType are set. The sync layer needs it to
// push the same reversal rather than guessing from the entity ID alone.
type UndoDetail struct {
	// Removed reports whether the undo took the row away rather than putting
	// it back. It is what separates reversing an add from reversing a remove,
	// which are otherwise indistinguishable at the sync layer.
	Removed bool

	ItemID      string // owning item: task, membership, dependency, item reorder
	ProjectID   string // membership and item reorder
	DependsOnID string // dependency
	Position    int    // reorder

	// Task is the row as it stands after the undo, nil when the undo removed it.
	Task *ProjectItemTask
}
