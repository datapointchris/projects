package routes_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goapiauth/authtest"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/datapointchris/projects/api/authentication"
	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/api/routes"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
)

// api is the routes behind the authentication production wraps them in, with a
// person's token, the same person's token from a second machine, and a service's.
// Any rule's store answers here, and the service's own suite proves the two stores
// agree.
type api struct {
	handler http.Handler
	person  string
	laptop  string
	service string
}

func newAPI(t *testing.T) api {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	log := slog.New(slog.DiscardHandler)
	provider := authtest.NewProvider(t)
	config := authentication.Config(provider.URL)
	verifier := provider.Verifier(t, config.PersonPrefix, config.ServicePrefix)
	mux := routes.New(service.New(sqlite.New(database)), log)
	authentication.Metadata(mux, "https://projects.example.com", provider.URL)
	protected := authentication.Protect(mux, routes.ServiceScopes, verifier, nil, "", log, routes.PublicPaths...)

	return api{
		handler: requestlog.Middleware(log, protected),
		person:  provider.Person(t, config.PersonPrefix+"test", "a-person"),
		laptop:  provider.Person(t, config.PersonPrefix+"laptop", "a-person"),
		service: provider.Sign(t, "at+jwt", map[string]any{"client_id": config.ServicePrefix + "reader", "scp": []string{"projects.items.read"}}),
	}
}

func (a api) send(t *testing.T, token, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&reader).Encode(body); err != nil {
			t.Fatalf("encoding body: %v", err)
		}
	}
	request := httptest.NewRequest(method, path, &reader)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

// created sends a write the test depends on and answers the id it created.
func (a api) created(t *testing.T, path string, body any) string {
	t.Helper()
	status, answer := a.send(t, a.person, http.MethodPost, path, body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s answered %d: %s", path, status, answer)
	}
	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answer, &row); err != nil {
		t.Fatalf("reading %s: %v", answer, err)
	}
	return row.ID
}

func schema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "schemas", name+".schema.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening schema: %v", err)
	}
	defer func() { _ = file.Close() }()
	document, err := jsonschema.UnmarshalJSON(file)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(name, document); err != nil {
		t.Fatalf("adding %s: %v", path, err)
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		t.Fatalf("compiling %s: %v", path, err)
	}
	return compiled
}

// A client builds its kind and status choices from the schemas, so each lists what
// model holds, and the kind a project gets when its create names none.
func TestTheSchemasListTheKindsAndStatusesModelHolds(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "schemas", "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sites := 0
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch node := node.(type) {
		case []any:
			for _, child := range node {
				walk(path, child)
			}
		case map[string]any:
			description, _ := node["description"].(string)
			var want []string
			switch {
			case strings.Contains(description, "project_kinds table"):
				want = model.ProjectKinds
				if node["default"] != model.KindLife {
					t.Errorf("%s: a kind's default is %v, where model's is %s", path, node["default"], model.KindLife)
				}
			case strings.Contains(description, "project_statuses table"):
				want = model.ProjectStatuses
			}
			if want != nil {
				sites++
				listed, _ := node["enum"].([]any)
				if !slices.Equal(stringsOf(listed), want) {
					t.Errorf("%s: %q lists %v, where model holds %v", path, description, listed, want)
				}
			}
			for _, child := range node {
				walk(path, child)
			}
		}
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		walk(path, document)
	}
	if sites == 0 {
		t.Fatal("no schema names the project_kinds or project_statuses table")
	}
}

func stringsOf(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		out = append(out, text)
	}
	return out
}

