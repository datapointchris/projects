package apiclient

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

// InProcess serves each request with handler inside this process. A command then
// runs the API's own routes, refusals and JSON against a database on this
// machine, and nothing listens on a socket.
func InProcess(handler http.Handler) http.RoundTripper {
	return inProcess{handler: handler}
}

type inProcess struct{ handler http.Handler }

func (t inProcess) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		defer func() { _ = request.Body.Close() }()
	}
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// Attempts is how many times a request that got no answer is sent in all.
const Attempts = 3

const longestPause = 20 * time.Second

// Retrying tries a request again where the API gave no answer, as the AWS CLI's
// standard mode does. A read retries any failure to reach the API, an answer cut
// short, and a 429, 502, 503 or 504. A write retries only a failure before it was
// sent, because a retry after that could do it twice.
func Retrying(next http.RoundTripper) http.RoundTripper {
	return retrying{next: next, pause: randomPause}
}

type retrying struct {
	next  http.RoundTripper
	pause func(attempt int, response *http.Response) time.Duration
}

func (t retrying) RoundTrip(request *http.Request) (*http.Response, error) {
	sent := false
	for attempt := 1; ; attempt++ {
		response, err := t.next.RoundTrip(request)
		if err == nil {
			if err = readWhole(response); err != nil {
				response = nil
			}
		}
		sent = sent || err == nil || !neverSent(err)
		if attempt == Attempts || !worthRetrying(request.Method, response, err) {
			if err != nil {
				err = &Unanswered{Method: request.Method, Attempts: attempt, Sent: sent, Err: err}
			}
			return response, err
		}
		pause := t.pause(attempt, response)
		if response != nil {
			_ = response.Body.Close()
		}
		select {
		case <-time.After(pause):
		case <-request.Context().Done():
			return nil, &Unanswered{Method: request.Method, Attempts: attempt, Sent: sent, Err: request.Context().Err()}
		}
		request = request.Clone(request.Context())
		if request.GetBody != nil {
			body, bodyErr := request.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			request.Body = body
		}
	}
}

// Unanswered is a request that got no answer. Attempts counts the tries, and is 0
// where nothing counted them. Sent is whether any try may have reached the API, so
// a write that was sent may have been carried out.
type Unanswered struct {
	Method   string
	Attempts int
	Sent     bool
	Err      error
}

func (u *Unanswered) Error() string {
	if u.Attempts > 1 {
		return fmt.Sprintf("%v, after %d attempts", u.Err, u.Attempts)
	}
	return u.Err.Error()
}

func (u *Unanswered) Unwrap() error { return u.Err }

// Read is whether the request only read, so sending it again changes nothing.
func (u *Unanswered) Read() bool { return u.Method == http.MethodGet }

// answerCutShort is a body that broke after its status arrived.
type answerCutShort struct{ error }

func (c answerCutShort) Unwrap() error { return c.error }

func readWhole(response *http.Response) error {
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return answerCutShort{fmt.Errorf("reading the answer: %w", err)}
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}

func worthRetrying(method string, response *http.Response, err error) bool {
	read := method == http.MethodGet
	if err != nil {
		var cut answerCutShort
		return neverSent(err) || (read && (unreachable(err) || errors.As(err, &cut)))
	}
	if !read {
		return false
	}
	switch response.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// neverSent is a name that did not resolve or a connection that did not open.
func neverSent(err error) bool {
	var lookup *net.DNSError
	var op *net.OpError
	return errors.As(err, &lookup) || (errors.As(err, &op) && op.Op == "dial")
}

// unreachable is a reset, a timeout or a connection cut before the whole answer.
func unreachable(err error) bool {
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// randomPause waits what the server's Retry-After says, or else a random fraction
// of 2^(attempt-1) seconds.
func randomPause(attempt int, response *http.Response) time.Duration {
	if response != nil {
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, longestPause)
		}
	}
	window := time.Second << (attempt - 1)
	return min(time.Duration(rand.Int64N(int64(window))), longestPause)
}
