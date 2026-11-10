package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/api/wire"
	"github.com/datapointchris/projects/model"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(body)
}

// statusPhrase is a status's standard phrase, which RFC 9110 words as Unprocessable
// Content where Go's text still says Entity.
func statusPhrase(status int) string {
	if status == http.StatusUnprocessableEntity {
		return "Unprocessable Content"
	}
	return http.StatusText(status)
}

func writeRefusal(w http.ResponseWriter, r *http.Request, status int, message string, problems []wire.ValidationError) {
	writeError(w, r, status, wire.Error{Message: message, Errors: problems})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, answer wire.Error) {
	requestlog.NoteRefusal(r.Context(), answer.Message)
	answer.Error = statusPhrase(status)
	writeJSON(w, status, answer)
}

// validationMessage is each refused field as `field: reason`, the field dotted below
// the part of the request it came in, in the one string a client prints.
func validationMessage(problems []wire.ValidationError) string {
	parts := make([]string, 0, len(problems))
	for _, problem := range problems {
		segments := make([]string, 0, len(problem.Loc))
		for i, segment := range problem.Loc {
			if i == 0 && slices.Contains([]any{"body", "query", "path"}, segment) {
				continue
			}
			segments = append(segments, fmt.Sprint(segment))
		}
		if len(segments) == 0 {
			parts = append(parts, problem.Msg)
			continue
		}
		parts = append(parts, strings.Join(segments, ".")+": "+problem.Msg)
	}
	return strings.Join(parts, "; ")
}

// refuseInvalid answers a 422 when any input was refused, and reports whether it did.
// An answer refusing a key the route does not read carries its code and names the
// first such key, so a client tells it from a value it sent wrong.
func refuseInvalid(w http.ResponseWriter, r *http.Request, problems ...[]wire.ValidationError) bool {
	all := slices.Concat(problems...)
	if len(all) == 0 {
		return false
	}
	answer := wire.Error{Message: validationMessage(all), Errors: all}
	for _, problem := range all {
		if problem.Type == typeUnknownField {
			answer.Code, answer.Field = codeUnknownField, fmt.Sprint(problem.Loc[len(problem.Loc)-1])
			break
		}
	}
	writeError(w, r, http.StatusUnprocessableEntity, answer)
	return true
}

// statusOfRefusal is what each rule's refusal answers.
var statusOfRefusal = []struct {
	kind   error
	status int
}{
	{model.ErrNotFound, http.StatusNotFound},

	{model.ErrDuplicateName, http.StatusConflict},
	{model.ErrDuplicateID, http.StatusConflict},
	{model.ErrAmbiguousName, http.StatusConflict},
	{model.ErrAlreadyInProject, http.StatusConflict},
	{model.ErrDuplicateDependency, http.StatusConflict},
	{model.ErrCyclicDependency, http.StatusConflict},
	{model.ErrLastProject, http.StatusConflict},
	{model.ErrIncompleteOrphans, http.StatusConflict},
	{model.ErrNothingToUndo, http.StatusConflict},
	{model.ErrUndoSuperseded, http.StatusConflict},

	{model.ErrUnknownKind, http.StatusUnprocessableEntity},
	{model.ErrUnknownStatus, http.StatusUnprocessableEntity},
	{model.ErrUnknownItemStatus, http.StatusUnprocessableEntity},
	{model.ErrInvalidReference, http.StatusUnprocessableEntity},
	{model.ErrReservedName, http.StatusUnprocessableEntity},
	{model.ErrDropReasonRequired, http.StatusUnprocessableEntity},
	{model.ErrSelfDependency, http.StatusUnprocessableEntity},
	{model.ErrInvertedWindow, http.StatusUnprocessableEntity},
	{model.ErrNoProjects, http.StatusUnprocessableEntity},
	{model.ErrRepoNamedProject, http.StatusUnprocessableEntity},

	{model.ErrIncompleteTasks, http.StatusBadRequest},
}

// fail answers what a rule returned. A refusal says its own words, and one naming a
// field marks it the way a validation error does. A refusal whose kind has no row is
// still the caller's to fix, so it answers as one and logs the missing row. A request
// that ran out of time answers 503, and so does a network error, because a route
// reaches nothing over a network but the database. Anything else is a fault of this
// server, and its answer names the request whose log line holds the detail.
func (t *table) fail(w http.ResponseWriter, r *http.Request, err error) {
	for _, known := range statusOfRefusal {
		if errors.Is(err, known.kind) {
			answerRefusal(w, r, known.status, err)
			return
		}
	}
	var refusal *model.Refusal
	var unreachable net.Error
	switch {
	case errors.As(err, &refusal):
		t.logFailure(r, "a refusal's kind has no row in statusOfRefusal", err)
		answerRefusal(w, r, http.StatusUnprocessableEntity, err)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		t.logFailure(r, "request ran out of time", err)
		writeRefusal(w, r, http.StatusServiceUnavailable, "The database did not answer in time.", nil)
	case errors.As(err, &unreachable):
		t.logFailure(r, "database unreachable", err)
		writeRefusal(w, r, http.StatusServiceUnavailable, "The database could not be reached.", nil)
	default:
		t.logFailure(r, "request failed", err)
		writeRefusal(w, r, http.StatusInternalServerError, "Internal server error in request "+requestlog.RequestID(r.Context()), nil)
	}
}

func answerRefusal(w http.ResponseWriter, r *http.Request, status int, err error) {
	var refusal *model.Refusal
	if errors.As(err, &refusal) && refusal.Field != "" {
		problem := wire.ValidationError{
			Type: "value_error", Loc: []any{"body", refusal.Field}, Msg: "Value error, " + refusal.Message,
			Input: refusal.Input, Ctx: map[string]any{"error": map[string]any{}},
		}
		refuseInvalid(w, r, []wire.ValidationError{problem})
		return
	}
	writeRefusal(w, r, status, err.Error(), nil)
}

func (t *table) logFailure(r *http.Request, message string, err error) {
	t.log.LogAttrs(r.Context(), slog.LevelError, message,
		slog.String("err", err.Error()),
		slog.String("method", r.Method),
		slog.String("path", r.URL.EscapedPath()),
		slog.String("request_id", requestlog.RequestID(r.Context())),
	)
}
