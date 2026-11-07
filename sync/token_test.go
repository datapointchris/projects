package sync_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	goSync "sync"
	"testing"

	"github.com/datapointchris/projects/sync"
)

// seenRequest is one request as the API received it.
type seenRequest struct {
	Authorization, Method, Path, Body string
}

// bearerAPI answers a pull with empty lists for a request whose Authorization
// header is the accepted one, and 401 for any other. It records every request.
type bearerAPI struct {
	*httptest.Server
	mu       goSync.Mutex
	accepted string
	seen     []seenRequest
}

func newBearerAPI(t *testing.T, accepted string) *bearerAPI {
	t.Helper()
	api := &bearerAPI{accepted: accepted}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		authorization := r.Header.Get("Authorization")
		api.seen = append(api.seen, seenRequest{authorization, r.Method, r.URL.Path, string(body)})
		ok := authorization == api.accepted
		api.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(api.Close)
	return api
}

func (a *bearerAPI) accept(authorization string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accepted = authorization
}

func (a *bearerAPI) requests() []seenRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]seenRequest(nil), a.seen...)
}

// writeTokenScript writes body as an executable shell script and returns its
// path, which holds no space, so it survives being split into a command.
func writeTokenScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "print-token")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("writing token script: %v", err)
	}
	return path
}

// tokenCommand returns a command printing tokens[n-1] on its nth run, and the
// last token on every run after, with a count of its runs so far.
func tokenCommand(t *testing.T, tokens ...string) (string, func() int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tokens"), []byte(strings.Join(tokens, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing tokens: %v", err)
	}
	script := writeTokenScript(t, dir, `dir=$(dirname "$0")
runs=$(( $(cat "$dir/runs" 2>/dev/null || echo 0) + 1 ))
echo "$runs" > "$dir/runs"
total=$(wc -l < "$dir/tokens")
[ "$runs" -gt "$total" ] && line=$total || line=$runs
sed -n "${line}p" "$dir/tokens"
`)
	runs := func() int {
		data, err := os.ReadFile(filepath.Join(dir, "runs"))
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return n
	}
	return script, runs
}

func mustPull(t *testing.T, engine *sync.Engine) {
	t.Helper()
	if err := engine.Pull(t.Context()); err != nil {
		t.Fatalf("pull: %v", err)
	}
}

// A pull is several requests, and the command runs for the first alone.
func TestEveryRequestCarriesTheTokenTheCommandPrints(t *testing.T) {
	command, runs := tokenCommand(t, "tok-1")
	api := newBearerAPI(t, "Bearer tok-1")
	engine := sync.New(openTestDB(t), api.URL, command)

	mustPull(t, engine)
	mustPull(t, engine)

	seen := api.requests()
	if len(seen) < 4 {
		t.Fatalf("two pulls made %d requests, want at least 4", len(seen))
	}
	for _, r := range seen {
		if r.Authorization != "Bearer tok-1" {
			t.Errorf("%s %s carried %q, want the printed token", r.Method, r.Path, r.Authorization)
		}
	}
	if got := runs(); got != 1 {
		t.Errorf("the command ran %d times for %d requests, want once", got, len(seen))
	}
}

// An access token expires while the TUI is open. The next request is refused,
// and the command runs once more for a token the API takes.
func TestAnExpiredTokenIsReplacedAndTheRequestSentAgain(t *testing.T) {
	command, runs := tokenCommand(t, "tok-1", "tok-2")
	api := newBearerAPI(t, "Bearer tok-1")
	engine := sync.New(openTestDB(t), api.URL, command)
	mustPull(t, engine)

	api.accept("Bearer tok-2")
	mustPull(t, engine)

	if got := runs(); got != 2 {
		t.Errorf("the command ran %d times, want 2", got)
	}
	seen := api.requests()
	if last := seen[len(seen)-1]; last.Authorization != "Bearer tok-2" {
		t.Errorf("the last request carried %q, want the replacement", last.Authorization)
	}
}

