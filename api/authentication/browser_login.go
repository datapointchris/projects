package authentication

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/datapointchris/gobrowserlogin"
	"github.com/datapointchris/gobrowserlogin/postgresstore"

	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/api/wire"
)

// The browser login's routes. A browser navigates to the login and the callback
// before it holds a session, and a page that lost its session still logs out.
const (
	loginPath    = "/api/auth/login"
	callbackPath = "/api/auth/callback"
	logoutPath   = "/api/auth/logout"
)

// NewBrowserLogin logs the web app's browsers in as clientID, this API's
// confidential client at issuer, and keeps each session in db. Its callback is
// under publicURL. It refuses a configuration that can never log anyone in, such
// as a publicURL on http at a host other than loopback. Run starts it.
func NewBrowserLogin(db *sql.DB, issuer, clientID, clientSecret, publicURL string, log *slog.Logger) (*gobrowserlogin.Connecting, error) {
	return gobrowserlogin.NewConnecting(gobrowserlogin.Config{
		Issuer:       issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  strings.TrimSuffix(publicURL, "/") + callbackPath,
		Logger:       log,
	}, func(ctx context.Context) (gobrowserlogin.Store, error) {
		return postgresstore.New(ctx, db)
	})
}

// MountBrowserLogin serves the login, its callback and the logout on mux, and
// returns their paths, which Protect passes with no credential.
func MountBrowserLogin(mux *http.ServeMux, login *gobrowserlogin.Connecting, log *slog.Logger) []string {
	mux.HandleFunc("GET "+loginPath, login.Login)
	mux.HandleFunc("GET "+callbackPath, login.Callback)
	mux.HandleFunc("POST "+logoutPath, func(w http.ResponseWriter, r *http.Request) {
		switch err := login.Logout(w, r); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, gobrowserlogin.ErrNoCSRFHeader):
			refuse(w, r, http.StatusForbidden, "a logout must send the "+gobrowserlogin.CSRFHeader+" header")
		default:
			log.ErrorContext(r.Context(), "logout failed", "err", err)
			refuse(w, r, http.StatusServiceUnavailable, "the session could not be ended, so it lasts until it times out")
		}
	})
	return []string{loginPath, callbackPath, logoutPath}
}

func refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	requestlog.NoteRefusal(r.Context(), message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(wire.Error{Error: http.StatusText(status), Message: message})
}
