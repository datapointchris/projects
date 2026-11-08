package cli

import (
	"context"
	"database/sql"
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
	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/config"
	"github.com/datapointchris/projects/repos"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
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
}

// LocalService is the service over a database this machine owns outright.
func LocalService(database *sql.DB, reposRegistryPath string) *service.Service {
	return service.New(sqlite.New(database), service.RefusingRepoNames(reposRegistryPath))
}

// runMode is where a run's data lives.
type runMode int

const (
	databaseMode runMode = iota
	apiMode
)

// openDataSource opens where this run's data lives, as decideMode answers it.
func openDataSource(cmd *cobra.Command) (*dataSource, error) {
	cfg, deployment, mode, err := decideMode(cmd)
	if err != nil {
		return nil, err
	}
	if mode == apiMode {
		return openAPI(cmd.Context(), cfg, deployment)
	}
	return openDatabase(cfg)
}

// decideMode reads the config file and the login settings in it, and answers
// which store this run uses: the API api names, or this machine's database when
// it names none. goclilogin's reading of api is the one the mode follows, because
// it is the one every request is then sent with.
func decideMode(cmd *cobra.Command) (*config.Config, goclilogin.Deployment, runMode, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, goclilogin.Deployment{}, databaseMode, err
	}
	if err := config.RefuseConfigUnderFormerName(); err != nil {
		return nil, goclilogin.Deployment{}, databaseMode, err
	}
	warnOfRetiredSyncSection(cmd, cfg)
	deployment, loadErr := loadDeployment(cmd.Context(), apiOverride)
	mode := databaseMode
	if deployment.API.Value != "" {
		mode = apiMode
	}
	// With no api, the deployment's only error worth stopping for is a file it
	// could not read: a broken api line must not fall back to the database.
	if loadErr != nil && (mode == apiMode || !errors.Is(loadErr, goclilogin.ErrNotConfigured)) {
		return nil, deployment, mode, loadErr
	}
	return cfg, deployment, mode, nil
}

// warnOfRetiredSyncSection names a [sync] section the run ignores. It warns rather
// than refuses, because a machine's config is rewritten only by its next apply,
// and a refusal would stop the tool there until then.
func warnOfRetiredSyncSection(cmd *cobra.Command, cfg *config.Config) {
	if cfg.RetiredSync == nil {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: [sync] in %s is ignored, because projects keeps no synced copy "+
		"of an API. Remove the section. `projects auth login <url>` writes api, which sends every command to an API.\n",
		homeAsTilde(configFilePath()))
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
		return nil, fmt.Errorf("opening %s: %w", homeAsTilde(cfg.Local.DBPath), err)
	}
	source := &dataSource{cfg: cfg, database: database, local: LocalService(database, repos.DefaultPath())}

	// A fault in a route is logged with its cause, which the answer only names by
	// request id. The request line itself is for a server's log, not a terminal.
	faults := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := requestlog.Middleware(quiet, routes.New(source.local, faults))
	source.client = apiclient.New(inProcessBase, &http.Client{Transport: apiclient.InProcess(handler)})
	return source, nil
}

// boardBackend is what the TUI reads and writes: the API over the client, or the
// database's own service.
func (s *dataSource) boardBackend() backend.Backend {
	if s.deployment != nil {
		return apiclient.NewBackend(s.client)
	}
	return s.local
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
