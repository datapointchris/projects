package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// loginTimeout is a backstop. The device code carries its own expiry, and the
// poll stops there first.
const loginTimeout = 15 * time.Minute

func newAuthCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "auth",
		Short: "Log in and out of the projects API",
		Long: "Only API mode needs a login. With no api in the config file, every command\n" +
			"reads this machine's database and nothing here applies.\n" +
			"\n" +
			"Login uses the OAuth 2.0 device authorization grant. The CLI prints a code and\n" +
			"a URL, and you approve it in any browser on any device, including from another\n" +
			"machine over SSH. The token is kept in the OS keychain, or in a file only this\n" +
			"user can read where there is none.\n" +
			"\n" +
			"`projects auth status` prints the config file's path and its api, issuer and\n" +
			"client_id. `projects auth login <url>` fills in whatever it lacks.\n" +
			"\n" +
			"With " + clientSecretEnv + " set, projects authenticates as the service client\n" +
			"the config file's client_id names, through the client-credentials grant. It\n" +
			"asks for a token each run and stores none, so login and logout refuse.",
		Example: "  projects auth status                             who you are, and where projects points\n" +
			"  projects auth login                              log in to the config file's api\n" +
			"  projects auth login https://projects.example.com log in there, filling in the config file",
	})
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupChanging, withAPIFlag(newAuthLoginCommand()), newAuthLogoutCommand())...)
	cmd.AddCommand(inGroup(groupReading, withAPIFlag(newAuthStatusCommand()), withAPIFlag(newAuthTokenCommand()))...)
	return cmd
}

func newAuthLoginCommand() *cobra.Command {
	var clientID string
	cmd := &cobra.Command{
		Use:   "login [url]",
		Short: "Log in by approving a code in any browser",
		Long: "Logs in to the API at url, or to the config file's api when url is left out.\n" +
			"Whatever the file lacks is filled in: api from url, the issuer from the API's\n" +
			"RFC 9728 metadata, and the client id from --client-id or this machine's short\n" +
			"hostname. Each is printed with where it came from before the login starts, and\n" +
			"written to the file once it succeeds. A value already in the file is kept.\n" +
			"\n" +
			"Writing api to the file is what puts every command in API mode.",
		Example: "  projects auth login\n" +
			"  projects auth login https://projects.example.com",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isService() {
				return fmt.Errorf("%s is set, so projects authenticates as a service client with no login; unset it to log in as a person", clientSecretEnv)
			}
			url := apiOverride
			if len(args) == 1 {
				if apiOverride != "" && apiOverride != args[0] {
					return goclikit.UsageError(fmt.Errorf("--api %s and the url %s name different APIs; give one", apiOverride, args[0]))
				}
				url = args[0]
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), loginTimeout)
			defer cancel()

			deployment, err := deploymentForLogin(ctx, url, clientID)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Logging in to:")
			goclilogin.WriteDeployment(cmd.ErrOrStderr(), deployment)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr())

			login := loginConfig(deployment)
			store := goclilogin.NewTokenStore(login)
			// pkg/browser hands the launcher this process's streams, which puts a
			// browser's startup noise between the code and the URL. A launcher
			// that fails is still reported by OpenURL's error.
			browser.Stdout = io.Discard
			browser.Stderr = io.Discard

			token, err := goclilogin.Login(ctx, login, func(p goclilogin.DevicePrompt) {
				goclilogin.WriteInstructions(cmd.ErrOrStderr(), login.ClientID, p)
				if openErr := browser.OpenURL(p.BrowserURL()); openErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "(could not open a browser here: %v)\n", openErr)
				}
			})
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("timed out waiting for approval after %s; run `projects auth login` again", loginTimeout)
				}
				return err
			}
			backend, err := store.Save(login.ClientID, token)
			if err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			if backend == goclilogin.BackendFile {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nNo OS keyring on this host — the token is in %s, readable by this user only.\n", store.FilePath())
			}
			if err := deployment.Save(); err != nil {
				return fmt.Errorf("logged in as %s, but the config file was not written: %w", login.ClientID, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n✓ Logged in (%s)\n", login.ClientID)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "",
		"Log in as this OAuth client when the config file names none (default projects-cli-<short hostname>)")
	return cmd
}

func newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Remove the stored token from this machine",
		Example: "  projects auth logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if isService() {
				return fmt.Errorf("%s is set, so projects authenticates as a service client and stores no token to remove", clientSecretEnv)
			}
			// Only the client id finds the token, so a file lacking the other keys
			// still logs out, and one naming no client falls back to the id login
			// uses when the file names none.
			deployment, err := loadDeployment(cmd.Context(), "")
			if err != nil && !errors.Is(err, goclilogin.ErrNotConfigured) {
				return err
			}
			if deployment.HandsTokens() {
				return fmt.Errorf("%s sets token_command, so projects stores no token and there is nothing to log out of", deployment.Path)
			}
			if deployment.ClientID.Value == "" {
				deployment.ClientID.Value = defaultClientID()
			}
			clientID := deployment.ClientID.Value
			if err := goclilogin.NewTokenStore(loginConfig(deployment)).Delete(clientID); err != nil {
				if errors.Is(err, goclilogin.ErrNotLoggedIn) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Not logged in — nothing to remove.")
					return nil
				}
				return fmt.Errorf("remove token from keychain: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged out (%s).\n", clientID)
			return nil
		},
	}
}

func newAuthTokenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the current access token to stdout",
		Long: "Prints a valid access token, refreshing it first if it has expired. Pass it to\n" +
			"curl on stdin, as the example does, so the token never appears in another\n" +
			"process's arguments. Exits 1 if not logged in. With " + clientSecretEnv + " set\n" +
			"it asks for a new service token, and with token_command in the config file it\n" +
			"prints the token that command hands.",
		Example: "  projects auth token\n" +
			"  printf 'Authorization: Bearer %s\\n' \"$(projects auth token)\" | curl --header @- https://projects.example.com/api/v1/projects",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deployment, err := loadDeployment(cmd.Context(), apiOverride)
			if err != nil {
				return err
			}
			source, err := tokenSource(cmd.Context(), deployment)
			if errors.Is(err, errNeedsLogin) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Not logged in as %s. Run `projects auth login`.\n", deployment.ClientID.Value)
				return exitCode(1)
			}
			if err != nil {
				return err
			}
			token, err := source.Token()
			switch {
			case err != nil && deployment.HandsTokens():
				return fmt.Errorf("token_command in %s handed no token: run %q yourself to see what it prints: %w",
					homeAsTilde(deployment.Path), deployment.TokenCommand, err)
			case err != nil && isService():
				return fmt.Errorf("obtain a token for service client %s: %w", deployment.ClientID.Value, err)
			case err != nil:
				return fmt.Errorf("obtain a valid token (the refresh may have failed — try `projects auth login`): %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), token.AccessToken)
			return nil
		},
	}
}

// credentialType is whose credential `auth status` checked, spelled as Google's
// credential files spell a person's login, a service's, and one an executable
// supplies.
type credentialType string

const (
	authorizedUser  credentialType = "authorized_user"
	serviceAccount  credentialType = "service_account"
	externalAccount credentialType = "external_account"
)

// authStatus is `auth status --json`. cli/schemas/auth-status.schema.json is its
// shape. It reports what this machine holds, not what the token claims, because
// the claims are the API's to verify. For a service or a handed token, logged_in
// is true only when a token was granted just now, since neither stores one.
type authStatus struct {
	SchemaVersion int            `json:"schema_version"`
	LoggedIn      bool           `json:"logged_in"`
	Configured    bool           `json:"configured"`
	ConfigFile    string         `json:"config_file"`
	Type          credentialType `json:"type"`
	API           string         `json:"api"`
	ClientID      string         `json:"client_id"`
	Issuer        string         `json:"issuer"`
	ExpiresAt     *string        `json:"expires_at"`
	Expired       bool           `json:"expired"`
	// Session is what asking the provider for a token found, and null where
	// nothing was asked: no config, or no token stored.
	Session *goclilogin.SessionState `json:"session"`
	// Backend is where the token is kept: the OS keyring, or a mode-600 file on
	// a host with none. Null where nothing is stored.
	Backend *goclilogin.Backend `json:"backend"`
	// KeyringNote is the keyring's own failure, where it is why no token was found.
	KeyringNote *string `json:"keyring_note"`
}

func (r authStatus) session() goclilogin.SessionState {
	if r.Session == nil {
		return ""
	}
	return *r.Session
}

const authStatusVersion = 1

func newAuthStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where projects is pointed and whether it is logged in",
		Long: "Shows the api, issuer and client_id API mode uses, where each came from, the\n" +
			"config file's path, and whether a token is stored for the client. Exits 1 when\n" +
			"the file lacks a value, no token is stored, or the provider rejected the session.",
		Example: "  projects auth status\n  projects auth status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deployment, loadErr := loadDeployment(cmd.Context(), apiOverride)
			if loadErr != nil && !errors.Is(loadErr, goclilogin.ErrNotConfigured) {
				return loadErr
			}
			report := authStatus{
				SchemaVersion: authStatusVersion,
				Configured:    loadErr == nil,
				ConfigFile:    deployment.Path,
				Type:          authorizedUser,
				API:           deployment.API.Value,
				ClientID:      deployment.ClientID.Value,
				Issuer:        deployment.Issuer.Value,
			}
			switch {
			case deployment.HandsTokens():
				report.Type = externalAccount
			case isService():
				report.Type = serviceAccount
			}
			switch {
			case !report.Configured:
			case report.Type == externalAccount:
				source, err := goclilogin.CommandTokenSource(cmd.Context(), deployment.TokenCommand.Argv())
				grantedStatus(source, err, &report)
			case report.Type == serviceAccount:
				source, err := goclilogin.ClientCredentialsTokenSource(cmd.Context(), serviceClient(deployment), clientSecret())
				grantedStatus(source, err, &report)
			default:
				if err := loginStatus(cmd.Context(), deployment, &report); err != nil {
					return err
				}
			}
			if asJSON {
				if err := encodeJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				printAuthStatus(cmd.OutOrStdout(), report, deployment, loadErr)
			}
			// A rejected session exits 1 for the same reason being logged out does:
			// nothing run next works until a login. An unverified login exits 0,
			// because its stored token may still work.
			if !report.Configured || !report.LoggedIn || report.session() == goclilogin.SessionRejected {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output status as JSON to stdout")
	return cmd
}

