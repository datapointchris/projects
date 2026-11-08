package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/datapointchris/goclilogin"

	"github.com/datapointchris/projects/config"
)

// product names this tool to goclilogin: its config file, keyring service and
// state directory.
const product = "projects"

// keyringService namespaces this tool's entries in the OS keychain.
const keyringService = product

// itemsReadScope is what a service client asks for. The API's ServiceScopes
// lists the routes it reaches, and an identity provider refuses the whole token
// request when it names a scope the client does not hold.
const itemsReadScope = "projects.items.read"

// clientSecretEnv selects the client-credentials grant for the client the config
// file names. It is read from the environment alone, so it is never written to
// the file and no command prints it.
const clientSecretEnv = "PROJECTS_CLIENT_SECRET"

// loadDeployment reads the API every command talks to, from the config file's
// api, issuer, client_id and token_command. api is the --api flag's value, and
// outranks the file's when it is not empty.
//
// A service missing a value is told which key to write rather than to log in,
// since a service has no login.
func loadDeployment(ctx context.Context, api string) (goclilogin.Deployment, error) {
	if err := config.RefuseMissingNamedFile(); err != nil {
		return goclilogin.Deployment{Path: configFilePath()}, err
	}
	d, err := goclilogin.LoadDeploymentFrom(ctx, product, configFilePath(), api)
	if d.HandsTokens() && isService() {
		return d, fmt.Errorf("%s sets token_command and %s is set, so projects has two credentials; keep one",
			d.Path, clientSecretEnv)
	}
	if !isService() || !errors.Is(err, goclilogin.ErrNotConfigured) {
		return d, err
	}
	var missing []string
	for _, key := range []struct {
		name string
		set  goclilogin.Setting
	}{{"api", d.API}, {"issuer", d.Issuer}, {"client_id", d.ClientID}} {
		if key.set.Value == "" {
			missing = append(missing, key.name)
		}
	}
	return d, fmt.Errorf("%w: %s sets no %s; a service names its API, provider and client there, and only %s in its environment",
		goclilogin.ErrNotConfigured, d.Path, strings.Join(missing, " or "), clientSecretEnv)
}

// deploymentForLogin is the deployment `projects auth login` logs in to: the
// file's values, with whatever it lacks filled in from api, the API's RFC 9728
// metadata and clientID. A $PROJECTS_CONFIG naming no file yet is where login
// creates it.
func deploymentForLogin(ctx context.Context, api, clientID string) (goclilogin.Deployment, error) {
	return goclilogin.DeploymentForLoginFrom(ctx, product, configFilePath(), api, clientID)
}

// configFilePath is the file every command reads and login writes, whether or
// not it exists.
func configFilePath() string { return config.FilePath() }

// defaultClientID is the client id login uses when neither the file nor
// --client-id names one: projects-cli-<short hostname>.
func defaultClientID() string { return goclilogin.ClientID(product) }

// loginConfig is the goclilogin view of a deployment: the provider, the client, and
// where the token and its refresh lock live. The state directory is named
// rather than left to goclilogin's default, so the lock sits beside the tool's
// other state whatever the keyring service is called.
func loginConfig(d goclilogin.Deployment) goclilogin.Config {
	return goclilogin.Config{
		Issuer:         d.Issuer.Value,
		ClientID:       d.ClientID.Value,
		KeyringService: keyringService,
		StateDir:       goclilogin.StateDir(product),
	}
}

// clientSecret is PROJECTS_CLIENT_SECRET, set only where a service runs this CLI
// with nobody present to approve a device login.
func clientSecret() string { return os.Getenv(clientSecretEnv) }

// isService reports whether the CLI authenticates as a confidential service
// client rather than as the person who logged in on this machine.
func isService() bool { return clientSecret() != "" }

// serviceClient is the goclilogin view of a deployment for the client-credentials
// grant. Nothing it obtains is stored, so it names no keyring or state
// directory.
func serviceClient(d goclilogin.Deployment) goclilogin.ServiceClient {
	return goclilogin.ServiceClient{Issuer: d.Issuer.Value, ClientID: d.ClientID.Value, Scopes: []string{itemsReadScope}}
}
