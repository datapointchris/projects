// Package requestlog writes one JSON line per request and gives each request its
// id. It wraps everything else, so a refusal from authentication is logged too.
package requestlog

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// HeaderRequestID carries a request's id in and back out, so a client's own id
// traces through this API's logs and its audit trail.
const HeaderRequestID = "X-Request-ID"

// HeaderTraceparent carries the W3C trace a request belongs to, which its log line
// and its audit rows keep, so either joins back to the trace.
const HeaderTraceparent = "traceparent"

// record is what inner layers learn about a request that the final line reports.
type record struct {
	id       string
	actor    string
	clientID string
	service  bool
	refusal  string
}

type recordKey struct{}

func recordFrom(ctx context.Context) *record {
	found, _ := ctx.Value(recordKey{}).(*record)
	return found
}

// RequestID answers the id of the request ctx belongs to.
func RequestID(ctx context.Context) string {
	if found := recordFrom(ctx); found != nil {
		return found.id
	}
	return ""
}

// NoteCaller adds who is calling to the request's line.
func NoteCaller(ctx context.Context, actor, clientID string, service bool) {
	if found := recordFrom(ctx); found != nil {
		found.actor, found.clientID, found.service = actor, clientID, service
	}
}

// NoteRefusal adds why a request was refused to its line.
func NoteRefusal(ctx context.Context, message string) {
	if found := recordFrom(ctx); found != nil {
		found.refusal = message
	}
}

// statusRecorder remembers what a handler answered, because http.ResponseWriter
// cannot be asked.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(body []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	written, err := s.ResponseWriter.Write(body)
	s.bytes += written
	return written, err
}

// Middleware logs every request but the probes, which a cluster sends every few
// seconds and which say nothing about use.
func Middleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(HeaderRequestID, id)

		current := &record{id: id}
		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), recordKey{}, current)))

		if r.URL.Path == "/health" || r.URL.Path == "/ready" {
			return
		}
		log.LogAttrs(r.Context(), levelOf(recorder.status), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.EscapedPath()),
			slog.String("query", r.URL.RawQuery),
			slog.Int("status", recorder.status),
			slog.Int("bytes", recorder.bytes),
			slog.Float64("duration_ms", float64(time.Since(started).Microseconds())/1000),
			slog.String("request_id", id),
			slog.String("traceparent", r.Header.Get(HeaderTraceparent)),
			slog.String("actor", current.actor),
			slog.String("client_id", current.clientID),
			slog.Bool("service", current.service),
			slog.String("refusal", current.refusal),
			slog.String("user_agent", r.UserAgent()),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}

func levelOf(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