// loginStatus fills report from the token this machine logged in for.
func loginStatus(ctx context.Context, d goclilogin.Deployment, report *authStatus) error {
	login := loginConfig(d)
	store := goclilogin.NewTokenStore(login)
	token, backend, err := store.Load(d.ClientID.Value)
	switch {
	case errors.Is(err, goclilogin.ErrNotLoggedIn):
		// goclilogin wraps the keyring's own failure where there was one. A host
		// with no Secret Service is fixed by logging in, and a locked keychain
		// is not, so the note is kept.
		if detail := strings.TrimPrefix(err.Error(), goclilogin.ErrNotLoggedIn.Error()); detail != "" {
			note := strings.Trim(detail, " ()")
			report.KeyringNote = &note
		}
	case err != nil:
		return fmt.Errorf("read token from keychain: %w", err)
	default:
		report.LoggedIn = true
		report.Backend = &backend
		setExpiry(report, token)
		// Asking for a token the way a command does is the one thing that tells
		// a soft expiry from a revoked grant.
		var session goclilogin.SessionState
		session, token = goclilogin.VerifySession(ctx, login, store)
		report.Session = &session
		setExpiry(report, token)
	}
	return nil
}

// grantedStatus asks source for a token, because a service or a handed token
// stores none that could say whether the next command gets one.
func grantedStatus(source oauth2.TokenSource, err error, report *authStatus) {
	var token *oauth2.Token
	if err == nil {
		token, err = source.Token()
	}
	session, token := goclilogin.ClassifySession(token, err)
	report.Session = &session
	report.LoggedIn = session == goclilogin.SessionLive
	if token != nil && !token.Expiry.IsZero() {
		expires := token.Expiry.UTC().Format(time.RFC3339)
		report.ExpiresAt = &expires
	}
}

func setExpiry(report *authStatus, token *oauth2.Token) {
	if token == nil || token.Expiry.IsZero() {
		return
	}
	expires := token.Expiry.UTC().Format(time.RFC3339)
	report.ExpiresAt = &expires
	report.Expired = time.Now().After(token.Expiry)
}

// printAuthStatus leads with the verdict on a line of its own, so a script
// checking a login reads the first line alone.
func printAuthStatus(out io.Writer, r authStatus, d goclilogin.Deployment, loadErr error) {
	switch {
	case !r.Configured:
		_, _ = fmt.Fprintf(out, "%v\n", loadErr)
	case r.Type == externalAccount && r.LoggedIn:
		_, _ = fmt.Fprintln(out, "Handed a token by token_command")
	case r.Type == externalAccount:
		_, _ = fmt.Fprintf(out, "token_command printed no access_token. Run %q yourself to see what it prints.\n", d.TokenCommand)
	case r.Type == serviceAccount && r.session() == goclilogin.SessionRejected:
		_, _ = fmt.Fprintf(out, "Service client refused by %s. Check client_id in the config file, and %s.\n", r.Issuer, clientSecretEnv)
	case r.Type == serviceAccount && r.session() == goclilogin.SessionUnverified:
		_, _ = fmt.Fprintf(out, "Service client configured; %s could not be reached.\n", r.Issuer)
	case r.Type == serviceAccount:
		_, _ = fmt.Fprintln(out, "Service client (client credentials)")
	case !r.LoggedIn:
		_, _ = fmt.Fprintf(out, "Not logged in as %s. Run `projects auth login` to authenticate.\n", r.ClientID)
	case r.session() == goclilogin.SessionRejected:
		_, _ = fmt.Fprintf(out, "Session rejected by %s. Run `projects auth login` to authenticate.\n", r.Issuer)
	default:
		_, _ = fmt.Fprintln(out, "Logged in")
	}
	goclilogin.WriteDeployment(out, d)
	if r.KeyringNote != nil {
		_, _ = fmt.Fprintf(out, "  %-11s%s\n", "note", *r.KeyringNote)
	}
	if r.ExpiresAt != nil {
		state := "valid"
		switch {
		case r.Type == serviceAccount, r.Type == externalAccount:
			state = "granted"
		case r.Expired:
			state = "expired — will refresh on next use"
		}
		_, _ = fmt.Fprintf(out, "  %-11s%s (expires %s)\n", "token", state, *r.ExpiresAt)
	}
	if r.Backend != nil && *r.Backend == goclilogin.BackendFile {
		_, _ = fmt.Fprintf(out, "  %-11s%s — no OS keyring on this host\n", "storage", *r.Backend)
	}
	if r.Type != serviceAccount && r.session() == goclilogin.SessionUnverified {
		_, _ = fmt.Fprintf(out, "  %-11sunverified — %s could not be reached\n", "session", r.Issuer)
	}
}
