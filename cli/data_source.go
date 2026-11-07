package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datapointchris/goclilogin"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/api/routes"
	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/config"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
	"github.com/datapointchris/projects/sync"
)

// inProcessBase is the address the client is given when the routes run inside
// this process. Nothing dials it.
const inProcessBase = "http://projects.invalid"

// requestTimeout bounds one call to the API, its retries and their pauses
// included. tokenTimeout bounds one token fetch from the login provider.
const (
	requestTimeout = 60 * time.Second
	tokenTimeout   = 30 * time.Second
)

// errNeedsLogin is API mode with no token stored for this machine's client.
var errNeedsLogin = errors.New("not logged in")

// dataSource is where this run's projects live: the API the config names, or the
// database on this machine. Every command reads and writes through client, so the
// routes, refusals and JSON are the API's in both.
type dataSource struct {
	client *apiclient.Client
	cfg    *config.Config

	// API mode.
	deployment *goclilogin.Deployment

	// Database mode.
	database *sql.DB
	local    *service.Service
	engine   *sync.Engine
	started  bool

	// writeRefusal is set where a write through the routes would be lost.
	writeRefusal string
}

// LocalService is the service over a database this machine owns outright, which
// numbers items itself because nothing upstream will.
func LocalService(database *sql.DB, reposRegistryPath string) *service.Service {
	return service.New(sqlite.New(database), service.AssigningItemNumbers(), service.RefusingRepoNames(reposRegistryPath))
}

// runMode is where a run's data lives.
type runMode int

const (
	databaseMode runMode = iota
	apiMode
)

// openDataSource opens where this run's data lives, as decideMode answers it.
func openDataSource(cmd *cobra.Command) (*dataSource, error) {
	cfg, deployment, mode, err := decideMode(cmd.Context())
	if err != nil {
		return nil, err
	}
	if mode == apiMode {
		return openAPI(cmd.Context(), cfg, deployment)
	}
	return openDatabase(cfg)
}

// decideMode reads the config file and the login settings in it, and answers
// which store this run uses. goclilogin's reading of api is the one the mode
// follows, because it is the one every request is then sent with.
func decideMode(ctx context.Context) (*config.Config, goclilogin.Deployment, runMode, error) {
	if err := config.RefuseConfigUnderFormerName(); err != nil {
		return nil, goclilogin.Deployment{}, databaseMode, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, goclilogin.Deployment{}, databaseMode, err
	}
	deployment, loadErr := loadDeployment(ctx, apiOverride)
	mode, err := chooseMode(cfg, deployment.API.Value, apiPhrase(deployment.API))
	if err != nil {
		return nil, deployment, mode, err
	}
	// With no api, the deployment's only error worth stopping for is a file it
	// could not read: a broken api line must not fall back to the database.
	if loadErr != nil && (mode == apiMode || !errors.Is(loadErr, goclilogin.ErrNotConfigured)) {
		return nil, deployment, mode, loadErr
	}
	return cfg, deployment, mode, nil
}

// chooseMode is the one answer to where a run's data lives: the API api names, or
// this machine's database when it names none. A synced database beside an API is
// refused, because the two are authorities for the same rows. apiPhrase says
// what named the API.
func chooseMode(cfg *config.Config, api, apiPhrase string) (runMode, error) {
	if api == "" {
		return databaseMode, nil
	}
	if cfg.Sync.Enabled {
		chosenBy, remedy := syncChosenBy()
		return apiMode, fmt.Errorf("%s, and %s keeps a synced database, which are two authorities for the same rows; "+
			"%s to work through the API", apiPhrase, chosenBy, remedy)
	}
	return apiMode, nil
}

// apiPhrase names what chose the API, as the first thing a person checks.
func apiPhrase(api goclilogin.Setting) string {
	if api.Source == goclilogin.SourceCommandLine {
		return "--api sends this command to " + api.Value
	}
	return fmt.Sprintf("api in %s sends every command to %s", homeAsTilde(api.From), api.Value)
}

// syncChosenBy names what turned sync on and how to turn it off. The variable
// outranks the file, so it is named whenever it is set.
func syncChosenBy() (chosenBy, remedy string) {
	if value := os.Getenv(config.SyncVariable); value != "" {
		return fmt.Sprintf("%s=%s in the environment", config.SyncVariable, value), "unset " + config.SyncVariable
	}
	path := homeAsTilde(configFilePath())
	return "[sync] enabled = true in " + path, "remove [sync] from " + path
}

// openForWriting is openDataSource for a command that writes, refused before it
// asks anything where the write would be lost.
func openForWriting(cmd *cobra.Command) (*dataSource, error) {
	source, err := openDataSource(cmd)
	if err != nil {
		return nil, err
	}
	if source.writeRefusal != "" {
		_ = source.Close()
		return nil, errors.New(source.writeRefusal)
	}
	return source, nil
}

