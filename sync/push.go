package sync

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/datapointchris/projects/db/generated"
)

// executePush translates a pending sync operation to an HTTP request.
// Returns nil on success, or on server rejection (404/409) which means
// the operation should be dropped (server wins).
// Returns non-nil error on network/transient failures that should be retried.
func (e *Engine) executePush(op generated.PendingSync) error {
	opType := OpType(op.Operation)

	switch opType {
	case OpCreateProject:
		return e.pushJSON(http.MethodPost, "/projects", op.Payload)
	case OpUpdateProject:
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/projects/%s", op.EntityID), op.Payload)
	case OpDeleteProject:
		return e.pushDelete(fmt.Sprintf("/projects/%s", op.EntityID))
	case OpReorderProject:
		// The payload is {"position": n}, which a project's own PATCH takes.
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/projects/%s", op.EntityID), op.Payload)

	case OpCreateItem:
		return e.pushCreatedItem(op.EntityID, op.Payload)
	case OpUpdateItem:
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/project-items/%s", op.EntityID), op.Payload)
	case OpDeleteItem:
		return e.pushDelete(fmt.Sprintf("/project-items/%s", op.EntityID))

	case OpReorderItem:
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/project-items/%s/reorder", op.EntityID), op.Payload)

	case OpAddToProject:
		return e.pushJSON(http.MethodPost, fmt.Sprintf("/project-items/%s/projects", op.EntityID), op.Payload)
	case OpRemoveFromProject:
		var p struct {
			ProjectID string `json:"project_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushDelete(fmt.Sprintf("/project-items/%s/projects/%s", op.EntityID, p.ProjectID))

	case OpCreateTask:
		var p struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushJSON(http.MethodPost, fmt.Sprintf("/project-items/%s/tasks", p.ItemID), op.Payload)
	case OpUpdateTask:
		var p struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/project-items/%s/tasks/%s", p.ItemID, op.EntityID), op.Payload)
	case OpDeleteTask:
		var p struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushDelete(fmt.Sprintf("/project-items/%s/tasks/%s", p.ItemID, op.EntityID))
	case OpCompleteTask:
		var p struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushJSON(http.MethodPatch, fmt.Sprintf("/project-items/%s/tasks/%s", p.ItemID, op.EntityID), `{"completed":true}`)

	case OpAddDependency:
		return e.pushJSON(http.MethodPost, fmt.Sprintf("/project-items/%s/dependencies", op.EntityID), op.Payload)
	case OpRemoveDependency:
		var p struct {
			DependsOnID string `json:"depends_on_id"`
		}
		_ = json.Unmarshal([]byte(op.Payload), &p)
		return e.pushDelete(fmt.Sprintf("/project-items/%s/dependencies/%s", op.EntityID, p.DependsOnID))

	default:
		return fmt.Errorf("unknown sync operation: %s", op.Operation)
	}
}

// pushCreatedItem posts a queued item create and records the number the server
// assigned to it.
//
// The number is the item's handle, and until the server answers there is none —
// so without this the item would stay unnamed until the next pull, up to a full
// sync interval after the command that created it returned. Long enough that
// the thing you just filed cannot be referred to by the handle it already has.
func (e *Engine) pushCreatedItem(itemID, payload string) error {
	var created struct {
		Number *int `json:"number"`
	}
	if err := e.pushJSONInto(http.MethodPost, "/project-items", payload, &created); err != nil {
		return err
	}
	if created.Number == nil {
		return nil
	}
	return e.q.SetItemNumber(e.ctx, generated.SetItemNumberParams{
		Number: sql.NullInt64{Int64: int64(*created.Number), Valid: true},
		ID:     itemID,
	})
}

// pushJSON sends a JSON request and handles the response.
// 404/409 are treated as "drop this op" (returns nil).
// Network errors and 5xx are retryable (returns error).
func (e *Engine) pushJSON(method, path string, payload string) error {
	return e.pushJSONInto(method, path, payload, nil)
}

// pushJSONInto is pushJSON with the response decoded into out when the server
// accepted the request. A body that will not decode is not an error: the write
// itself landed, and failing here would re-queue a create the server has
// already applied. The missing field arrives on the next pull instead.
func (e *Engine) pushJSONInto(method, path string, payload string, out any) error {
	var body io.Reader
	if payload != "" {
		body = bytes.NewReader([]byte(payload))
	}

	req, err := http.NewRequestWithContext(e.ctx, method, e.base+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return friendlyNetErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, _ := io.ReadAll(resp.Body)

	if err := classifyResponse(resp.StatusCode, responseBody); err != nil {
		return err
	}
	if out != nil && resp.StatusCode < 400 {
		_ = json.Unmarshal(responseBody, out)
	}
	return nil
}

// pushDelete sends a DELETE request.
func (e *Engine) pushDelete(path string) error {
	req, err := http.NewRequestWithContext(e.ctx, http.MethodDelete, e.base+path, nil)
	if err != nil {
		return err
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return friendlyNetErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, _ := io.ReadAll(resp.Body)

	return classifyResponse(resp.StatusCode, responseBody)
}

// classifyResponse decides whether a status code means success, drop, or retry.
func classifyResponse(status int, body []byte) error {
	switch {
	case status < 400:
		return nil // success
	case status == http.StatusNotFound, status == http.StatusConflict:
		return nil // drop: server wins, next pull reconciles
	case status >= 500:
		return refusal("server error", status, body)
	default:
		return refusal("push rejected", status, body)
	}
}

// refusal names the status and the reason the API gave in its error body's
// `message`, or the body as it arrived when it carries none.
func refusal(prefix string, status int, body []byte) error {
	reason := strings.TrimSpace(string(body))
	var answer struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &answer) == nil && answer.Message != "" {
		reason = answer.Message
	}
	return fmt.Errorf("%s (%d %s): %s", prefix, status, http.StatusText(status), reason)
}

// friendlyNetErr translates raw network errors into user-readable messages.
//
// A token command's failure arrives through the same call, wrapped as a network
// error. Its own message says what to run, and its timeout is not the API's, so
// it is returned as it is.
func friendlyNetErr(err error) error {
	var tokenErr *TokenCommandError
	if errors.As(err, &tokenErr) {
		return tokenErr
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("cannot reach API server (connection refused)")
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("cannot reach API server (DNS lookup failed)")
	case strings.Contains(msg, "Timeout") || strings.Contains(msg, "deadline exceeded"):
		return fmt.Errorf("API server did not respond (timeout)")
	default:
		return fmt.Errorf("network error: %w", err)
	}
}
