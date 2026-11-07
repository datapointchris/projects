package authentication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/datapointchris/gobrowserlogin"
	"github.com/datapointchris/gobrowserlogin/postgresstore"
)

// The browser login's routes, which a browser reaches before it holds a session.
const (
	LoginPath    = "/api/auth/login"
	CallbackPath = "/api/auth/callback"
)

// errLoginNotReady answers a session or a login before Run has read the provider
// and the session tables.
var errLoginNotReady = errors.New("the browser login has not read the identity provider and the session tables yet")

// BrowserLogin logs the web app's browsers in as this API's confidential client,
// and keeps each session in Postgres. It starts the way goapiauth.Connecting does:
// Run reads the provider and checks the session tables in the background, and
// until it has, a login and a request carrying only a session answer 503 while
// the rest of the API serves.
type BrowserLogin struct {
	db                    *sql.DB
	config                gobrowserlogin.Config
	firstRetry, lastRetry time.Duration

	sessions atomic.Pointer[gobrowserlogin.Sessions]
	failure  atomic.Pointer[error]
}

// NewBrowserLogin is the login for clientID at issuer, whose callback is under
// publicURL, keeping its sessions in db. Run starts it.
func NewBrowserLogin(db *sql.DB, issuer, clientID, clientSecret, publicURL string, log *slog.Logger) *BrowserLogin {
	return &BrowserLogin{
		db: db,
		config: gobrowserlogin.Config{
			Issuer:       issuer,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  strings.TrimSuffix(publicURL, "/") + CallbackPath,
			Logger:       log,
		},
		firstRetry: time.Second,
		lastRetry:  time.Minute,
	}
}

// Run builds the login, retrying after each failure with a wait that doubles from
// firstRetry to lastRetry, until it succeeds or ctx ends. A configuration
// gobrowserlogin refuses is retried too, and logged each time, so
// /ready names it rather than the process exiting before anyone reads why.
func (b *BrowserLogin) Run(ctx context.Context, log *slog.Logger) {
	wait := b.firstRetry
	for {
		sessions, err := b.connect(ctx)
		switch {
		case err == nil:
			b.sessions.Store(sessions)
			log.InfoContext(ctx, "browser login ready", "issuer", b.config.Issuer, "client_id", b.config.ClientID, "redirect_url", b.config.RedirectURL)
			return
		case ctx.Err() != nil:
			return
		}
		b.failure.Store(&err)
		log.WarnContext(ctx, "browser login not ready", "issuer", b.config.Issuer, "client_id", b.config.ClientID, "err", err, "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(2*wait, b.lastRetry)
	}
}

func (b *BrowserLogin) connect(ctx context.Context) (*gobrowserlogin.Sessions, error) {
	store, err := postgresstore.New(ctx, b.db)
	if err != nil {
		return nil, err
	}
	config := b.config
	config.Store = store
	return gobrowserlogin.New(ctx, config)
}

// Ready is nil once Run has built the login, and before then the last reason it
// could not.
func (b *BrowserLogin) Ready() error {
	if b.sessions.Load() != nil {
		return nil
	}
	if failure := b.failure.Load(); failure != nil {
		return fmt.Errorf("%w: %w", errLoginNotReady, *failure)
	}
	return errLoginNotReady
}

// Authenticate is the identity of the session a request carries. Before Run has
// built the login it answers an error tokenorsession turns into a 503.
func (b *BrowserLogin) Authenticate(r *http.Request) (gobrowserlogin.Identity, error) {
	sessions := b.sessions.Load()
	if sessions == nil {
		return gobrowserlogin.Identity{}, b.Ready()
	}
	return sessions.Authenticate(r)
}

// Mount serves the login and its callback on mux. Both are navigations, so
// Protect has to pass them with no credential.
func (b *BrowserLogin) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+LoginPath, b.whenReady(func(s *gobrowserlogin.Sessions) http.HandlerFunc { return s.Login }))
	mux.HandleFunc("GET "+CallbackPath, b.whenReady(func(s *gobrowserlogin.Sessions) http.HandlerFunc { return s.Callback }))
}

// whenReady answers a page saying to try again later while the login is not built.
// The browser navigated here, so the answer is text rather than the API's JSON.
func (b *BrowserLogin) whenReady(handler func(*gobrowserlogin.Sessions) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := b.sessions.Load()
		if sessions == nil {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "Logging in is not available yet, because the identity provider has not been reached. Try again in a minute.", http.StatusServiceUnavailable)
			return
		}
		handler(sessions)(w, r)
	}
}
