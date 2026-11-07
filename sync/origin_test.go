package sync_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
	"github.com/datapointchris/projects/sync"
)

// apiWith serves a fixed set of projects and items, which is all a pull reads.
func apiWith(t *testing.T, projects []model.Project, items []model.ProjectItemDetail) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects":
			_ = json.NewEncoder(w).Encode(projects)
		case "/api/v1/project-items":
			_ = json.NewEncoder(w).Encode(items)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func seededAPI(t *testing.T, projectCount, itemCount int) *httptest.Server {
	t.Helper()
	projects := make([]model.Project, 0, projectCount)
	for i := range projectCount {
		projects = append(projects, model.Project{
			ID: "proj-" + itoa(i), Name: "Project " + itoa(i), CreatedAt: time.Now(),
		})
	}
	items := make([]model.ProjectItemDetail, 0, itemCount)
	for i := range itemCount {
		items = append(items, model.ProjectItemDetail{
			ProjectItem: model.ProjectItem{
				ID: "item-" + itoa(i), Title: "Item " + itoa(i),
				CreatedAt: time.Now(), UpdatedAt: time.Now(),
			},
			Projects:      []model.Project{{ID: "proj-0"}},
			DependencyIDs: []string{},
			Tasks:         []model.ProjectItemTask{},
		})
	}
	return apiWith(t, projects, items)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// A fresh install must not need a ceremony: its first pull records which API it
// belongs to and gets on with it.
func TestFirstPullAdoptsTheAPI(t *testing.T) {
	database := openTestDB(t)
	ts := seededAPI(t, 2, 3)

	engine := sync.New(database, ts.URL, "")
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if got := engine.Origin(t.Context()); got != ts.URL {
		t.Errorf("origin = %q, want %q", got, ts.URL)
	}
}

// The incident this exists to prevent: a database that belongs to one API is
// pointed at another, and the pull faithfully deletes everything the second API
// has never heard of. A production database lost 42 projects and 286 items this
// way, and the command printed "Synced."
func TestPullRefusesADifferentAPI(t *testing.T) {
	database := openTestDB(t)
	real := seededAPI(t, 20, 40)

	if err := sync.New(database, real.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	other := seededAPI(t, 1, 2)
	err := sync.New(database, other.URL, "").Pull(t.Context())

	var mismatch *sync.OriginMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("pull against a different API returned %v, want an OriginMismatch", err)
	}
	if mismatch.Recorded != real.URL || mismatch.Configured != other.URL {
		t.Errorf("mismatch names %s → %s, want %s → %s",
			mismatch.Recorded, mismatch.Configured, real.URL, other.URL)
	}

	local := service.New(sqlite.New(database))
	projects, err := local.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("ListProjectsByStatus: %v", err)
	}
	if len(projects) != 20 {
		t.Errorf("the refused pull still took data: %d projects left of 20", len(projects))
	}
}

