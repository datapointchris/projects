// Package routes is the API's HTTP surface. A handler decodes, calls service and
// encodes, because a rule in a handler would have to land again for the other store.
package routes

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/service"
)

// prefix is where every route but the probes and the login metadata lives. Each
// route spells it out, so a search or a route reader finds the whole path.
const prefix = "/api/v1"

const (
	// requestDeadline sits under the server's WriteTimeout, so a database that stops
	// answering costs a client a 503 it can read, not a closed connection.
	requestDeadline = 10 * time.Second
	// readinessCheckDeadline sits under the readiness probe's timeoutSeconds, so the
	// probe reads the 503 rather than timing out on its own.
	readinessCheckDeadline = 2 * time.Second
)

// WithRequestDeadline bounds everything a request waits on, the write lock included.
func WithRequestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PublicPaths answer with no credential, because a cluster's probes hold none.
var PublicPaths = []string{"/health", "/ready"}

// ServiceScopes is which routes a service client's scope reaches, as registered
// patterns. A person's client reaches every route, and a service reaches only these.
var ServiceScopes = map[string][]string{
	"projects.items.read": {
		"GET /api/v1/project-items",
		"GET /api/v1/project-items/blocked",
		"GET /api/v1/project-items/search",
		"GET /api/v1/project-items/{id}",
		"GET /api/v1/project-items/{id}/blockers",
		"GET /api/v1/project-items/{item_id}/tasks",
		"GET /api/v1/projects/{id}/items",
		"GET /api/v1/version",
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
	version string
	ready   []ReadinessCheck
	mux     *http.ServeMux
}

// scoped is the service for one request, so its cancellation and its writer reach
// the store.
func (t *table) scoped(r *http.Request) *service.Service {
	return t.service.WithContext(r.Context())
}

// New registers every route. A wildcard takes an escaped slash, so a project name
// holding one is reached by escaping it. version is the release tag the build names.
func New(svc *service.Service, log *slog.Logger, version string, ready ...ReadinessCheck) *http.ServeMux {
	t := &table{service: svc, log: log, version: version, ready: ready, mux: http.NewServeMux()}
	mux := t.mux

	mux.HandleFunc("GET /health", t.health)
	mux.HandleFunc("GET /ready", t.readiness)
	mux.HandleFunc("/", t.unrouted)

	mux.HandleFunc("GET /api/v1/projects", t.listProjects)
	mux.HandleFunc("POST /api/v1/projects", t.createProject)
	mux.HandleFunc("GET /api/v1/projects/{id}/items", t.listProjectItems)
	mux.HandleFunc("GET /api/v1/projects/{id}", t.readProject)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", t.updateProject)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", t.deleteProject)

	mux.HandleFunc("GET /api/v1/project-items", t.listItems)
	mux.HandleFunc("POST /api/v1/project-items", t.createItem)
	mux.HandleFunc("GET /api/v1/project-items/blocked", t.listBlockedItems)
	mux.HandleFunc("GET /api/v1/project-items/search", t.searchItems)
	mux.HandleFunc("GET /api/v1/project-items/{id}", t.readItem)
	mux.HandleFunc("PATCH /api/v1/project-items/{id}", t.updateItem)
	mux.HandleFunc("DELETE /api/v1/project-items/{id}", t.deleteItem)
	mux.HandleFunc("PATCH /api/v1/project-items/{id}/reorder", t.reorderItem)
	mux.HandleFunc("GET /api/v1/project-items/{id}/projects", t.listItemProjects)
	mux.HandleFunc("POST /api/v1/project-items/{id}/projects", t.addItemToProject)
	mux.HandleFunc("DELETE /api/v1/project-items/{id}/projects/{project_id}", t.removeItemFromProject)
	mux.HandleFunc("POST /api/v1/project-items/{id}/dependencies", t.addDependency)
	mux.HandleFunc("DELETE /api/v1/project-items/{id}/dependencies/{dep_id}", t.removeDependency)
	mux.HandleFunc("GET /api/v1/project-items/{id}/blockers", t.listBlockers)

	mux.HandleFunc("GET /api/v1/project-items/{item_id}/tasks", t.listTasks)
	mux.HandleFunc("POST /api/v1/project-items/{item_id}/tasks", t.createTask)
	mux.HandleFunc("PATCH /api/v1/project-items/{item_id}/tasks/{task_id}", t.updateTask)
	mux.HandleFunc("DELETE /api/v1/project-items/{item_id}/tasks/{task_id}", t.deleteTask)

	mux.HandleFunc("POST /api/v1/undo", t.undo)
	mux.HandleFunc("GET /api/v1/undo", t.undoState)

	mux.HandleFunc("GET /api/v1/version", t.readVersion)

	return mux
}

func (t *table) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wire.Probe{Status: "ok"})
}

// readVersion answers the release this server was built from. A client refused for
// a field it sent names it beside its own.
func (t *table) readVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wire.Version{Version: t.version})
}

// readiness answers 503 while a dependency does not, which takes a pod out of its
// Service without restarting it.
func (t *table) readiness(w http.ResponseWriter, r *http.Request) {
	for _, dependency := range t.ready {
		checking, cancel := context.WithTimeout(r.Context(), readinessCheckDeadline)
		err := dependency.Check(checking)
		cancel()
		if err != nil {
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
