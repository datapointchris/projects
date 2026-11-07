// Package routes is the API's HTTP surface. A handler decodes, calls service and
// encodes, because a rule in a handler would have to land again for the other store.
package routes

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/service"
)

// prefix is where every route but the probes and the login metadata lives.
const prefix = "/api/v1"

// PublicPaths answer with no credential, because a cluster's probes hold none.
var PublicPaths = []string{"/health", "/ready"}

// ServiceScopes is which routes a service client's scope reaches, as registered
// patterns. A person's client reaches every route, and a service reaches only these.
var ServiceScopes = map[string][]string{
	"projects.items.read": {
		"GET " + prefix + "/project-items",
		"GET " + prefix + "/project-items/blocked",
		"GET " + prefix + "/project-items/search",
		"GET " + prefix + "/project-items/{id}",
		"GET " + prefix + "/project-items/{id}/blockers",
		"GET " + prefix + "/project-items/{item_id}/tasks",
		"GET " + prefix + "/projects/{id}/items",
	},
}

// ReadinessCheck answers nil when the named dependency can serve a request.
type ReadinessCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// table is what the handlers share.
type table struct {
	service *service.Service
	log     *slog.Logger
	ready   []ReadinessCheck
	mux     *http.ServeMux
}

// scoped is the service for one request, so its cancellation and its writer reach
// the store.
func (t *table) scoped(r *http.Request) *service.Service {
	return t.service.WithContext(r.Context())
}

// New registers every route. A wildcard takes an escaped slash, so a project name
// holding one is reached by escaping it.
func New(svc *service.Service, log *slog.Logger, ready ...ReadinessCheck) *http.ServeMux {
	t := &table{service: svc, log: log, ready: ready, mux: http.NewServeMux()}
	mux := t.mux

	mux.HandleFunc("GET /health", t.health)
	mux.HandleFunc("GET /ready", t.readiness)
	mux.HandleFunc("/", t.unrouted)

	mux.HandleFunc("GET "+prefix+"/projects", t.listProjects)
	mux.HandleFunc("POST "+prefix+"/projects", t.createProject)
	mux.HandleFunc("GET "+prefix+"/projects/{id}/items", t.listProjectItems)
	mux.HandleFunc("GET "+prefix+"/projects/{id}", t.readProject)
	mux.HandleFunc("PATCH "+prefix+"/projects/{id}", t.updateProject)
	mux.HandleFunc("DELETE "+prefix+"/projects/{id}", t.deleteProject)

	mux.HandleFunc("GET "+prefix+"/project-items", t.listItems)
	mux.HandleFunc("POST "+prefix+"/project-items", t.createItem)
	mux.HandleFunc("GET "+prefix+"/project-items/blocked", t.listBlockedItems)
	mux.HandleFunc("GET "+prefix+"/project-items/search", t.searchItems)
	mux.HandleFunc("GET "+prefix+"/project-items/{id}", t.readItem)
	mux.HandleFunc("PATCH "+prefix+"/project-items/{id}", t.updateItem)
	mux.HandleFunc("DELETE "+prefix+"/project-items/{id}", t.deleteItem)
	mux.HandleFunc("PATCH "+prefix+"/project-items/{id}/reorder", t.reorderItem)
	mux.HandleFunc("GET "+prefix+"/project-items/{id}/projects", t.listItemProjects)
	mux.HandleFunc("POST "+prefix+"/project-items/{id}/projects", t.addItemToProject)
	mux.HandleFunc("DELETE "+prefix+"/project-items/{id}/projects/{project_id}", t.removeItemFromProject)
	mux.HandleFunc("POST "+prefix+"/project-items/{id}/dependencies", t.addDependency)
	mux.HandleFunc("DELETE "+prefix+"/project-items/{id}/dependencies/{dep_id}", t.removeDependency)
	mux.HandleFunc("GET "+prefix+"/project-items/{id}/blockers", t.listBlockers)

	mux.HandleFunc("GET "+prefix+"/project-items/{item_id}/tasks", t.listTasks)
	mux.HandleFunc("POST "+prefix+"/project-items/{item_id}/tasks", t.createTask)
	mux.HandleFunc("PATCH "+prefix+"/project-items/{item_id}/tasks/{task_id}", t.updateTask)
	mux.HandleFunc("DELETE "+prefix+"/project-items/{item_id}/tasks/{task_id}", t.deleteTask)

	mux.HandleFunc("POST "+prefix+"/undo", t.undo)
	mux.HandleFunc("GET "+prefix+"/undo", t.undoState)

	return mux
}

func (t *table) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wire.Probe{Status: "ok"})
}

// readiness answers 503 while a dependency does not, which takes a pod out of its
// Service without restarting it.
func (t *table) readiness(w http.ResponseWriter, r *http.Request) {
	for _, dependency := range t.ready {
		if err := dependency.Check(r.Context()); err != nil {
			t.log.WarnContext(r.Context(), "not ready", "dependency", dependency.Name, "err", err)
			writeJSON(w, http.StatusServiceUnavailable, wire.Probe{Status: "unavailable", Reason: dependency.Name})
			return
		}
	}
	writeJSON(w, http.StatusOK, wire.Probe{Status: "ready"})
}

// unrouted answers every path no route holds, and every method a route's path does
// not take. The catch-all takes both away from the mux's plain-text answers, so a
// client reads the same body it reads for any other refusal.
func (t *table) unrouted(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") && path != prefix && !strings.HasPrefix(path, prefix+"/") {
		writeRefusal(w, r, http.StatusGone, "this API serves "+prefix+"; update the client calling it", nil)
		return
	}
	allowed := t.methodsTakenAt(r)
	if len(allowed) == 0 {
		writeRefusal(w, r, http.StatusNotFound, "no route answers "+path, nil)
		return
	}
	allow := strings.Join(allowed, ", ")
	w.Header().Set("Allow", allow)
	writeRefusal(w, r, http.StatusMethodNotAllowed, path+" takes "+allow+", not "+r.Method, nil)
}

// methodsTakenAt asks the mux which methods reach a real route at this path. Every
// other answer is the catch-all's own pattern.
func (t *table) methodsTakenAt(r *http.Request) []string {
	var allowed []string
	probe := r.Clone(r.Context())
	for _, method := range []string{http.MethodDelete, http.MethodGet, http.MethodPatch, http.MethodPost, http.MethodPut} {
		probe.Method = method
		if _, pattern := t.mux.Handler(probe); pattern != "/" {
			allowed = append(allowed, method)
		}
	}
	slices.Sort(allowed)
	return allowed
}