// The refusal has to survive the trailing slash, or it fires on a difference
// that addresses the same server.
func TestOriginIgnoresATrailingSlash(t *testing.T) {
	database := openTestDB(t)
	ts := seededAPI(t, 2, 3)

	if err := sync.New(database, ts.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if err := sync.New(database, ts.URL+"/", "").Pull(t.Context()); err != nil {
		t.Errorf("a trailing slash was treated as a different API: %v", err)
	}
}

// --adopt is the deliberate override, for an API that genuinely moved.
func TestAdoptRebindsTheDatabase(t *testing.T) {
	database := openTestDB(t)
	first := seededAPI(t, 2, 3)
	if err := sync.New(database, first.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	moved := seededAPI(t, 2, 3)
	engine := sync.New(database, moved.URL, "")
	if err := engine.PullWith(t.Context(), sync.PullOptions{Adopt: true, AllowLargeSweep: true}); err != nil {
		t.Fatalf("adopting pull: %v", err)
	}
	if got := engine.Origin(t.Context()); got != moved.URL {
		t.Errorf("origin = %q, want %q", got, moved.URL)
	}
}

// A database carried over from before the binding existed has reconciled with
// something, and nothing recorded what. Guessing from whichever environment
// happens to be loaded is the mistake itself.
func TestPullRefusesToGuessForADatabaseThatSyncedBeforeTheBinding(t *testing.T) {
	database := openTestDB(t)
	ts := seededAPI(t, 20, 40)
	if err := sync.New(database, ts.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if _, err := database.Exec("DELETE FROM sync_origin"); err != nil {
		t.Fatalf("clearing the origin: %v", err)
	}

	err := sync.New(database, ts.URL, "").Pull(t.Context())

	var unclaimed *sync.OriginUnclaimed
	if !errors.As(err, &unclaimed) {
		t.Fatalf("pull returned %v, want an OriginUnclaimed", err)
	}
	if unclaimed.Projects != 20 || unclaimed.Items != 40 {
		t.Errorf("the refusal reports %d projects and %d items, want 20 and 40",
			unclaimed.Projects, unclaimed.Items)
	}

	if err := sync.New(database, ts.URL, "").PullWith(t.Context(), sync.PullOptions{Adopt: true}); err != nil {
		t.Errorf("--adopt did not resolve it: %v", err)
	}
}

// A truncated response and a real mass deletion are the same event from here,
// and only one of them should be obeyed without being asked.
func TestPullRefusesToDeleteMostOfTheDatabase(t *testing.T) {
	database := openTestDB(t)
	full := seededAPI(t, 20, 40)
	if err := sync.New(database, full.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	// Same API, now answering with almost nothing — a bad deploy, an auth
	// failure that still returns 200, a paginated endpoint read as complete.
	truncated := apiWith(t, []model.Project{{ID: "proj-0", Name: "Project 0", CreatedAt: time.Now()}}, nil)
	engine := sync.New(database, truncated.URL, "")
	if err := engine.PullWith(t.Context(), sync.PullOptions{Adopt: true}); err == nil {
		t.Fatal("a pull deleting 19 of 20 projects was allowed")
	} else {
		var refused *sync.SweepRefused
		if !errors.As(err, &refused) {
			t.Fatalf("got %v, want a SweepRefused", err)
		}
	}

	local := service.New(sqlite.New(database))
	projects, err := local.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("ListProjectsByStatus: %v", err)
	}
	if len(projects) != 20 {
		t.Errorf("the refused pull still deleted: %d projects left of 20", len(projects))
	}

	if err := engine.PullWith(t.Context(), sync.PullOptions{Adopt: true, AllowLargeSweep: true}); err != nil {
		t.Fatalf("--force did not allow it: %v", err)
	}
	projects, err = local.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("ListProjectsByStatus: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("--force did not apply the sweep: %d projects left", len(projects))
	}
}

// The guard must not fire on ordinary tidying, or it trains you to pass --force
// by reflex and stops guarding anything.
func TestPullAllowsAnOrdinaryDeletion(t *testing.T) {
	database := openTestDB(t)
	full := seededAPI(t, 20, 40)
	if err := sync.New(database, full.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	fewer := seededAPI(t, 18, 32)
	engine := sync.New(database, fewer.URL, "")
	if err := engine.PullWith(t.Context(), sync.PullOptions{Adopt: true}); err != nil {
		t.Fatalf("deleting 2 of 20 projects and 8 of 40 items was refused: %v", err)
	}
}

// A small database loses "most of itself" on any ordinary day, so the guard has
// a floor — otherwise it fires constantly and gets forced past on reflex.
func TestSweepGuardHasAFloorForSmallDatabases(t *testing.T) {
	database := openTestDB(t)
	full := seededAPI(t, 4, 6)
	if err := sync.New(database, full.URL, "").Pull(t.Context()); err != nil {
		t.Fatalf("first pull: %v", err)
	}

	empty := apiWith(t, nil, nil)
	engine := sync.New(database, empty.URL, "")
	if err := engine.PullWith(t.Context(), sync.PullOptions{Adopt: true}); err != nil {
		t.Errorf("a 4-project database was guarded: %v", err)
	}
}
