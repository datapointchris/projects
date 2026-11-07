package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// A pull deletes every local row the server did not return, which makes a
// database and the API it reconciles against a pair. Point one at the other
// one and the pull is not a sync but a wipe: it faithfully deletes everything
// the other API has never heard of. That is not a hypothetical — a production
// database reconciled against a development API lost 42 projects and 286 items
// in a single command that reported success.
//
// Nothing in a database says which API it belongs to, so nothing could refuse.
// The pairing is recorded on first pull and checked before every push and pull
// after that.

// OriginMismatch is returned when a database is asked to reconcile against an
// API other than the one it was adopted by.
type OriginMismatch struct {
	Recorded   string
	Configured string
}

func (e *OriginMismatch) Error() string {
	return fmt.Sprintf(
		"this database belongs to %s but sync is configured for %s — "+
			"reconciling would delete everything the other API has never seen. "+
			"Run `projects sync --adopt` if the move is deliberate",
		e.Recorded, e.Configured)
}

// normalizeAPIURL makes the comparison indifferent to the things that do not
// change which server is being addressed.
func normalizeAPIURL(url string) string {
	return strings.TrimRight(strings.TrimSpace(url), "/")
}

// OriginUnclaimed is returned when a database that has reconciled before has no
// record of which API it did that with. It was bound to something; adopting it
// silently to whatever is configured now is exactly the step that turns a
// misconfiguration into a wipe, so it is the user who says.
type OriginUnclaimed struct {
	Configured string
	Projects   int
	Items      int
}

func (e *OriginUnclaimed) Error() string {
	return fmt.Sprintf(
		"this database holds %d projects and %d items and has synced before, but has no record of "+
			"which API it belongs to — reconciling against %s would delete everything that API has "+
			"never seen. Run `projects sync --adopt` to bind it",
		e.Projects, e.Items, e.Configured)
}

// checkOriginMatch refuses only an outright mismatch. It is what the push path
// asks, because a push does not destroy local data: it only has to be stopped
// from delivering one database's work to another's API.
func (e *Engine) checkOriginMatch(ctx context.Context) error {
	recorded, err := e.q.GetSyncOrigin(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the database's sync origin: %w", err)
	}
	if configured := normalizeAPIURL(e.apiURL); recorded != configured {
		return &OriginMismatch{Recorded: recorded, Configured: configured}
	}
	return nil
}

// checkOrigin is the pull's stricter question: may this reconcile happen, and
// should it be the one to record the pairing.
//
// A database that has never pulled adopts on this one — that is every new
// install, whatever it has typed in locally, since a pull spares rows queued for
// push anyway. A database that has pulled before was bound to some API already,
// and if nothing recorded which, that has to be said out loud rather than
// inferred from whichever environment happens to be loaded.
func (e *Engine) checkOrigin(ctx context.Context) (adopt bool, err error) {
	configured := normalizeAPIURL(e.apiURL)

	recorded, err := e.q.GetSyncOrigin(ctx)
	switch {
	case err == nil:
		if recorded != configured {
			return false, &OriginMismatch{Recorded: recorded, Configured: configured}
		}
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("reading the database's sync origin: %w", err)
	}

	pulledBefore, err := e.q.HasPulledBefore(ctx)
	if err != nil {
		return false, fmt.Errorf("checking whether this database has synced before: %w", err)
	}
	if pulledBefore == 0 {
		return true, nil
	}

	projects, err := e.q.CountProjects(ctx)
	if err != nil {
		return false, fmt.Errorf("counting projects: %w", err)
	}
	items, err := e.q.CountItems(ctx)
	if err != nil {
		return false, fmt.Errorf("counting items: %w", err)
	}
	return false, &OriginUnclaimed{
		Configured: configured,
		Projects:   int(projects),
		Items:      int(items),
	}
}

// AdoptOrigin binds this database to the engine's API, overwriting whatever it
// was bound to. It is what `projects sync --adopt` calls: the one place the
// pairing can change, and only because someone said so.
func (e *Engine) AdoptOrigin(ctx context.Context) error {
	return e.q.SetSyncOrigin(ctx, normalizeAPIURL(e.apiURL))
}

// A pull deletes whatever the server did not return, so a truncated response
// and a genuine mass deletion are the same event seen from here. Scale is the
// only thing that separates them, and the cost of guessing wrong is asymmetric:
// a refused pull costs one flag, an obeyed one costs the database. The floor
// keeps the guard out of the way of small databases, where losing "most of it"
// is a handful of rows and routine.
const (
	sweepFloor = 10
	sweepShare = 0.5
)

// SweepRefused is returned when a pull would delete implausibly much.
type SweepRefused struct {
	Entity   string
	Deleting int
	Local    int
}

func (e *SweepRefused) Error() string {
	return fmt.Sprintf(
		"refusing to delete %d of %d %s in one pull — the API returned almost nothing this database has, "+
			"which is a truncated response or the wrong API far more often than it is a real deletion. "+
			"Run `projects sync --force` if it is genuinely what happened",
		e.Deleting, e.Local, e.Entity)
}

func guardSweep(entity string, deleting, local int, allowed bool) error {
	if allowed || deleting < sweepFloor || float64(deleting) <= float64(local)*sweepShare {
		return nil
	}
	return &SweepRefused{Entity: entity, Deleting: deleting, Local: local}
}

// Origin returns the API this database is bound to, or "" if it has never
// pulled.
func (e *Engine) Origin(ctx context.Context) string {
	recorded, err := e.q.GetSyncOrigin(ctx)
	if err != nil {
		return ""
	}
	return recorded
}
