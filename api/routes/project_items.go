package routes

import (
	"net/http"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/model"
)

func (t *table) listItems(w http.ResponseWriter, r *http.Request) {
	query := readRequestQuery(r)
	filter := model.ItemFilter{
		Status: query.stringOr("status", model.ItemStatusOpen),
		Repo:   query.optional("repo"),
		Since:  query.instant("since"),
		Until:  query.instant("until"),
		Limit:  query.limit(),
	}
	if refuseInvalid(w, r, query.problems) {
		return
	}
	items, err := t.scoped(r).ListItems(filter)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(items, wire.NewProjectItem))
}

func (t *table) listBlockedItems(w http.ResponseWriter, r *http.Request) {
	items, err := t.scoped(r).ListBlockedItems(readRequestQuery(r).optional("repo"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(items, wire.NewProjectItem))
}

func (t *table) searchItems(w http.ResponseWriter, r *http.Request) {
	query := readRequestQuery(r)
	words := query.required("q")
	if refuseInvalid(w, r, query.problems) {
		return
	}
	items, err := t.scoped(r).SearchItems(words, query.optional("repo"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(items, wire.NewProjectItem))
}

func (t *table) createItem(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	input := model.CreateProjectItem{
		ID:         body.optionalUUID("id"),
		Title:      body.requiredString("title"),
		Notes:      body.nullableString("notes"),
		Repo:       body.nullableString("repo"),
		ProjectIDs: body.requiredStringList("project_ids"),
	}
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	created, err := t.scoped(r).CreateItem(input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.NewProjectItemDetail(*created))
}

func (t *table) readItem(w http.ResponseWriter, r *http.Request) {
	item, err := t.scoped(r).GetItem(r.PathValue("id"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProjectItemDetail(*item))
}

func (t *table) updateItem(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	notes, clearNotes := body.clearableString("notes")
	repo, clearRepo := body.clearableString("repo")
	input := model.UpdateProjectItem{
		Title:      body.partialString("title"),
		Notes:      notes,
		ClearNotes: clearNotes,
		Repo:       repo,
		ClearRepo:  clearRepo,
		Completed:  body.partialBool("completed"),
		Archived:   body.partialBool("archived"),
	}
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	svc := t.scoped(r)
	updated, err := svc.UpdateItem(r.PathValue("id"), input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	filed, err := svc.GetItem(updated.ID)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProjectItem(*filed))
}

func (t *table) deleteItem(w http.ResponseWriter, r *http.Request) {
	if err := t.scoped(r).DeleteItem(r.PathValue("id")); err != nil {
		t.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *table) reorderItem(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	projectRef := body.requiredString("project_id")
	position := body.requiredInt("position")
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	svc := t.scoped(r)
	itemRef := r.PathValue("id")
	if err := svc.ReorderItem(itemRef, projectRef, position); err != nil {
		t.fail(w, r, err)
		return
	}
	item, err := svc.GetItemInProject(itemRef, projectRef)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProjectItemInProject(*item))
}

func (t *table) listItemProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := t.scoped(r).GetItemProjects(r.PathValue("id"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(projects, wire.NewProject))
}

func (t *table) addItemToProject(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	projectRef := body.requiredString("project_id")
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	svc := t.scoped(r)
	if err := svc.AddToProject(r.PathValue("id"), projectRef); err != nil {
		t.fail(w, r, err)
		return
	}
	project, err := svc.ResolveProject(projectRef)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.NewProject(*project))
}

func (t *table) removeItemFromProject(w http.ResponseWriter, r *http.Request) {
	if err := t.scoped(r).RemoveFromProject(r.PathValue("id"), r.PathValue("project_id")); err != nil {
		t.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *table) addDependency(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	dependsOn := body.requiredItemReference("depends_on_id")
	if refuseInvalid(w, r, body.refusals()) {
		return
	}
	svc := t.scoped(r)
	itemRef := r.PathValue("id")
	if err := svc.AddDependency(itemRef, dependsOn); err != nil {
		t.fail(w, r, err)
		return
	}
	item, err := svc.GetItem(itemRef)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.NewProjectItemDetail(*item))
}

func (t *table) removeDependency(w http.ResponseWriter, r *http.Request) {
	if err := t.scoped(r).RemoveDependency(r.PathValue("id"), r.PathValue("dep_id")); err != nil {
		t.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *table) listBlockers(w http.ResponseWriter, r *http.Request) {
	items, err := t.scoped(r).ListBlockerItems(r.PathValue("id"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(items, wire.NewProjectItem))
}