func openAPI(ctx context.Context, cfg *config.Config, deployment goclilogin.Deployment) (*dataSource, error) {
	source := &dataSource{cfg: cfg, deployment: &deployment}
	// oauth2 fetches a token through the context its source was built with, so the
	// fetch is bounded by that context's client rather than by the request's.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenTimeout})
	tokens, err := tokenSource(ctx, deployment)
	if err != nil {
		return nil, source.explain(err)
	}
	// The token is fetched outside the retries, so a provider that does not answer
	// is asked once per request and is never reported as the API.
	httpClient := &http.Client{
		Transport: &oauth2.Transport{
			Source: oauth2.ReuseTokenSource(nil, failedTokensMarked{tokens}),
			Base:   apiclient.Retrying(http.DefaultTransport),
		},
		Timeout: requestTimeout,
	}
	source.client = apiclient.New(deployment.API.Value, httpClient)
	return source, nil
}

// tokenFailure is a token this run could not get, so no request was sent.
type tokenFailure struct{ error }

func (f tokenFailure) Unwrap() error { return f.error }

// failedTokensMarked marks every failure to get a token, so explain words it as
// the login provider's rather than the API's.
type failedTokensMarked struct{ next oauth2.TokenSource }

func (m failedTokensMarked) Token() (*oauth2.Token, error) {
	token, err := m.next.Token()
	if err != nil {
		return nil, tokenFailure{err}
	}
	return token, nil
}

// tokenSource is the config file's token_command where it names one, the
// client-credentials grant when PROJECTS_CLIENT_SECRET is set, and otherwise the
// token this machine logged in for.
func tokenSource(ctx context.Context, d goclilogin.Deployment) (oauth2.TokenSource, error) {
	if d.HandsTokens() {
		return goclilogin.CommandTokenSource(ctx, d.TokenCommand.Argv())
	}
	if isService() {
		return goclilogin.ClientCredentialsTokenSource(ctx, serviceClient(d), clientSecret())
	}
	login := loginConfig(d)
	source, err := goclilogin.TokenSource(ctx, login, goclilogin.NewTokenStore(login))
	if errors.Is(err, goclilogin.ErrNotLoggedIn) {
		return nil, errNeedsLogin
	}
	return source, err
}

func openDatabase(cfg *config.Config) (*dataSource, error) {
	if err := config.MoveDataUnderFormerName(cfg.Local.DBPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Local.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	database, err := sqlite.Open(cfg.Local.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	source := &dataSource{cfg: cfg, database: database}
	if cfg.Sync.Enabled {
		source.local = service.New(sqlite.New(database), service.RefusingRepoNames(repos.DefaultPath()))
		source.engine = sync.New(database, cfg.Sync.APIURL, cfg.Sync.TokenCommand)
		chosenBy, remedy := syncChosenBy()
		source.writeRefusal = fmt.Sprintf("%s keeps this database a copy of %s, and a write from a command would "+
			"never reach it; make the change in `projects tui`, or %s and set api in %s to write through the projects API",
			chosenBy, cfg.Sync.APIURL, remedy, homeAsTilde(configFilePath()))
	} else {
		source.local = LocalService(database, repos.DefaultPath())
	}

	// A fault in a route is logged with its cause, which the answer only names by
	// request id. The request line itself is for a server's log, not a terminal.
	faults := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := requestlog.Middleware(quiet, routes.New(source.local, faults))
	if source.writeRefusal != "" {
		handler = refusingWrites(handler, source.writeRefusal)
	}
	source.client = apiclient.New(inProcessBase, &http.Client{Transport: apiclient.InProcess(handler)})
	return source, nil
}

// refusingWrites answers every request but a read with the sentence saying why.
// It backs up openForWriting for any write a command reaches without it.
func refusingWrites(next http.Handler, sentence string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(wire.Error{Error: http.StatusText(http.StatusConflict), Message: sentence})
	})
}

// startSync starts the background push loop, for the commands that sync.
func (s *dataSource) startSync() {
	if s.engine != nil && !s.started {
		s.engine.Start()
		s.started = true
	}
}

// boardBackend is what the TUI reads and writes: the API over the client, or the
// database's own service, wrapped to queue each write when sync is on.
func (s *dataSource) boardBackend() backend.Backend {
	switch {
	case s.deployment != nil:
		return apiclient.NewBackend(s.client)
	case s.engine != nil:
		return sync.NewSyncBackend(s.local, s.engine)
	default:
		return s.local
	}
}

// repoRegistry is what a repo tag is checked against: this machine's registry in
// database mode, and an empty one through an API, which holds tags any machine
// may have written.
func (s *dataSource) repoRegistry() (*repos.Registry, error) {
	if s.deployment != nil {
		return &repos.Registry{}, nil
	}
	return repos.Load(repos.DefaultPath())
}

// apiHost is the API's host, or "" in database mode.
func (s *dataSource) apiHost() string {
	if s.deployment == nil {
		return ""
	}
	if parsed, err := url.Parse(s.deployment.API.Value); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return s.deployment.API.Value
}

func (s *dataSource) Close() error {
	if s.engine != nil && s.started {
		s.engine.Stop()
	}
	if s.database != nil {
		return s.database.Close()
	}
	return nil
}

// homeAsTilde writes a path under the home directory as ~, the way a person reads it.
func homeAsTilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, under := strings.CutPrefix(path, home+string(filepath.Separator)); under {
		return "~/" + rest
	}
	return path
}