// Every shape the API answers with is a real answer checked against its file, so
// the schemas cannot drift from the code without this failing.
func TestEveryAnswerMatchesItsSchema(t *testing.T) {
	a := newAPI(t)
	const v1 = "/api/v1"
	project := a.created(t, v1+"/projects", map[string]any{"name": "first"})
	other := a.created(t, v1+"/projects", map[string]any{"name": "second", "status": "dropped", "status_reason": "why"})
	item := a.created(t, v1+"/project-items", map[string]any{"title": "an item", "repo": "a-repo", "project_ids": []string{project}})
	blocker := a.created(t, v1+"/project-items", map[string]any{"title": "a blocker", "project_ids": []string{project}})
	task := a.created(t, v1+"/project-items/"+item+"/tasks", map[string]any{"title": "a task"})
	itemPath := v1 + "/project-items/" + item

	for _, c := range []struct {
		method, path string
		body         any
		status       int
		schema       string
		isList       bool
	}{
		{http.MethodGet, "/health", nil, 200, "probe", false},
		{http.MethodGet, "/ready", nil, 200, "probe", false},
		{http.MethodGet, v1 + "/projects", nil, 200, "project-with-item-count", true},
		{http.MethodGet, v1 + "/projects/" + project, nil, 200, "project-with-item-count", false},
		{http.MethodPatch, v1 + "/projects/" + other, map[string]any{"position": 3}, 200, "project", false},
		{http.MethodGet, v1 + "/projects/" + project + "/items", nil, 200, "project-item-in-project", true},
		{http.MethodPost, itemPath + "/dependencies", map[string]any{"depends_on_id": blocker}, 201, "project-item-detail", false},
		{http.MethodGet, itemPath, nil, 200, "project-item-detail", false},
		{http.MethodGet, v1 + "/project-items", nil, 200, "project-item", true},
		{http.MethodGet, v1 + "/project-items/blocked", nil, 200, "project-item", true},
		{http.MethodGet, v1 + "/project-items/search?q=item", nil, 200, "project-item", true},
		{http.MethodGet, itemPath + "/blockers", nil, 200, "project-item", true},
		{http.MethodPatch, itemPath, map[string]any{"notes": "a note"}, 200, "project-item", false},
		{http.MethodPatch, itemPath + "/reorder", map[string]any{"project_id": project, "position": 1}, 200, "project-item-in-project", false},
		{http.MethodGet, itemPath + "/projects", nil, 200, "project", true},
		{http.MethodPost, itemPath + "/projects", map[string]any{"project_id": other}, 201, "project", false},
		{http.MethodGet, itemPath + "/tasks", nil, 200, "project-item-task", true},
		{http.MethodPatch, itemPath + "/tasks/" + task, map[string]any{"completed": true}, 200, "project-item-task", false},
		{http.MethodGet, v1 + "/undo", nil, 200, "undo-state", false},
		{http.MethodPost, v1 + "/undo", nil, 200, "undo-result", false},

		{http.MethodGet, v1 + "/projects/absent", nil, 404, "error", false},
		{http.MethodPost, v1 + "/projects", map[string]any{"position": "first"}, 422, "error", false},
		{http.MethodPatch, itemPath, map[string]any{"title": nil}, 422, "error", false},
		{http.MethodGet, v1 + "/project-items?limit=-1", nil, 422, "error", false},
		{http.MethodGet, v1 + "/nothing-here", nil, 404, "error", false},
		{http.MethodPut, v1 + "/projects", nil, 405, "error", false},
		{http.MethodGet, "/api/projects", nil, 410, "error", false},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			status, answer := a.send(t, a.person, c.method, c.path, c.body)
			if status != c.status {
				t.Fatalf("answered %d, want %d: %s", status, c.status, answer)
			}
			var document any
			if err := json.Unmarshal(answer, &document); err != nil {
				t.Fatalf("reading %s: %v", answer, err)
			}
			rows := []any{document}
			if c.isList {
				rows = document.([]any)
				if len(rows) == 0 {
					t.Fatalf("an empty list checks nothing against %s", c.schema)
				}
			}
			compiled := schema(t, c.schema)
			for _, row := range rows {
				if err := compiled.Validate(row); err != nil {
					t.Errorf("%v\nanswer: %s", err, answer)
				}
			}
		})
	}
}

