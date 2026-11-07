package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite/generated"
)

// Pull fetches all data from the remote API and reconciles it with the local database.
// It attempts to push pending ops first (so the server has our changes), then does a
// full pull. Server state wins on conflicts.
//
// A failure is recorded on the status so the TUI's indicator reflects it. Pull runs
// unattended on a timer, and an error only a foreground caller can see would leave
// the bar reading SYNCED while nothing had reconciled for hours.
func (e *Engine) Pull(ctx context.Context) error {
	return e.PullWith(ctx, PullOptions{})
}

// PullOptions carries the deliberate overrides of the two guards that stand
// between a misconfigured reconcile and an emptied database. Neither is
// reachable from the background timers — a guard something automatic can waive
// is not a guard.
type PullOptions struct {
	// Adopt binds the database to this API before reconciling, which is the
	// only way the pairing ever changes. `projects sync --adopt`.
	Adopt bool
	// AllowLargeSweep permits a pull that would delete most of what is here.
	// `projects sync --force`.
	AllowLargeSweep bool
}

func (e *Engine) PullWith(ctx context.Context, opts PullOptions) error {
	e.beginSync()
	defer e.endSync()

	err := e.pull(ctx, opts)
	e.setStatus(func(s *SyncStatus) {
		s.Connected = err == nil
		if err != nil {
			s.LastError = err.Error()
		} else {
			s.LastError = ""
		}
	})
	return err
}

