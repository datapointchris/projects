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

// UndoEntry is one row of the undo log: which mutation ran, on what, who wrote it,
// and the state reversing it needs. PreviousState is the service's own JSON, which a
// store holds without reading. It is nil for a mutation whose reversal needs no
// state.
//
// Actor and Client are the writer, and an undo reaches only its own writer's
// entries. Actor is a person's subject or a service's client id, and Client is the
// client the write came through, so one person's machines keep separate stacks. A
// database with no API behind it binds no writer, and every entry it writes holds
// two empty strings.
type UndoEntry struct {
	ID            int64     `json:"id"`
	Action        string    `json:"action"`
	EntityType    string    `json:"entity_type"`
	EntityID      string    `json:"entity_id"`
	Actor         string    `json:"actor"`
	Client        string    `json:"client"`
	PreviousState *string   `json:"previous_state,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// UndoResult describes what an undo reversed.
type UndoResult struct {
	Description string
	Action      string
	EntityType  string
	EntityID    string
}
