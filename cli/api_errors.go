package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"
	"golang.org/x/oauth2"

	"github.com/datapointchris/projects/apiclient"
)

// noAnswerError is a failure a later run of the same command can pass without
// doing anything twice: a read that got no answer, a 429 or a 5xx, and a write
// that never reached the API or got a 429. It exits 4.
type noAnswerError struct{ error }

func (n noAnswerError) Unwrap() error { return n.error }

// explain words an error from a request for the person reading it. A credential
// failure names its own remedy, a service is never sent to a login it cannot
// complete, and a refusal is the API's sentence alone. A write the API may have
// carried out says so and exits 1, so nothing told to retry on 4 repeats it.
func (s *dataSource) explain(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errNeedsLogin) {
		return errors.New("not logged in — run `projects auth login`")
	}
	var refused *apiclient.APIError
	isRefusal := errors.As(err, &refused)
	if s.deployment != nil {
		if worded := s.credentialFailure(err, refused); worded != nil {
			return worded
		}
	}
	var noToken tokenFailure
	if s.deployment != nil && errors.As(err, &noToken) {
		return s.noToken(noToken.error)
	}
	if isRefusal && refused.StatusCode == http.StatusUnprocessableEntity && refused.Message != "" {
		return goclikit.UsageError(refused)
	}
	if s.deployment == nil {
		return err
	}
	if isRefusal && (refused.StatusCode == http.StatusTooManyRequests || refused.StatusCode >= http.StatusInternalServerError) {
		answered := s.answeredWith(refused)
		if refused.Read() || refused.StatusCode == http.StatusTooManyRequests {
			return noAnswerError{answered}
		}
		return mayHaveBeenCarriedOut(refused.Method, answered)
	}
	var lost *apiclient.Unanswered
	if errors.As(err, &lost) {
		if lost.Read() || !lost.Sent {
			return noAnswerError{s.unanswered(lost)}
		}
		return mayHaveBeenCarriedOut(lost.Method, s.unanswered(lost))
	}
	// A transport error nothing counted, such as the client's own deadline running
	// out, is taken to have been sent.
	var transport *url.Error
	if errors.As(err, &transport) {
		if strings.EqualFold(transport.Op, http.MethodGet) {
			return noAnswerError{s.unanswered(transport.Err)}
		}
		return mayHaveBeenCarriedOut(strings.ToUpper(transport.Op), s.unanswered(transport.Err))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return noAnswerError{s.unanswered(err)}
	}
	return err
}

// mayHaveBeenCarriedOut is a write that reached the API with no answer saying
// whether it landed. Running it again could do it twice, so it exits 1.
func mayHaveBeenCarriedOut(method string, cause error) error {
	return fmt.Errorf("%w; the %s was sent, so it may have been carried out — check before running it again", cause, method)
}

// noToken is a token this run could not get, so no request was sent. A provider
// that did not answer is a failure a later run can pass.
func (s *dataSource) noToken(cause error) error {
	from := "the login provider at " + s.deployment.Issuer.Value
	if s.deployment.HandsTokens() {
		from = "token_command in " + homeAsTilde(s.deployment.Path)
	}
	failed := fmt.Errorf("no token from %s: %w", from, cause)
	var network net.Error
	var provider *oauth2.RetrieveError
	switch {
	case errors.As(cause, &network), errors.Is(cause, context.DeadlineExceeded):
		return noAnswerError{failed}
	case errors.As(cause, &provider) && provider.Response != nil &&
		(provider.Response.StatusCode == http.StatusTooManyRequests || provider.Response.StatusCode >= http.StatusInternalServerError):
		return noAnswerError{failed}
	}
	return failed
}

// credentialFailure words a failure to authenticate, which the request never got
// past, or nil when err is not one.
func (s *dataSource) credentialFailure(err error, refused *apiclient.APIError) error {
	d := *s.deployment
	unauthorized := refused != nil && refused.Unauthorized()
	forbidden := refused != nil && refused.Forbidden() && refused.Message != ""
	if d.HandsTokens() {
		var failed *goclilogin.TokenCommandError
		switch {
		case errors.As(err, &failed):
			return fmt.Errorf("token_command in %s handed no token: it must print an RFC 6749 § 5.1 JSON token response, so run %q yourself to see what it prints: %w",
				homeAsTilde(d.Path), d.TokenCommand, failed)
		case unauthorized:
			return fmt.Errorf("%s rejected the token %q handed", d.API.Value, d.TokenCommand)
		case forbidden:
			return fmt.Errorf("the token %q handed: %s", d.TokenCommand, refused.Message)
		}
		return nil
	}
	if isService() {
		switch {
		case goclilogin.IsSessionRejected(err):
			return fmt.Errorf("%s refused service client %s — check client_id in %s, and %s",
				d.Issuer.Value, d.ClientID.Value, homeAsTilde(d.Path), clientSecretEnv)
		case unauthorized:
			return fmt.Errorf("%s rejected the token issued to service client %s", d.API.Value, d.ClientID.Value)
		case forbidden:
			return fmt.Errorf("service client %s: %s", d.ClientID.Value, refused.Message)
		}
		return nil
	}
	switch {
	case goclilogin.IsSessionRejected(err):
		return errors.New("session expired — run `projects auth login`")
	case unauthorized:
		return errors.New("session rejected by the API — run `projects auth login` to log in again")
	case forbidden:
		return errors.New(refused.Message)
	}
	return nil
}

// unanswered names the API and what chose it, as the first thing a person checks.
func (s *dataSource) unanswered(cause error) error {
	api := s.deployment.API
	return fmt.Errorf("no answer from the API at %s (%s): %w", api.Value, chosenBy(api), cause)
}

// answeredWith is a 429 or 5xx, which names the API the way unanswered does.
func (s *dataSource) answeredWith(refused *apiclient.APIError) error {
	api := s.deployment.API
	answered := fmt.Sprintf("the API at %s (%s) answered %d %s", api.Value, chosenBy(api),
		refused.StatusCode, http.StatusText(refused.StatusCode))
	if refused.Message != "" {
		answered += ": " + refused.Message
	}
	return errors.New(answered)
}

func chosenBy(api goclilogin.Setting) string {
	if api.Source == goclilogin.SourceFile {
		return "api in " + homeAsTilde(api.From)
	}
	return "--api"
}

// notFound is the API's sentence for a 404 it explained, which goclikit follows
// with the commands that find a real name or number.
func notFound(err error) (string, bool) {
	var refused *apiclient.APIError
	if !errors.As(err, &refused) || !refused.NotFound() {
		return "", false
	}
	return refused.Message, true
}