func TestATokenReachesWhatItsClientMay(t *testing.T) {
	a := newAPI(t)
	project := a.created(t, "/api/v1/projects", map[string]any{"name": "first"})
	item := a.created(t, "/api/v1/project-items", map[string]any{"title": "an item", "project_ids": []string{project}})

	reachable := []string{
		"/api/v1/project-items",
		"/api/v1/project-items/blocked",
		"/api/v1/project-items/search?q=item",
		"/api/v1/project-items/" + item,
		"/api/v1/project-items/" + item + "/blockers",
		"/api/v1/project-items/" + item + "/tasks",
		"/api/v1/projects/" + project + "/items",
	}
	for _, path := range reachable {
		if status, answer := a.send(t, a.service, http.MethodGet, path, nil); status != http.StatusOK {
			t.Errorf("service GET %s answered %d: %s", path, status, answer)
		}
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodGet, "/api/v1/projects/" + project},
		{http.MethodPatch, "/api/v1/project-items/" + item},
		{http.MethodGet, "/api/v1/nothing-here"},
	} {
		if status, answer := a.send(t, a.service, c.method, c.path, nil); status != http.StatusForbidden {
			t.Errorf("service %s %s answered %d: %s", c.method, c.path, status, answer)
		}
	}

	if status, _ := a.send(t, a.person, http.MethodGet, "/api/v1/projects", nil); status != http.StatusOK {
		t.Errorf("a person's token answered %d", status)
	}
	if status, _ := a.send(t, "", http.MethodGet, "/api/v1/projects", nil); status != http.StatusUnauthorized {
		t.Errorf("no token answered %d", status)
	}
	if status, _ := a.send(t, "", http.MethodGet, "/.well-known/oauth-protected-resource", nil); status != http.StatusOK {
		t.Errorf("the login metadata with no token answered %d", status)
	}
}

// A project name may hold a slash, and every client escapes what it puts in a path.
func TestAProjectNameHoldingASlashIsReachedEscaped(t *testing.T) {
	a := newAPI(t)
	const name = "web/api rewrite"
	project := a.created(t, "/api/v1/projects", map[string]any{"name": name})
	a.created(t, "/api/v1/project-items", map[string]any{"title": "an item", "project_ids": []string{name}})

	escaped := url.PathEscape(name)
	status, answer := a.send(t, a.person, http.MethodGet, "/api/v1/projects/"+escaped, nil)
	if status != http.StatusOK || !bytes.Contains(answer, []byte(project)) {
		t.Errorf("reading it answered %d: %s", status, answer)
	}
	status, answer = a.send(t, a.person, http.MethodGet, "/api/v1/projects/"+escaped+"/items", nil)
	if status != http.StatusOK || !bytes.Contains(answer, []byte("an item")) {
		t.Errorf("reading its items answered %d: %s", status, answer)
	}
}

