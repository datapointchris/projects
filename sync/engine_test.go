package sync_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	goSync "sync"
	"testing"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
	"github.com/datapointchris/projects/sync"
)

func setupSync(t *testing.T, handler http.Handler) (*sync.SyncBackend, *sync.Engine) {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	local := service.New(sqlite.New(database))
	engine := sync.New(database, ts.URL, "")
	engine.Start()
	t.Cleanup(engine.Stop)

	sb := sync.NewSyncBackend(local, engine)
	return sb, engine
}

func TestSyncBackend_ReadsDelegateToLocal(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	sb, _ := setupSync(t, handler)

	// Create a project
	_, err := sb.CreateProject(model.CreateProject{Name: "LocalRead"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Read should come from local DB immediately
	projects, err := sb.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("ListProjectsByStatus: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].Name != "LocalRead" {
		t.Errorf("got name %q, want LocalRead", projects[0].Name)
	}
}

func TestSyncBackend_PushRetryOnNetworkError(t *testing.T) {
	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts <= 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	sb, engine := setupSync(t, handler)

	_, err := sb.CreateProject(model.CreateProject{Name: "Retry"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// First push will fail (500), wait for backoff + retry
	time.Sleep(4 * time.Second)

	// Manually trigger a retry
	engine.Notify()
	time.Sleep(500 * time.Millisecond)

	status := engine.Status()
	if status.PendingCount != 0 {
		t.Errorf("expected 0 pending after retry, got %d", status.PendingCount)
	}
}

func TestSyncBackend_PushDropsOn409(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"duplicate"}`))
	})
	sb, engine := setupSync(t, handler)

	_, err := sb.CreateProject(model.CreateProject{Name: "Conflict"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// 409 should be treated as "drop" — pending count goes to 0
	time.Sleep(200 * time.Millisecond)

	status := engine.Status()
	if status.PendingCount != 0 {
		t.Errorf("expected 0 pending (409 should drop), got %d", status.PendingCount)
	}
}

// callRecorder captures what a test's mock server received. The push loop
// runs on its own goroutine, so every read and write of the log needs the
// lock — asserting on an unguarded slice is a data race.
type callRecorder struct {
	mu    goSync.Mutex
	calls []string
}

func (r *callRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *callRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestSyncBackend_ItemLifecycle(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	p, _ := sb.CreateProject(model.CreateProject{Name: "Items"})
	item, err := sb.CreateItem(model.CreateProjectItem{
		Title:      "Test",
		ProjectIDs: []string{p.ID},
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	done := true
	_, err = sb.UpdateItem(item.ID, model.UpdateProjectItem{Completed: &done})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	if err := sb.DeleteItem(item.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// Wait for push loop to drain
	time.Sleep(500 * time.Millisecond)

	// Verify the HTTP calls were made in order
	if recorder.count() < 4 {
		t.Fatalf("expected at least 4 HTTP calls, got %d: %v", recorder.count(), recorder.snapshot())
	}
}

func TestSyncBackend_TaskOperations(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	p, _ := sb.CreateProject(model.CreateProject{Name: "Tasks"})
	item, _ := sb.CreateItem(model.CreateProjectItem{
		Title:      "WithTasks",
		ProjectIDs: []string{p.ID},
	})

	task, err := sb.CreateTask(item.ID, model.CreateProjectItemTask{Title: "Sub"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	done := true
	if _, err := sb.UpdateTask(item.ID, task.ID, model.UpdateProjectItemTask{Completed: &done}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if err := sb.DeleteTask(item.ID, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if recorder.count() < 5 {
		t.Fatalf("expected at least 5 HTTP calls, got %d: %v", recorder.count(), recorder.snapshot())
	}
}

func TestSyncBackend_UndoAfterPushDeletesRemotely(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	p, _ := sb.CreateProject(model.CreateProject{Name: "Undo"})
	item, _ := sb.CreateItem(model.CreateProjectItem{
		Title:      "Undoable",
		ProjectIDs: []string{p.ID},
	})

	// Let the create reach the server, so the server holds a row the undo removes.
	time.Sleep(300 * time.Millisecond)

	if _, err := sb.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	want := "DELETE /api/v1/project-items/" + item.ID
	calls := recorder.snapshot()
	for _, call := range calls {
		if call == want {
			return
		}
	}
	t.Errorf("expected %q after undoing an already-pushed create, got %v", want, calls)
}

func TestSyncBackend_UndoBeforePushDropsQueuedCreate(t *testing.T) {
	// Every push fails, so operations stay queued and nothing reaches the server.
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	sb, engine := setupSync(t, handler)

	p, _ := sb.CreateProject(model.CreateProject{Name: "Undo"})
	item, _ := sb.CreateItem(model.CreateProjectItem{
		Title:      "Undoable",
		ProjectIDs: []string{p.ID},
	})

	if _, err := sb.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	// The queued create described a row that no longer exists. Nothing should be
	// left for this item — not the create, and not a delete for a row the server
	// never received.
	remaining, err := engine.DropOpsForEntity(item.ID)
	if err != nil {
		t.Fatalf("checking queue: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected no queued operations for the undone item, found %d", remaining)
	}
}

func TestPull_Reconciles(t *testing.T) {
	// Mock API server that returns test data
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]model.Project{
				{ID: "proj-1", Name: "Pulled", Position: 0, CreatedAt: time.Now()},
			})
		case "/api/v1/project-items":
			_ = json.NewEncoder(w).Encode([]model.ProjectItem{
				{ID: "01900000-0000-7000-8000-00000000a001", Title: "Pulled Item", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			})
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a001":
			_ = json.NewEncoder(w).Encode(model.ProjectItemDetail{
				ProjectItem: model.ProjectItem{ID: "01900000-0000-7000-8000-00000000a001", Title: "Pulled Item", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Projects:    []model.Project{{ID: "proj-1", Name: "Pulled"}},
			})
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a001/tasks":
			_ = json.NewEncoder(w).Encode([]model.ProjectItemTask{
				{ID: "task-1", ItemID: "01900000-0000-7000-8000-00000000a001", Title: "Pulled Task", CreatedAt: time.Now()},
			})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}
	})

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer func() { _ = database.Close() }()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	engine := sync.New(database, ts.URL, "")

	// Pull should reconcile server data into local DB
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Verify data was pulled into local DB
	local := service.New(sqlite.New(database))

	projects, err := local.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("ListProjectsByStatus: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "Pulled" {
		t.Errorf("expected 1 project 'Pulled', got %v", projects)
	}

	item, err := local.GetItem("01900000-0000-7000-8000-00000000a001")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Title != "Pulled Item" {
		t.Errorf("got title %q, want 'Pulled Item'", item.Title)
	}

	tasks, err := local.ListTasks("01900000-0000-7000-8000-00000000a001")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Pulled Task" {
		t.Errorf("expected 1 task 'Pulled Task', got %v", tasks)
	}
}

// Undoing an already-pushed delete has to recreate the row on the server, not
// update one the server no longer has — and recreate its memberships, tasks,
// and dependencies with it, because a pull rebuilds those from server state
// and would otherwise erase the restore.
func TestSyncBackend_UndoOfPushedDeleteRecreatesRemotely(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	p, _ := sb.CreateProject(model.CreateProject{Name: "Undo"})
	item, _ := sb.CreateItem(model.CreateProjectItem{
		Title:      "Doomed",
		ProjectIDs: []string{p.ID},
	})
	if _, err := sb.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := sb.DeleteItem(item.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// Let the delete reach the server before undoing it, then look only at
	// what the undo itself pushed — the original create hit the same paths.
	time.Sleep(400 * time.Millisecond)
	beforeUndo := recorder.count()

	if _, err := sb.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	calls := recorder.snapshot()[beforeUndo:]

	var sawItemCreate, sawTaskCreate bool
	for _, call := range calls {
		switch call {
		case "POST /api/v1/project-items":
			sawItemCreate = true
		case "POST /api/v1/project-items/" + item.ID + "/tasks":
			sawTaskCreate = true
		}
	}
	if !sawItemCreate {
		t.Errorf("expected the restored item recreated on the server, got %v", calls)
	}
	if !sawTaskCreate {
		t.Errorf("expected the restored sub-task recreated on the server, got %v", calls)
	}
}

// Membership, dependency, and reorder undos all record the item as their
// entity ID. Reconciling them by dropping queued ops for that ID would take
// unrelated edits with it, and falling through to the item branch would push
// a delete for an item that is still there.
func TestSyncBackend_UndoOfMembershipDoesNotDeleteTheItem(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	work, _ := sb.CreateProject(model.CreateProject{Name: "work"})
	personal, _ := sb.CreateProject(model.CreateProject{Name: "personal"})
	item, _ := sb.CreateItem(model.CreateProjectItem{
		Title:      "shared",
		ProjectIDs: []string{work.ID},
	})
	if err := sb.AddToProject(item.ID, personal.ID); err != nil {
		t.Fatalf("AddToProject: %v", err)
	}

	time.Sleep(400 * time.Millisecond)
	beforeUndo := recorder.count()

	if _, err := sb.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	calls := recorder.snapshot()[beforeUndo:]
	wantRemove := "DELETE /api/v1/project-items/" + item.ID + "/projects/" + personal.ID
	itemDelete := "DELETE /api/v1/project-items/" + item.ID

	var sawRemove bool
	for _, call := range calls {
		if call == itemDelete {
			t.Fatalf("undoing a membership add deleted the item on the server: %v", calls)
		}
		if call == wantRemove {
			sawRemove = true
		}
	}
	if !sawRemove {
		t.Errorf("expected %q after undoing the membership add, got %v", wantRemove, calls)
	}
}

func TestSyncBackend_UndoOfTaskCompletionPushesTheReversal(t *testing.T) {
	recorder := &callRecorder{}
	sb, _ := setupSync(t, recorder.handler())

	p, _ := sb.CreateProject(model.CreateProject{Name: "work"})
	item, _ := sb.CreateItem(model.CreateProjectItem{Title: "item", ProjectIDs: []string{p.ID}})
	task, err := sb.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	done := true
	if _, err := sb.UpdateTask(item.ID, task.ID, model.UpdateProjectItemTask{Completed: &done}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	time.Sleep(400 * time.Millisecond)
	beforeUndo := recorder.count()

	if _, err := sb.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	calls := recorder.snapshot()[beforeUndo:]
	want := "PATCH /api/v1/project-items/" + item.ID + "/tasks/" + task.ID
	for _, call := range calls {
		if call == want {
			return
		}
	}
	t.Errorf("expected %q after undoing the completion, got %v", want, calls)
}

// logging wraps a handler so a test can count requests without reimplementing
// the routing.
func logging(recorder *callRecorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.calls = append(recorder.calls, r.Method+" "+r.URL.Path)
		recorder.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// A pull takes seconds — 2+2N requests — and the user keeps working through it.
// Anything created in that window postdates the server response, so the
// reconciliation must not read it as "deleted upstream" and erase it, nor drop
// the queued operation that is the only record of it.
func TestPull_KeepsWorkDoneWhileItWasRunning(t *testing.T) {
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	local := service.New(sqlite.New(database))

	// The mid-pull create needs a project to attach to, and the server has to
	// report that same project or the pull deletes it and cascades the item away.
	project, err := local.CreateProject(model.CreateProject{Name: "Server"})
	if err != nil {
		t.Fatalf("seeding project: %v", err)
	}

	var sb *sync.SyncBackend
	var once goSync.Once
	raced := make(chan string, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every write fails, so the op queued below is guaranteed to still be
		// pending when the assertion runs.
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]model.Project{
				{ID: project.ID, Name: "Server", Position: 0, CreatedAt: time.Now()},
			})
		case "/api/v1/project-items":
			_ = json.NewEncoder(w).Encode([]model.ProjectItem{
				{ID: "01900000-0000-7000-8000-00000000a001", Title: "Server Item", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			})
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a001":
			// Mid-pull: the user adds an item the server response cannot know about.
			once.Do(func() {
				created, err := sb.CreateItem(model.CreateProjectItem{
					Title:      "Typed During Pull",
					ProjectIDs: []string{project.ID},
				})
				if err != nil {
					t.Errorf("creating item mid-pull: %v", err)
					return
				}
				raced <- created.ID
			})
			_ = json.NewEncoder(w).Encode(model.ProjectItemDetail{
				ProjectItem: model.ProjectItem{ID: "01900000-0000-7000-8000-00000000a001", Title: "Server Item", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Projects:    []model.Project{{ID: project.ID, Name: "Server"}},
			})
		default:
			_ = json.NewEncoder(w).Encode([]model.ProjectItemTask{})
		}
	})

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	engine := sync.New(database, ts.URL, "")
	t.Cleanup(engine.Stop)
	sb = sync.NewSyncBackend(local, engine)

	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	racedID := <-raced
	if _, err := local.GetItem(racedID); err != nil {
		t.Fatalf("item created during the pull was erased by it: %v", err)
	}
	if got := engine.Status().PendingCount; got == 0 {
		t.Error("the queued create was dropped, so nothing will ever push it")
	}
}

// Notify only fires on a local mutation. Without a retry ticker a push that
// failed while the API was down stayed queued until the user happened to edit
// something else, which is indistinguishable from sync being broken.
func TestPush_RetriesWithoutAFurtherMutation(t *testing.T) {
	var mu goSync.Mutex
	failing := true
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		down := failing
		mu.Unlock()
		if down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	engine := sync.New(database, ts.URL, "", sync.WithPushRetryInterval(100*time.Millisecond))
	engine.Start()
	t.Cleanup(engine.Stop)
	sb := sync.NewSyncBackend(service.New(sqlite.New(database)), engine)

	if _, err := sb.CreateProject(model.CreateProject{Name: "Queued"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// The first push fails and backs off; the op must still be waiting.
	time.Sleep(500 * time.Millisecond)
	if engine.Status().PendingCount == 0 {
		t.Fatal("op vanished while the API was failing")
	}

	mu.Lock()
	failing = false
	mu.Unlock()

	// No Notify, no further mutation — only the ticker can clear this.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if engine.Status().PendingCount == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("queue never healed on its own after the API recovered")
}

// pullFixture serves a two-item dataset. When embedded is true the list carries
// dependency_ids and tasks the way the current API does; when false it omits
// them the way a server predating the embed does, which is what forces the
// per-item fallback.
func pullFixture(embedded bool) http.HandlerFunc {
	now := time.Now()
	base := func(id, title string) map[string]any {
		return map[string]any{
			"id": id, "title": title, "notes": nil, "repo": nil,
			"completed": false, "archived": false,
			"created_at": now.Format(time.RFC3339Nano),
			"updated_at": now.Format(time.RFC3339Nano),
			"projects":   []map[string]any{{"id": "proj-1", "name": "Remote", "position": 0, "created_at": now.Format(time.RFC3339Nano)}},
		}
	}
	task := map[string]any{
		"id": "task-1", "item_id": "01900000-0000-7000-8000-00000000a002", "title": "Embedded Task",
		"completed": false, "position": 0, "created_at": now.Format(time.RFC3339Nano),
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]model.Project{{ID: "proj-1", Name: "Remote", Position: 0, CreatedAt: now}})
		case "/api/v1/project-items":
			blocker := base("01900000-0000-7000-8000-00000000a001", "Blocker")
			blocked := base("01900000-0000-7000-8000-00000000a002", "Blocked")
			if embedded {
				blocker["dependency_ids"] = []string{}
				blocker["tasks"] = []map[string]any{}
				blocked["dependency_ids"] = []string{"01900000-0000-7000-8000-00000000a001"}
				blocked["tasks"] = []map[string]any{task}
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{blocker, blocked})
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a001":
			_ = json.NewEncoder(w).Encode(base("01900000-0000-7000-8000-00000000a001", "Blocker"))
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a002":
			detail := base("01900000-0000-7000-8000-00000000a002", "Blocked")
			detail["dependency_ids"] = []string{"01900000-0000-7000-8000-00000000a001"}
			_ = json.NewEncoder(w).Encode(detail)
		case "/api/v1/project-items/01900000-0000-7000-8000-00000000a002/tasks":
			_ = json.NewEncoder(w).Encode([]map[string]any{task})
		default:
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}
	}
}

func pulledInto(t *testing.T, handler http.HandlerFunc) (*callRecorder, *service.Service) {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	recorder := &callRecorder{}
	ts := httptest.NewServer(logging(recorder, handler))
	t.Cleanup(ts.Close)

	engine := sync.New(database, ts.URL, "")
	t.Cleanup(engine.Stop)
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	return recorder, service.New(sqlite.New(database))
}

// The whole point of embedding: a reconcile is two requests regardless of how
// many items exist, instead of two more per item.
func TestPull_UsesEmbeddedDetailAndStopsAtTwoRequests(t *testing.T) {
	recorder, local := pulledInto(t, pullFixture(true))

	if got := recorder.snapshot(); len(got) != 2 {
		t.Errorf("expected 2 requests, got %d: %v", len(got), got)
	}

	tasks, err := local.ListTasks("01900000-0000-7000-8000-00000000a002")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Embedded Task" {
		t.Errorf("embedded task did not reconcile: %v", tasks)
	}
	blockers, err := local.GetBlockers("01900000-0000-7000-8000-00000000a002")
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ID != "01900000-0000-7000-8000-00000000a001" {
		t.Errorf("embedded dependency did not reconcile: %v", blockers)
	}
}

// Deploy order between this tool and its server must not matter. Against a server
// that omits the fields, treating absent as empty would delete every task and
// dependency locally, so the pull has to fall back to the per-item endpoints.
func TestPull_FallsBackWhenTheServerOmitsEmbeddedFields(t *testing.T) {
	recorder, local := pulledInto(t, pullFixture(false))

	if got := recorder.count(); got <= 2 {
		t.Errorf("expected per-item fallback requests, got only %d", got)
	}

	tasks, err := local.ListTasks("01900000-0000-7000-8000-00000000a002")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Embedded Task" {
		t.Errorf("fallback lost the item's tasks: %v", tasks)
	}
	blockers, err := local.GetBlockers("01900000-0000-7000-8000-00000000a002")
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ID != "01900000-0000-7000-8000-00000000a001" {
		t.Errorf("fallback lost the item's dependencies: %v", blockers)
	}
}

// pendingAfterPush waits for the push loop a mutation's Notify woke, and returns
// how many ops are still queued once the queue empties or the wait runs out.
func pendingAfterPush(engine *sync.Engine) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		pending := engine.Status().PendingCount
		if pending == 0 || time.Now().After(deadline) {
			return pending
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The number is the item's handle in the TUI, and the create response is the
// first place it exists. Waiting for the next pull would leave the item unnamed
// for up to a full sync interval after it was made.
func TestPush_RecordsTheNumberTheServerAssigned(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/project-items" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number": 231}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	sb, engine := setupSync(t, handler)

	p, _ := sb.CreateProject(model.CreateProject{Name: "Numbered"})
	item, err := sb.CreateItem(model.CreateProjectItem{Title: "Earns a number", ProjectIDs: []string{p.ID}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.Number != nil {
		t.Fatalf("Number = %d before the push, want nil", *item.Number)
	}

	if pending := pendingAfterPush(engine); pending != 0 {
		t.Fatalf("pending ops = %d, want the create pushed", pending)
	}

	pushed, err := sb.GetItem(item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if pushed.Number == nil {
		t.Fatal("Number is still nil after the push — the create response was discarded")
	}
	if *pushed.Number != 231 {
		t.Errorf("Number = %d, want 231", *pushed.Number)
	}
}

// A create that reached the server is applied whether or not its response
// decodes. Failing here would re-queue it and file the item twice.
func TestPush_AnUndecodableCreateResponseIsNotAFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/project-items" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("not json"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	sb, engine := setupSync(t, handler)

	p, _ := sb.CreateProject(model.CreateProject{Name: "Garbled"})
	item, err := sb.CreateItem(model.CreateProjectItem{Title: "Still filed once", ProjectIDs: []string{p.ID}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if pending := pendingAfterPush(engine); pending != 0 {
		t.Errorf("pending ops = %d, want 0 — the create landed and must not be retried", pending)
	}
	stored, err := sb.GetItem(item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if stored.Number != nil {
		t.Errorf("Number = %d, want nil — the next pull supplies it", *stored.Number)
	}
}

// Two drains overlapping pushed the same queued op twice — a 201 followed by a
// 409, and a server-side item number burned on the insert that lost the race.
// Notify wakes the push loop on a create, and a pull drains the queue before it
// fetches, so a create landing just before a pull is drained from both sides.
func TestPush_AQueuedCreateIsPushedExactlyOnce(t *testing.T) {
	var mu goSync.Mutex
	creates := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/project-items" {
			mu.Lock()
			creates++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number": 9}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	sb, engine := setupSync(t, handler)

	p, _ := sb.CreateProject(model.CreateProject{Name: "Once"})
	if _, err := sb.CreateItem(model.CreateProjectItem{Title: "Pushed once", ProjectIDs: []string{p.ID}}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// The create already woke the push loop. The pull's own drain races one
	// that is very likely still in flight, and its fetch then fails on the
	// handler's "{}", which this test does not care about.
	_ = engine.Pull(t.Context())
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if creates != 1 {
		t.Errorf("POST /api/v1/project-items called %d times, want exactly 1", creates)
	}
}

// The server assembles /projects/ and each item's embedded Projects list
// separately, and nothing makes the two agree, so an item can name a project
// the projects endpoint omitted. The pull drops that link and carries on.
// Inserting it would fail the pull on the foreign key, and one inconsistent
// link would cost every other change in the reconcile.
func TestPull_DropsLinksToEntitiesTheServerDidNotSend(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode([]model.Project{
				{ID: "proj-1", Name: "Real", Position: 0, CreatedAt: time.Now()},
			})
		case "/api/v1/project-items":
			_ = json.NewEncoder(w).Encode([]model.ProjectItemDetail{{
				ProjectItem: model.ProjectItem{ID: "01900000-0000-7000-8000-00000000a001", Title: "Kept", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Projects: []model.Project{
					{ID: "proj-1", Name: "Real"},
					{ID: "proj-missing", Name: "Never sent by /projects/"},
				},
				DependencyIDs: []string{"item-missing"},
				Tasks: []model.ProjectItemTask{
					{ID: "task-1", ItemID: "01900000-0000-7000-8000-00000000a001", Title: "Kept task", CreatedAt: time.Now()},
				},
			}})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}
	})

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer func() { _ = database.Close() }()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	engine := sync.New(database, ts.URL, "")
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("one unlinkable membership failed the whole pull: %v", err)
	}

	local := service.New(sqlite.New(database))
	projects, err := local.GetItemProjects("01900000-0000-7000-8000-00000000a001")
	if err != nil {
		t.Fatalf("GetItemProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "proj-1" {
		t.Errorf("expected only the membership that has a project, got %v", projects)
	}

	var violations int
	if err := database.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		t.Fatalf("checking foreign keys: %v", err)
	}
	if violations != 0 {
		t.Errorf("the pull left %d dangling rows behind", violations)
	}

	tasks, err := local.ListTasks("01900000-0000-7000-8000-00000000a001")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("the good task went with the bad link: %v", tasks)
	}
}

// idx_items_number is unique and the upsert writes row by row, so two items
// swapping numbers upstream means one of them briefly needs a number the other
// still holds. Writing the numbers after every row has been cleared makes the
// pass order-independent — otherwise any renumbering upstream fails the entire
// pull and the tool never syncs again.
func TestPull_SurvivesItemsSwappingNumbers(t *testing.T) {
	numbers := map[string]int{"01900000-0000-7000-8000-00000000a00a": 1, "01900000-0000-7000-8000-00000000a00b": 2}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/project-items" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		out := make([]model.ProjectItemDetail, 0, 2)
		for _, id := range []string{"01900000-0000-7000-8000-00000000a00a", "01900000-0000-7000-8000-00000000a00b"} {
			n := numbers[id]
			out = append(out, model.ProjectItemDetail{
				ProjectItem: model.ProjectItem{
					ID: id, Number: &n, Title: id,
					CreatedAt: time.Now(), UpdatedAt: time.Now(),
				},
				DependencyIDs: []string{},
				Tasks:         []model.ProjectItemTask{},
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer func() { _ = database.Close() }()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	engine := sync.New(database, ts.URL, "")
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	numbers["01900000-0000-7000-8000-00000000a00a"], numbers["01900000-0000-7000-8000-00000000a00b"] = 2, 1
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("a swap upstream failed the pull: %v", err)
	}

	local := service.New(sqlite.New(database))
	item, err := local.GetItem("01900000-0000-7000-8000-00000000a00a")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Number == nil || *item.Number != 2 {
		t.Errorf("01900000-0000-7000-8000-00000000a00a number = %v, want 2", item.Number)
	}
}

// Both list fetches ask for every status, because the sweep in Pull deletes any
// local row the server did not return. An unqualified fetch is therefore not a
// narrower read — it is a delete. /project-items/ defaults to open items, so an
// unqualified fetch would delete every completed item here.
func TestPull_RequestsEveryStatusFromBothListEndpoints(t *testing.T) {
	var mu goSync.Mutex
	asked := map[string]string{}
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked[r.URL.Path] = r.URL.Query().Get("status")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			record(r)
			_ = json.NewEncoder(w).Encode([]model.Project{})
		case "/api/v1/project-items":
			record(r)
			_ = json.NewEncoder(w).Encode([]model.ProjectItemDetail{})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}
	})

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer func() { _ = database.Close() }()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	engine := sync.New(database, ts.URL, "")
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("pull: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/api/v1/projects", "/api/v1/project-items"} {
		if asked[path] != "all" {
			t.Errorf("%s fetched with status=%q, want all — the sweep deletes whatever the server omits", path, asked[path])
		}
	}
}
