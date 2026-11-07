// Package apiclient is a Go client of the projects API: one method per route, each
// answering in the api/wire types the API encodes. The transport decides where the
// routes run, over the network or inside this process, so the same calls reach the
// API and a database on this machine.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/projects/api/wire"
)

// prefix is where every route the client calls lives.
const prefix = "/api/v1"

// Client calls one API. Base is its address without the /api/v1 the routes add.
type Client struct {
	Base string
	http *http.Client
}

// New returns a client of the API at base, whose requests go through httpClient.
func New(base string, httpClient *http.Client) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), http: httpClient}
}

// APIError is an answer outside 2xx. Message is the API's own sentence, empty when
// the body carried none, as a proxy's error page does.
type APIError struct {
	StatusCode int
	Message    string
	Problems   []wire.ValidationError
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("the API answered %d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// NotFound reports a 404 the API explained, so the subject it names was looked for
// and is not there. A 404 with no sentence never reached a route.
func (e *APIError) NotFound() bool {
	return e.StatusCode == http.StatusNotFound && e.Message != ""
}

func (e *APIError) Unauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

func (e *APIError) Forbidden() bool { return e.StatusCode == http.StatusForbidden }

// call sends one request and decodes a 2xx answer into answer, when there is one.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body, answer any) error {
	target := c.Base + prefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var sent io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
		sent = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, sent)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	received, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("reading the answer to %s %s: %w", method, path, err)
	}
	if response.StatusCode >= http.StatusMultipleChoices {
		return refusalOf(response.StatusCode, received)
	}
	if answer == nil || len(received) == 0 {
		return nil
	}
	if err := json.Unmarshal(received, answer); err != nil {
		return fmt.Errorf("decoding the answer to %s %s: %w", method, path, err)
	}
	return nil
}

func refusalOf(status int, body []byte) *APIError {
	refused := &APIError{StatusCode: status}
	var answer wire.Error
	if json.Unmarshal(body, &answer) == nil {
		refused.Message = answer.Message
		refused.Problems = answer.Errors
	}
	return refused
}

// escaped is one path segment. A project name may hold a slash, which the API's
// wildcards take escaped.
func escaped(ref string) string { return url.PathEscape(ref) }

// ItemQuery narrows an item list. An empty Status leaves the API's default, open.
// A nil Repo is no filter, and an empty one asks for the untagged items.
type ItemQuery struct {
	Status       string
	Repo         *string
	Since, Until *time.Time
	Limit        *int
}

func (q ItemQuery) values() url.Values {
	values := url.Values{}
	if q.Status != "" {
		values.Set("status", q.Status)
	}
	if q.Repo != nil {
		values.Set("repo", *q.Repo)
	}
	if q.Since != nil {
		values.Set("since", q.Since.UTC().Format(time.RFC3339Nano))
	}
	if q.Until != nil {
		values.Set("until", q.Until.UTC().Format(time.RFC3339Nano))
	}
	if q.Limit != nil {
		values.Set("limit", strconv.Itoa(*q.Limit))
	}
	return values
}

// changes is a PATCH body read field by field: a key left out keeps, null clears.
type changes map[string]any

func (c changes) text(key string, value *string, clear bool) {
	switch {
	case clear:
		c[key] = nil
	case value != nil:
		c[key] = *value
	}
}

func (c changes) number(key string, value *int) {
	if value != nil {
		c[key] = *value
	}
}

func (c changes) flag(key string, value *bool) {
	if value != nil {
		c[key] = *value
	}
}