// Null clears a field and leaving it out keeps it, and a field that cannot hold
// null refuses it by name, so a partial update never guesses.
func TestAPartialUpdateTellsNullFromLeftOut(t *testing.T) {
	a := newAPI(t)
	project := a.created(t, "/api/v1/projects", map[string]any{"name": "first"})
	item := a.created(t, "/api/v1/project-items", map[string]any{"title": "an item", "notes": "a note", "repo": "a-repo", "project_ids": []string{project}})
	path := "/api/v1/project-items/" + item

	patched := func(body any) (notes, repo *string) {
		t.Helper()
		status, answer := a.send(t, a.person, http.MethodPatch, path, body)
		if status != http.StatusOK {
			t.Fatalf("PATCH %v answered %d: %s", body, status, answer)
		}
		var row struct {
			Notes *string `json:"notes"`
			Repo  *string `json:"repo"`
		}
		if err := json.Unmarshal(answer, &row); err != nil {
			t.Fatalf("reading %s: %v", answer, err)
		}
		return row.Notes, row.Repo
	}
	if notes, repo := patched(map[string]any{"title": "renamed"}); notes == nil || repo == nil {
		t.Errorf("leaving the fields out changed them: notes %v, repo %v", notes, repo)
	}
	if notes, repo := patched(map[string]any{"notes": nil}); notes != nil || repo == nil {
		t.Errorf("null on notes alone: notes %v, repo %v", notes, repo)
	}

	status, answer := a.send(t, a.person, http.MethodPatch, path, map[string]any{"title": nil})
	var refused struct {
		Errors []struct {
			Loc []any `json:"loc"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(answer, &refused); err != nil {
		t.Fatalf("reading %s: %v", answer, err)
	}
	if status != http.StatusUnprocessableEntity || len(refused.Errors) != 1 {
		t.Fatalf("null on a title answered %d: %s", status, answer)
	}
	if refused.Errors[0].Loc[1] != "title" {
		t.Errorf("the refusal marks %v", refused.Errors[0].Loc)
	}
}

// One person's machines share the API, and an undo on one never reverts what
// another wrote. Where the other wrote to the same row since, the undo is refused
// with that write named, and both edits stand.
func TestAnUndoRevertsOnlyItsOwnClientsWrite(t *testing.T) {
	a := newAPI(t)
	project := a.created(t, "/api/v1/projects", map[string]any{"name": "first"})
	item := a.created(t, "/api/v1/project-items", map[string]any{"title": "before", "project_ids": []string{project}})
	path := "/api/v1/project-items/" + item
	if status, answer := a.send(t, a.person, http.MethodPatch, path, map[string]any{"title": "desk's edit"}); status != http.StatusOK {
		t.Fatalf("the desk's edit answered %d: %s", status, answer)
	}
	status, answer := a.send(t, a.laptop, http.MethodPost, "/api/v1/project-items", map[string]any{"title": "laptop's item", "project_ids": []string{project}})
	if status != http.StatusCreated {
		t.Fatalf("the laptop's create answered %d: %s", status, answer)
	}
	var laptops struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answer, &laptops); err != nil {
		t.Fatal(err)
	}

	if status, answer := a.send(t, a.person, http.MethodPost, "/api/v1/undo", nil); status != http.StatusOK {
		t.Fatalf("the desk's undo answered %d: %s", status, answer)
	}
	if status, answer := a.send(t, a.person, http.MethodGet, "/api/v1/project-items/"+laptops.ID, nil); status != http.StatusOK {
		t.Errorf("the desk's undo removed the laptop's item, which answers %d: %s", status, answer)
	}
	if _, answer := a.send(t, a.person, http.MethodGet, path, nil); !bytes.Contains(answer, []byte(`"title":"before"`)) {
		t.Errorf("the desk's undo left its own edit standing: %s", answer)
	}
}

// A key no route reads is refused by name before anything is written, so a client
// released ahead of this server learns its field was not applied.
func TestAKeyTheRouteDoesNotReadIsRefusedAndNothingIsWritten(t *testing.T) {
	a := newAPI(t)
	project := a.created(t, "/api/v1/projects", map[string]any{"name": "first"})
	other := a.created(t, "/api/v1/projects", map[string]any{"name": "second"})
	item := a.created(t, "/api/v1/project-items", map[string]any{"title": "an item", "project_ids": []string{project}})
	blocker := a.created(t, "/api/v1/project-items", map[string]any{"title": "a blocker", "project_ids": []string{project}})
	task := a.created(t, "/api/v1/project-items/"+item+"/tasks", map[string]any{"title": "a task"})
	itemPath := "/api/v1/project-items/" + item

	everything := func() string {
		t.Helper()
		var all []byte
		for _, path := range []string{"/api/v1/projects?status=all", "/api/v1/project-items?status=all", itemPath + "/tasks"} {
			_, answer := a.send(t, a.person, http.MethodGet, path, nil)
			all = append(all, answer...)
		}
		return string(all)
	}
	before := everything()
	errorSchema := schema(t, "error")

	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/api/v1/projects", map[string]any{"name": "third"}},
		{http.MethodPatch, "/api/v1/projects/" + project, map[string]any{"name": "renamed"}},
		{http.MethodPost, "/api/v1/project-items", map[string]any{"title": "another", "project_ids": []string{project}}},
		{http.MethodPatch, itemPath, map[string]any{"title": "renamed"}},
		{http.MethodPatch, itemPath + "/reorder", map[string]any{"project_id": project, "position": 1}},
		{http.MethodPost, itemPath + "/projects", map[string]any{"project_id": other}},
		{http.MethodPost, itemPath + "/dependencies", map[string]any{"depends_on_id": blocker}},
		{http.MethodPost, itemPath + "/tasks", map[string]any{"title": "another"}},
		{http.MethodPatch, itemPath + "/tasks/" + task, map[string]any{"title": "renamed"}},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			c.body["sent_ahead"] = "a field this server predates"
			status, answer := a.send(t, a.person, c.method, c.path, c.body)
			var refused struct {
				Code  string `json:"code"`
				Field string `json:"field"`
			}
			if err := json.Unmarshal(answer, &refused); err != nil {
				t.Fatalf("reading %s: %v", answer, err)
			}
			if status != http.StatusUnprocessableEntity || refused.Code != "unknown_field" || refused.Field != "sent_ahead" {
				t.Errorf("answered %d: %s", status, answer)
			}
			var document any
			_ = json.Unmarshal(answer, &document)
			if err := errorSchema.Validate(document); err != nil {
				t.Errorf("%v\nanswer: %s", err, answer)
			}
		})
	}
	if after := everything(); after != before {
		t.Errorf("a refused write changed the data\nbefore: %s\nafter:  %s", before, after)
	}
}