func (e *Engine) pull(ctx context.Context, opts PullOptions) error {
	if opts.Adopt {
		if err := e.AdoptOrigin(ctx); err != nil {
			return fmt.Errorf("adopting %s: %w", e.apiURL, err)
		}
	}
	// Before the push, not after: draining first would send this database's
	// queued work to an API that is not its own.
	adopt, err := e.checkOrigin(ctx)
	if err != nil {
		return err
	}
	if adopt {
		if err := e.AdoptOrigin(ctx); err != nil {
			return fmt.Errorf("recording %s as this database's origin: %w", e.apiURL, err)
		}
	}

	// Push first: try to drain pending ops so server has our changes
	e.drainPendingOps()

	// Everything queued from here on describes a local change the fetch below
	// cannot know about. Recording the high-water mark lets the reconciliation
	// clear only what it actually superseded — see the delete calls at the end.
	pushedThrough, err := e.q.MaxPendingSyncID(ctx)
	if err != nil {
		return fmt.Errorf("reading pending sync high-water mark: %w", err)
	}

	// Fetch all data from the server.
	//
	// status=all because the sweep below deletes any local project the server
	// did not return, cascading through to its memberships. Once /projects
	// defaults to active-only, an omitted-because-completed project reads as
	// deleted-upstream and the row goes. Terminal projects are filtered for
	// display locally instead. The param is ignored by an API predating it,
	// which is what lets this ship ahead of the server change.
	projects, err := fetchJSON[[]model.Project](ctx, e.client, e.base, "/projects?status=all")
	if err != nil {
		return fmt.Errorf("pulling projects: %w", err)
	}

	// status=all for the same reason as projects. /project-items defaults to
	// open items, so without it every completed item is absent from the response
	// and the sweep below deletes it.
	items, err := fetchJSON[[]model.ProjectItemDetail](ctx, e.client, e.base, "/project-items?status=all")
	if err != nil {
		return fmt.Errorf("pulling items: %w", err)
	}

	type itemExtra struct {
		detail *model.ProjectItemDetail
		tasks  []model.ProjectItemTask
	}
	extras := make(map[string]itemExtra, len(items))

	for _, item := range items {
		// The list embeds memberships, dependencies, and tasks, so the whole
		// reconcile is two requests. The fallback below costs two more per item:
		// 122 serial round trips and 6.15s at 60 items.
		if item.DependencyIDs != nil && item.Tasks != nil {
			extras[item.ID] = itemExtra{detail: &item, tasks: item.Tasks}
			continue
		}

		// A server predating the embed omits both fields. Falling back rather
		// than trusting the zero value matters: treating absent as empty would
		// erase every task and dependency locally on the first pull against an
		// older API, so deploy order between this and ichrisbirch cannot lose data.
		detail, err := fetchJSON[*model.ProjectItemDetail](ctx, e.client, e.base, fmt.Sprintf("/project-items/%s", item.ID))
		if err != nil {
			return fmt.Errorf("pulling item %s detail: %w", item.ID, err)
		}

		tasks, err := fetchJSON[[]model.ProjectItemTask](ctx, e.client, e.base, fmt.Sprintf("/project-items/%s/tasks", item.ID))
		if err != nil {
			return fmt.Errorf("pulling item %s tasks: %w", item.ID, err)
		}

		extras[item.ID] = itemExtra{detail: detail, tasks: tasks}
	}

	// Reconcile in a transaction
	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning sync transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := e.q.WithTx(tx)

	// Entities changed locally while the fetch was in flight. The server response
	// predates them, so the sweeps below would read them as "deleted upstream" and
	// erase a row the user created seconds ago. Ids are UUIDv7 and unique across
	// tables, so one set covers projects, items, and tasks alike.
	pendingIDs, err := qtx.ListPendingSyncEntityIDsAfter(ctx, pushedThrough)
	if err != nil {
		return fmt.Errorf("listing in-flight sync entities: %w", err)
	}
	queuedLocally := make(map[string]bool, len(pendingIDs))
	for _, id := range pendingIDs {
		queuedLocally[id] = true
	}

	// Upsert projects
	serverProjectIDs := make(map[string]bool, len(projects))
	for _, p := range projects {
		serverProjectIDs[p.ID] = true
		// A server predating the status column sends none, and an empty status
		// would violate NOT NULL and fail the whole pull. Absent means active,
		// which is what every project was before the column existed.
		status := p.Status
		if status == "" {
			status = model.StatusActive
		}
		if err := qtx.UpsertProject(ctx, generated.UpsertProjectParams{
			ID:           p.ID,
			Name:         p.Name,
			Description:  nullStr(p.Description),
			Status:       status,
			StatusReason: nullStr(p.StatusReason),
			ClosedAt:     nullTime(p.ClosedAt),
			Position:     int64(p.Position),
			CreatedAt:    p.CreatedAt.Format(time.RFC3339Nano),
		}); err != nil {
			return fmt.Errorf("upserting project %s: %w", p.ID, err)
		}
	}

	// Delete local projects not on server
	localProjects, err := qtx.ListAllProjectsRaw(ctx)
	if err != nil {
		return fmt.Errorf("listing local projects: %w", err)
	}
	// Which projects a membership may legally point at once this loop is done:
	// what the server sent, plus anything created locally that the sweep spares.
	projectExists := make(map[string]bool, len(serverProjectIDs))
	for id := range serverProjectIDs {
		projectExists[id] = true
	}
	var staleProjects []string
	for _, lp := range localProjects {
		if !serverProjectIDs[lp.ID] && !queuedLocally[lp.ID] {
			staleProjects = append(staleProjects, lp.ID)
			continue
		}
		projectExists[lp.ID] = true
	}
	if err := guardSweep("projects", len(staleProjects), len(localProjects), opts.AllowLargeSweep); err != nil {
		return err
	}
	for _, id := range staleProjects {
		if err := qtx.DeleteProject(ctx, id); err != nil {
			return fmt.Errorf("deleting stale project %s: %w", id, err)
		}
	}

	// Upsert items, unnumbered.
	//
	// idx_items_number is unique and the upsert writes row by row, so an item
	// taking a number another row has not given up yet fails the whole pull —
	// which is what any renumbering upstream looks like from here. Clearing
	// every server item to unnumbered first makes the pass order-independent;
	// the index is partial over non-null numbers, so any number of rows may sit
	// unnumbered in between.
	serverItemIDs := make(map[string]bool, len(items))
	for _, item := range items {
		serverItemIDs[item.ID] = true
		if err := qtx.UpsertItem(ctx, generated.UpsertItemParams{
			ID:        item.ID,
			Title:     item.Title,
			Notes:     nullStr(item.Notes),
			Repo:      nullStr(item.Repo),
			Completed: boolToInt(item.Completed),
			Archived:  boolToInt(item.Archived),
			CreatedAt: item.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt: item.UpdatedAt.Format(time.RFC3339Nano),
		}); err != nil {
			return fmt.Errorf("upserting item %s: %w", item.ID, err)
		}
	}
	// Delete local items not on server
	localItems, err := qtx.ListAllItemsRaw(ctx)
	if err != nil {
		return fmt.Errorf("listing local items: %w", err)
	}
	itemExists := make(map[string]bool, len(serverItemIDs))
	for id := range serverItemIDs {
		itemExists[id] = true
	}
	var staleItems []string
	for _, li := range localItems {
		if !serverItemIDs[li.ID] && !queuedLocally[li.ID] {
			staleItems = append(staleItems, li.ID)
			continue
		}
		itemExists[li.ID] = true
	}
	if err := guardSweep("items", len(staleItems), len(localItems), opts.AllowLargeSweep); err != nil {
		return err
	}
	for _, id := range staleItems {
		if err := qtx.DeleteItem(ctx, id); err != nil {
			return fmt.Errorf("deleting stale item %s: %w", id, err)
		}
	}

	// Numbers go on last of all, once the rows that are leaving have left: an
	// item deleted upstream still holds its number until the sweep above runs,
	// and the server reissuing that number to something else is exactly the
	// collision this ordering avoids.
	for _, item := range items {
		if item.Number == nil {
			continue
		}
		if err := qtx.SetItemNumber(ctx, generated.SetItemNumberParams{
			ID:     item.ID,
			Number: nullInt(item.Number),
		}); err != nil {
			return fmt.Errorf("numbering item %s: %w", item.ID, err)
		}
	}

	// Replace all memberships with server state
	if err := qtx.DeleteAllMemberships(ctx); err != nil {
		return fmt.Errorf("clearing memberships: %w", err)
	}
	for _, extra := range extras {
		if extra.detail == nil {
			continue
		}
		for _, proj := range extra.detail.Projects {
			// An item detail can name a project the projects endpoint did not
			// return — the two answers are assembled separately server-side and
			// nothing makes them agree. The link is dropped rather than
			// inserted: it has no project to point at, and failing the pull over
			// it would take the whole reconcile down with it, which is the
			// opposite of local-first.
			if !projectExists[proj.ID] || !itemExists[extra.detail.ID] {
				continue
			}
			if err := qtx.UpsertMembership(ctx, generated.UpsertMembershipParams{
				ItemID:    extra.detail.ID,
				ProjectID: proj.ID,
				Position:  0, // server doesn't return per-project position in detail
			}); err != nil {
				return fmt.Errorf("upserting membership: %w", err)
			}
		}
	}

	// Replace all dependencies with server state
	if err := qtx.DeleteAllDependencies(ctx); err != nil {
		return fmt.Errorf("clearing dependencies: %w", err)
	}
	for _, extra := range extras {
		if extra.detail == nil {
			continue
		}
		for _, depID := range extra.detail.DependencyIDs {
			// Same reasoning as memberships: a blocker the server no longer
			// lists has no row to reference.
			if !itemExists[depID] || !itemExists[extra.detail.ID] {
				continue
			}
			if err := qtx.UpsertDependency(ctx, generated.UpsertDependencyParams{
				ItemID:      extra.detail.ID,
				DependsOnID: depID,
			}); err != nil {
				return fmt.Errorf("upserting dependency: %w", err)
			}
		}
	}

	// Upsert tasks and delete stale ones
	serverTaskIDs := make(map[string]bool)
	for _, extra := range extras {
		for _, task := range extra.tasks {
			if !itemExists[task.ItemID] {
				continue
			}
			serverTaskIDs[task.ID] = true
			if err := qtx.UpsertTask(ctx, generated.UpsertTaskParams{
				ID:        task.ID,
				ItemID:    task.ItemID,
				Title:     task.Title,
				Completed: boolToInt(task.Completed),
				Position:  int64(task.Position),
				CreatedAt: task.CreatedAt.Format(time.RFC3339Nano),
			}); err != nil {
				return fmt.Errorf("upserting task %s: %w", task.ID, err)
			}
		}
	}

	localTasks, err := qtx.ListAllTasks(ctx)
	if err != nil {
		return fmt.Errorf("listing local tasks: %w", err)
	}
	for _, lt := range localTasks {
		if !serverTaskIDs[lt.ID] && !queuedLocally[lt.ID] {
			if err := qtx.DeleteTask(ctx, lt.ID); err != nil {
				return fmt.Errorf("deleting stale task %s: %w", lt.ID, err)
			}
		}
	}

	// Clear only the ops this reconciliation superseded. Anything queued after the
	// high-water mark postdates the server response and still has to be pushed.
	if err := qtx.DeletePendingSyncUpTo(ctx, pushedThrough); err != nil {
		return fmt.Errorf("clearing pending sync: %w", err)
	}

	// Update sync state
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, entityType := range []string{"project", "item", "task"} {
		if err := qtx.UpsertSyncState(ctx, generated.UpsertSyncStateParams{
			EntityType: entityType,
			LastPullAt: now,
			LastPushAt: now,
		}); err != nil {
			return fmt.Errorf("updating sync state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing sync: %w", err)
	}

	// Ops that survived the truncation were queued mid-pull and are still unsent.
	if len(queuedLocally) > 0 {
		e.Notify()
	}
	return nil
}

// fetchJSON performs a GET request and decodes the JSON response into T.
func fetchJSON[T any](ctx context.Context, client *http.Client, baseURL, path string) (T, error) {
	var zero T

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return zero, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return zero, friendlyNetErr(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return zero, refusal("API error", resp.StatusCode, body)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("decoding response: %w", err)
	}
	return result, nil
}

func nullStr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// nullTime stores a nullable timestamp in the same RFC3339Nano text every other
// column uses, so a value written by a pull and one written by SQLite's own
// strftime sort against each other.
func nullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339Nano), Valid: true}
}

func nullInt(n *int) sql.NullInt64 {
	if n == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*n), Valid: true}
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
