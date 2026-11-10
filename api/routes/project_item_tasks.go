package routes

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/model"
)

func (t *table) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := t.scoped(r).ListTasks(r.PathValue("item_id"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(tasks, wire.NewProjectItemTask))
}

func (t *table) createTask(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	input := model.CreateProjectItemTask{
		ID:       body.optionalUUID("id"),
		Title:    body.requiredString("title"),
		Position: body.intOr("position", 0),
	}
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	created, err := t.scoped(r).CreateTask(r.PathValue("item_id"), input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.NewProjectItemTask(*created))
}

// pathTaskID refuses a task id that is no UUID. A task has no other handle, so
// anything else cannot name one.
func pathTaskID(r *http.Request) (string, []wire.ValidationError) {
	id := r.PathValue("task_id")
	if _, err := uuid.Parse(id); err != nil {
		return "", []wire.ValidationError{{Type: "uuid_parsing", Loc: []any{"path", "task_id"}, Msg: messageNotAUUID, Input: id}}
	}
	return id, nil
}

func (t *table) updateTask(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	input := model.UpdateProjectItemTask{
		Title:     body.partialString("title"),
		Completed: body.partialBool("completed"),
		Position:  body.partialInt("position"),
	}
	taskID, problems := pathTaskID(r)
	if refuseInvalid(w, r, problems, body.refusals()) {
		return
	}
	updated, err := t.scoped(r).UpdateTask(r.PathValue("item_id"), taskID, input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProjectItemTask(*updated))
}

func (t *table) deleteTask(w http.ResponseWriter, r *http.Request) {
	taskID, problems := pathTaskID(r)
	if refuseInvalid(w, r, problems) {
		return
	}
	if err := t.scoped(r).DeleteTask(r.PathValue("item_id"), taskID); err != nil {
		t.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *table) undo(w http.ResponseWriter, r *http.Request) {
	result, err := t.scoped(r).Undo()
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewUndoResult(*result))
}

func (t *table) undoState(w http.ResponseWriter, r *http.Request) {
	canUndo, err := t.scoped(r).CanUndo()
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.UndoState{CanUndo: canUndo})
}
