package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"

	"github.com/datapointchris/projects/apiclient"
)

// noAnswerError is a request the API never judged: it was not reached, timed out,
// or answered 429 or 5xx. It exits 4, because the same command later can succeed.
type noAnswerError struct{ error }

func (n noAnswerError) Unwrap() error { return n.error }

// explain words an error from a request for the person reading it. A credential
// failure names its own remedy, a service is never sent to a login it cannot
// complete, and a refusal is the API's sentence alone.
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
	switch {
	case isRefusal && refused.StatusCode == http.StatusUnprocessableEntity && refused.Message != "":
		return goclikit.UsageError(refused)
	case isRefusal && (refused.StatusCode == http.StatusTooManyRequests || refused.StatusCode >= http.StatusInternalServerError):
		return noAnswerError{s.unanswered(refused)}
	case errors.Is(err, context.DeadlineExceeded):
		return noAnswerError{s.unanswered(err)}
	}
	var transport *url.Error
	if errors.As(err, &transport) {
		return noAnswerError{s.unanswered(transport.Err)}
	}
	return err
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
	if s.deployment == nil {
		return cause
	}
	api := s.deployment.API
	chosenBy := "--api"
	if api.Source == goclilogin.SourceFile {
		chosenBy = "api in " + homeAsTilde(api.From)
	}
	return fmt.Errorf("no answer from the API at %s (%s): %w, %d attempts", api.Value, chosenBy, cause, apiclient.Attempts)
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
