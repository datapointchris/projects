package routes

import (
	"net/http"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/model"
)

func (t *table) listProjects(w http.ResponseWriter, r *http.Request) {
	query := readRequestQuery(r)
	filter := model.ProjectFilter{
		Status: query.stringOr("status", model.StatusActive),
		Repo:   query.optional("repo"),
		Limit:  query.limit(),
	}
	if refuseInvalid(w, r, query.problems) {
		return
	}
	projects, err := t.scoped(r).ListProjects(filter)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(projects, wire.NewProjectWithItemCount))
}

func (t *table) createProject(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	position := body.intOr("position", 0)
	input := model.CreateProject{
		ID:           body.optionalUUID("id"),
		Name:         body.requiredString("name"),
		Description:  body.nullableString("description"),
		Kind:         body.stringUnlessLeftOut("kind"),
		Status:       body.stringUnlessLeftOut("status"),
		StatusReason: body.nullableString("status_reason"),
		Position:     &position,
	}
	if refuseInvalid(w, r, body.problems) {
		return
	}
	created, err := t.scoped(r).CreateProject(input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, wire.NewProject(*created))
}

func (t *table) readProject(w http.ResponseWriter, r *http.Request) {
	project, err := t.scoped(r).GetProject(r.PathValue("id"))
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProjectWithItemCount(*project))
}

func (t *table) updateProject(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	description, clearDescription := body.clearableString("description")
	reason, clearReason := body.clearableString("status_reason")
	input := model.UpdateProject{
		Name:              body.partialString("name"),
		Description:       description,
		ClearDescription:  clearDescription,
		Kind:              body.partialString("kind"),
		Status:            body.partialString("status"),
		StatusReason:      reason,
		ClearStatusReason: clearReason,
		Position:          body.partialInt("position"),
	}
	if refuseInvalid(w, r, body.problems) {
		return
	}
	updated, err := t.scoped(r).UpdateProject(r.PathValue("id"), input)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.NewProject(*updated))
}

func (t *table) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := t.scoped(r).DeleteProject(r.PathValue("id")); err != nil {
		t.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *table) listProjectItems(w http.ResponseWriter, r *http.Request) {
	query := readRequestQuery(r)
	filter := model.ItemFilter{
		Status: query.stringOr("status", model.ItemStatusOpen),
		Since:  query.instant("since"),
		Until:  query.instant("until"),
		Limit:  query.limit(),
	}
	if refuseInvalid(w, r, query.problems) {
		return
	}
	items, err := t.scoped(r).ListProjectItems(r.PathValue("id"), filter)
	if err != nil {
		t.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wire.Map(items, wire.NewProjectItemInProject))
}