// Running the command again for a token it printed a moment ago would print the
// same refusal, so the refusal is the answer.
func TestATokenRefusedAsSoonAsItIsPrintedIsTheAnswer(t *testing.T) {
	command, runs := tokenCommand(t, "tok-1", "tok-2")
	api := newBearerAPI(t, "Bearer nothing-it-prints")

	if err := sync.New(openTestDB(t), api.URL, command).Pull(t.Context()); err == nil {
		t.Fatal("a pull whose every request is refused succeeded")
	}
	if got := runs(); got != 1 {
		t.Errorf("the command ran %d times, want once", got)
	}
}

func TestARefusedReplacementIsTheAnswer(t *testing.T) {
	command, runs := tokenCommand(t, "tok-1", "tok-2", "tok-3")
	api := newBearerAPI(t, "Bearer tok-1")
	engine := sync.New(openTestDB(t), api.URL, command)
	mustPull(t, engine)

	api.accept("Bearer nothing-it-prints")
	if err := engine.Pull(t.Context()); err == nil {
		t.Fatal("a pull whose every request is refused succeeded")
	}
	if got := runs(); got != 2 {
		t.Errorf("the command ran %d times, want 2: the first token and one replacement", got)
	}
}

// A create refused for an expired token has not reached the server, so it is
// sent again whole.
func TestAPushRefusedForAnExpiredTokenIsResentWithItsBody(t *testing.T) {
	command, _ := tokenCommand(t, "tok-1", "tok-2")
	api := newBearerAPI(t, "Bearer tok-1")
	engine := sync.New(openTestDB(t), api.URL, command)
	mustPull(t, engine)

	api.accept("Bearer tok-2")
	if err := engine.QueueOp(sync.OpCreateProject, "proj-1", map[string]string{"id": "proj-1", "name": "Boxes"}); err != nil {
		t.Fatalf("queueing a create: %v", err)
	}
	mustPull(t, engine)

	var posts []seenRequest
	for _, r := range api.requests() {
		if r.Method == http.MethodPost && r.Path == "/api/v1/projects" {
			posts = append(posts, r)
		}
	}
	if len(posts) != 2 {
		t.Fatalf("the create was posted %d times, want 2: refused, then resent", len(posts))
	}
	if posts[1].Authorization != "Bearer tok-2" {
		t.Errorf("the resend carried %q, want the replacement token", posts[1].Authorization)
	}
	if posts[1].Body == "" || posts[1].Body != posts[0].Body {
		t.Errorf("the resend's body %q, want the original %q", posts[1].Body, posts[0].Body)
	}
}

// A CLI whose login has lapsed says so on stderr, and that line is what the
// status bar needs, rather than a network error.
func TestAFailingCommandSaysWhatItPrinted(t *testing.T) {
	script := writeTokenScript(t, t.TempDir(), "echo 'Not logged in. Run the login command.' >&2\nexit 1\n")
	api := newBearerAPI(t, "Bearer tok-1")

	err := sync.New(openTestDB(t), api.URL, script).Pull(t.Context())

	var tokenErr *sync.TokenCommandError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("pull error %v, want a TokenCommandError", err)
	}
	if tokenErr.Reason != "Not logged in. Run the login command." {
		t.Errorf("reason %q, want the command's stderr", tokenErr.Reason)
	}
	if strings.Contains(err.Error(), "network error") {
		t.Errorf("pull error %q calls the command's failure a network error", err)
	}
	if seen := api.requests(); len(seen) != 0 {
		t.Errorf("%d requests reached the API without a token", len(seen))
	}
}

func TestACommandPrintingNothingIsAFailure(t *testing.T) {
	script := writeTokenScript(t, t.TempDir(), "exit 0\n")
	api := newBearerAPI(t, "Bearer tok-1")

	err := sync.New(openTestDB(t), api.URL, script).Pull(t.Context())

	var tokenErr *sync.TokenCommandError
	if !errors.As(err, &tokenErr) || tokenErr.Reason != "printed no token" {
		t.Fatalf("pull error %v, want a TokenCommandError saying it printed no token", err)
	}
}

// A dev API sets its own identity and needs no header.
func TestNoTokenCommandSendsNoAuthorization(t *testing.T) {
	api := newBearerAPI(t, "")
	mustPull(t, sync.New(openTestDB(t), api.URL, ""))

	for _, r := range api.requests() {
		if r.Authorization != "" {
			t.Errorf("%s %s carried %q, want no Authorization header", r.Method, r.Path, r.Authorization)
		}
	}
}
