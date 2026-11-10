// Command projects-api serves projects over HTTP on Postgres. With the argument
// migrate it brings the database's schema up to date and exits instead.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/datapointchris/goapiauth"
	"github.com/datapointchris/goapiauth/tokenorsession"
	"github.com/datapointchris/gobrowserlogin"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/datapointchris/projects/api/authentication"
	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/api/routes"
	"github.com/datapointchris/projects/api/settings"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/postgres"
)

// version is set at build time.
var version = "dev"

// drainTimeout is how long a request in flight gets after TERM. The Deployment's
// grace period has to cover it and the preStop sleep.
const drainTimeout = 20 * time.Second

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], log)
	stop()
	if err != nil {
		log.Error("projects-api stopped", "err", err, "version", version)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, log *slog.Logger) error {
	switch {
	case len(args) == 0:
		return serve(ctx, log)
	case len(args) == 1 && args[0] == "migrate":
		return migrate(ctx, log)
	default:
		return fmt.Errorf("unknown arguments %q: run with none to serve, or with migrate", args)
	}
}

func migrate(ctx context.Context, log *slog.Logger) error {
	database, err := settings.LoadPostgres()
	if err != nil {
		return err
	}
	if err := postgres.Migrate(ctx, database.URL()); err != nil {
		return err
	}
	log.InfoContext(ctx, "migrations applied", "version", version)
	return nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	loaded, err := settings.LoadServer()
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, loaded.URL())
	if err != nil {
		return err
	}
	defer pool.Close()
	postgresStore := postgres.New(pool)

	provider, err := goapiauth.NewConnecting(authentication.Config(loaded.OIDCIssuer))
	if err != nil {
		return fmt.Errorf("reading the identity provider's settings: %w", err)
	}
	go provider.Run(ctx, log)

	checks := []routes.ReadinessCheck{
		{Name: "postgres", Check: postgresStore.Ping},
		{Name: "identity provider", Check: func(context.Context) error { return provider.Ready() }},
	}
	public := routes.PublicPaths
	var browserLogin *gobrowserlogin.Connecting
	if loaded.BrowserLogin() {
		browserLogin, err = authentication.NewBrowserLogin(stdlib.OpenDBFromPool(pool), loaded.OIDCIssuer, loaded.WebClientID, loaded.WebClientSecret, loaded.PublicURL, log)
		if err != nil {
			return fmt.Errorf("reading the browser login's settings: %w", err)
		}
		go browserLogin.Run(ctx)
	} else {
		log.WarnContext(ctx, "WEB_CLIENT_SECRET is unset, so the API logs no browser in and only a bearer token reaches it")
	}

	svc := service.New(postgresStore)
	mux := routes.New(svc, log, version, checks...)
	if loaded.PublicURL != "" {
		authentication.Metadata(mux, loaded.PublicURL, loaded.OIDCIssuer)
	} else {
		log.WarnContext(ctx, "PUBLIC_URL is unset, so no RFC 9728 metadata is served and a client cannot discover the issuer")
	}
	var sessions tokenorsession.Sessions
	if browserLogin != nil {
		public = append(slices.Clone(public), authentication.MountBrowserLogin(mux, browserLogin, log)...)
		sessions = browserLogin
	}
	protected := authentication.Protect(mux, routes.ServiceScopes, provider, sessions, loaded.WebClientID, log, public...)

	server := &http.Server{
		Addr:         net.JoinHostPort("", loaded.ListenPort),
		Handler:      requestlog.Middleware(log, routes.WithRequestDeadline(protected)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return listenUntilDone(ctx, server, log)
}

// listenUntilDone serves until the signal, then lets requests in flight finish. A
// drained server exits zero, so a rollout reads as healthy.
func listenUntilDone(ctx context.Context, server *http.Server, log *slog.Logger) error {
	listening := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "listening", "addr", server.Addr, "version", version)
		listening <- server.ListenAndServe()
	}()

	select {
	case err := <-listening:
		return err
	case <-ctx.Done():
	}

	log.Info("draining", "timeout", drainTimeout.String())
	drainContext, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := server.Shutdown(drainContext); err != nil {
		return fmt.Errorf("draining: %w", err)
	}
	if err := <-listening; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}
