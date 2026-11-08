// Package settings reads the API's configuration from the environment, which is the
// only place a container gets it from.
package settings

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

const (
	defaultListenPort   = "8000"
	defaultPostgresPort = "5432"
	defaultWebClientID  = "projects-web"
)

// Postgres is where the database is.
type Postgres struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

// URL is the connection string pgx and goose read. A host that drops packets fails
// its dial in seconds rather than at the system's TCP timeout.
func (p Postgres) URL() string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.Username, p.Password),
		Host:     net.JoinHostPort(p.Host, p.Port),
		Path:     "/" + p.Database,
		RawQuery: "connect_timeout=5",
	}).String()
}

// Server is everything serving needs.
type Server struct {
	Postgres
	OIDCIssuer string
	// PublicURL is the address clients reach the API at. Set, the API serves RFC
	// 9728 metadata naming the issuer, so a client can find where to log in.
	PublicURL string
	// WebClientID and WebClientSecret are the confidential client the API logs
	// the web app's browsers in as. The secret set turns that login on.
	WebClientID     string
	WebClientSecret string
	ListenPort      string
}

// BrowserLogin is whether the API logs browsers in, which the secret decides.
func (s Server) BrowserLogin() bool {
	return s.WebClientSecret != ""
}

// LoadPostgres reads the database's settings, the only ones the migration job needs.
func LoadPostgres() (Postgres, error) {
	var missing []string
	require := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			missing = append(missing, name)
		}
		return value
	}
	loaded := Postgres{
		Host:     require("POSTGRES_HOST"),
		Port:     orDefault("POSTGRES_PORT", defaultPostgresPort),
		Username: require("POSTGRES_USERNAME"),
		Password: require("POSTGRES_PASSWORD"),
		Database: require("POSTGRES_DB"),
	}
	if len(missing) > 0 {
		return Postgres{}, fmt.Errorf("settings not set: %s", strings.Join(missing, ", "))
	}
	return loaded, nil
}

// LoadServer reads every setting serving needs.
func LoadServer() (Server, error) {
	postgres, err := LoadPostgres()
	if err != nil {
		return Server{}, err
	}
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return Server{}, fmt.Errorf("settings not set: OIDC_ISSUER")
	}
	loaded := Server{
		Postgres:        postgres,
		OIDCIssuer:      issuer,
		PublicURL:       os.Getenv("PUBLIC_URL"),
		WebClientID:     orDefault("WEB_CLIENT_ID", defaultWebClientID),
		WebClientSecret: os.Getenv("WEB_CLIENT_SECRET"),
		ListenPort:      orDefault("PORT", defaultListenPort),
	}
	if loaded.BrowserLogin() && loaded.PublicURL == "" {
		return Server{}, fmt.Errorf("WEB_CLIENT_SECRET is set and PUBLIC_URL is not: the browser login's callback is PUBLIC_URL/api/auth/callback")
	}
	return loaded, nil
}

func orDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
