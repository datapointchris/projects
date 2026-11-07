package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	gosync "sync"
	"time"

	"github.com/datapointchris/projects/db/generated"
)

// apiPrefix is the API version every path and decoder here is written against.
const apiPrefix = "/api/v1"

// Engine orchestrates push and pull synchronization with the remote API.
type Engine struct {
	db     *sql.DB
	q      *generated.Queries
	client *http.Client
	apiURL string
	base   string

	mu        gosync.Mutex
	status    SyncStatus
	syncDepth int

	// drainMu serializes drainPendingOps. It reads the oldest queued op, pushes
	// it, and only then deletes it, so two drains at once both read the same op
	// and both push it — a 201 followed by a 409 for every create, with the
	// server burning an item number on the insert that lost. Two can overlap
	// easily: Notify wakes the push loop on the same mutation that a caller then
	// flushes for, and the retry ticker fires regardless of either.
	drainMu gosync.Mutex

	pushCh        chan struct{}
	retryInterval time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
}

// Option customizes an Engine at construction.
type Option func(*Engine)

// WithPushRetryInterval overrides how often the push loop re-attempts a queue it
// failed to drain. Tests use it to avoid waiting out the production interval.
func WithPushRetryInterval(d time.Duration) Option {
	return func(e *Engine) { e.retryInterval = d }
}

// New creates a sync engine. Call Start() to launch the background push loop.
// A non-empty tokenCommand is split on spaces and run without a shell, and every
// request carries the bearer token it prints (tokenTransport). Empty sends no
// Authorization header.
func New(db *sql.DB, apiURL, tokenCommand string, opts ...Option) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Timeout: 10 * time.Second}
	if argv := strings.Fields(tokenCommand); len(argv) > 0 {
		client.Transport = newTokenTransport(argv, http.DefaultTransport)
	}
	e := &Engine{
		db:            db,
		q:             generated.New(db),
		client:        client,
		apiURL:        apiURL,
		base:          strings.TrimRight(apiURL, "/") + apiPrefix,
		pushCh:        make(chan struct{}, 1),
		retryInterval: defaultPushRetryInterval,
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// APIURL returns the configured address of the server this engine syncs with.
func (e *Engine) APIURL() string { return e.apiURL }

// Start launches the background push loop goroutine.
func (e *Engine) Start() {
	go func() {
		defer close(e.done)
		e.pushLoop()
	}()
}

// Stop cancels the background loop and waits for it to finish. Callers close the
// database right after this returns, so returning early leaves the loop mid-query
// against a closed handle, which fails with "sql: database is closed" on exit.
// The timeout keeps an in-flight HTTP push from hanging exit.
func (e *Engine) Stop() {
	e.cancel()
	select {
	case <-e.done:
	case <-time.After(2 * time.Second):
	}
}

// QueueOp inserts a pending sync operation into the database.
func (e *Engine) QueueOp(op OpType, entityID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling sync payload: %w", err)
	}
	return e.q.InsertPendingSync(e.ctx, generated.InsertPendingSyncParams{
		Operation:  string(op),
		EntityType: op.entityType(),
		EntityID:   entityID,
		Payload:    string(data),
	})
}

// DropOpsForEntity removes every queued operation for one entity and reports how
// many were dropped. Used when a local change is reversed: the queued operations
// describe a state that no longer exists, and pushing them would recreate it on
// the server. A non-zero count also means nothing for that entity reached the
// server yet, since operations are deleted once pushed.
func (e *Engine) DropOpsForEntity(entityID string) (int64, error) {
	return e.q.DeletePendingSyncByEntity(e.ctx, entityID)
}

// Notify signals the push loop to wake up and process pending operations.
// Non-blocking: if the push loop is already signaled, this is a no-op.
func (e *Engine) Notify() {
	select {
	case e.pushCh <- struct{}{}:
	default:
	}
}

// Status returns the current sync status (thread-safe).
func (e *Engine) Status() SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()

	count, err := e.q.CountPendingSync(e.ctx)
	if err != nil {
		return e.status
	}
	e.status.PendingCount = int(count)
	return e.status
}

func (e *Engine) setStatus(fn func(s *SyncStatus)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.status)
}

// beginSync/endSync track sync depth rather than a bare flag: Pull calls
// drainPendingOps inside its own window, and a bool would clear on the inner
// call's return, reporting SYNCED while a full pull was still in flight.
func (e *Engine) beginSync() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.syncDepth++
	e.status.Syncing = true
}

func (e *Engine) endSync() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.syncDepth--
	if e.syncDepth <= 0 {
		e.syncDepth = 0
		e.status.Syncing = false
	}
}

// defaultPushRetryInterval is how often the push loop re-attempts a queue it
// failed to drain. Notify only fires on a local mutation, so without this a push
// that failed while the API was unreachable stayed queued until the user
// happened to edit something else — the queue never healed on its own.
const defaultPushRetryInterval = 30 * time.Second

func (e *Engine) pushLoop() {
	retry := time.NewTicker(e.retryInterval)
	defer retry.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.pushCh:
			e.drainPendingOps()
		case <-retry.C:
			e.retryPendingOps()
		}
	}
}

// retryPendingOps drains only when something is actually queued. An unconditional
// drain would report Connected on an empty queue without having reached the
// network, clearing a pull failure the status bar should still be showing.
func (e *Engine) retryPendingOps() {
	count, err := e.q.CountPendingSync(e.ctx)
	if err != nil || count == 0 {
		return
	}
	e.drainPendingOps()
}

func (e *Engine) drainPendingOps() {
	e.drainMu.Lock()
	defer e.drainMu.Unlock()

	e.beginSync()
	defer e.endSync()

	// The push loop runs unattended, so this is the only thing standing between
	// a mispointed engine and one database's work landing in another's API.
	// Nothing is lost by stopping: the ops stay queued for a correctly pointed
	// engine to drain.
	if err := e.checkOriginMatch(e.ctx); err != nil {
		e.setStatus(func(s *SyncStatus) {
			s.Connected = false
			s.LastError = err.Error()
		})
		return
	}

	for {
		if e.ctx.Err() != nil {
			return
		}

		op, err := e.q.GetOldestPendingSync(e.ctx)
		if err == sql.ErrNoRows {
			e.setStatus(func(s *SyncStatus) {
				s.Connected = true
				s.LastError = ""
			})
			return
		}
		if err != nil {
			// Shutdown cancels the context mid-query. That is the normal exit
			// path, not a fault worth printing on every invocation.
			if !errors.Is(err, context.Canceled) {
				log.Printf("sync: reading pending op: %v", err)
			}
			return
		}

		if err := e.executePush(op); err != nil {
			e.setStatus(func(s *SyncStatus) {
				s.Connected = false
				s.LastError = err.Error()
			})
			_ = e.q.UpdatePendingSyncError(e.ctx, generated.UpdatePendingSyncErrorParams{
				LastError: sql.NullString{String: err.Error(), Valid: true},
				ID:        op.ID,
			})
			// Back off briefly, then exit loop. Will retry on next Notify.
			select {
			case <-time.After(2 * time.Second):
			case <-e.ctx.Done():
			}
			return
		}

		_ = e.q.DeletePendingSync(e.ctx, op.ID)
		e.setStatus(func(s *SyncStatus) {
			s.Connected = true
			s.LastError = ""
		})
	}
}
