package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/datapointchris/projects/api/wire"
)

const (
	messageFieldRequired = "Field required"
	messageNotAString    = "Input should be a valid string"
	messageNotAnInteger  = "Input should be a valid integer"
	messageNotABoolean   = "Input should be a valid boolean"
	messageNotAList      = "Input should be a valid list"
	messageNotAUUID      = "Input should be a valid UUID"
	messageNotAnObject   = "Input should be a valid dictionary or object to extract fields from"
	messageUnparsedInt   = "Input should be a valid integer, unable to parse string as an integer"
	messageNegativeLimit = "Input should be greater than or equal to 0"
	messageNoOffset      = "Input should have timezone info"
	messageNotAnInstant  = "Input should be a valid datetime or date"
	messageCannotBeNull  = "Value error, cannot be null; omit the field to leave it unchanged"
)

// requestBody reads a JSON object field by field. A partial update has to tell a
// field left out from a field sent as null, which no struct's zero value can, so a
// handler asks for each field in the terms its contract states it. Every refusal
// collects in problems, so one answer names each refused field at once.
type requestBody struct {
	fields   map[string]json.RawMessage
	whole    any
	problems []wire.ValidationError
}

func readRequestBody(r *http.Request) *requestBody {
	body := &requestBody{}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		body.refuse("json_invalid", "JSON decode error", nil, "body")
		return body
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		body.refuse("missing", messageFieldRequired, nil, "body")
		return body
	}
	if err := json.Unmarshal(raw, &body.whole); err != nil {
		var position int64
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			position = syntax.Offset
		}
		body.problems = append(body.problems, wire.ValidationError{
			Type: "json_invalid", Loc: []any{"body", position}, Msg: "JSON decode error",
			Input: map[string]any{}, Ctx: map[string]any{"error": err.Error()},
		})
		return body
	}
	if _, isObject := body.whole.(map[string]any); !isObject {
		body.refuse("model_attributes_type", messageNotAnObject, body.whole, "body")
		return body
	}
	if err := json.Unmarshal(raw, &body.fields); err != nil {
		body.refuse("model_attributes_type", messageNotAnObject, body.whole, "body")
	}
	return body
}

func (b *requestBody) refuse(kind, message string, input any, loc ...any) {
	b.problems = append(b.problems, wire.ValidationError{Type: kind, Loc: loc, Msg: message, Input: input})
}

func (b *requestBody) refuseField(field, kind, message string, raw json.RawMessage) {
	var input any
	_ = json.Unmarshal(raw, &input)
	b.refuse(kind, message, input, "body", field)
}

// refuseMissing says nothing about a body that could not be read at all, which is
// refused whole already.
func (b *requestBody) refuseMissing(field string) {
	if b.fields == nil {
		return
	}
	b.refuse("missing", messageFieldRequired, b.whole, "body", field)
}

func (b *requestBody) refuseNull(field string) {
	b.problems = append(b.problems, wire.ValidationError{
		Type: "value_error", Loc: []any{"body", field}, Msg: messageCannotBeNull,
		Ctx: map[string]any{"error": map[string]any{}},
	})
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeField holds a value of T or refuses the field. A JSON null is never a T
// here, because each caller decides what null means before asking.
func decodeField[T any](b *requestBody, field, kind, message string, raw json.RawMessage) *T {
	var value T
	if isJSONNull(raw) || json.Unmarshal(raw, &value) != nil {
		b.refuseField(field, kind, message, raw)
		return nil
	}
	return &value
}

func stringField(b *requestBody, field string, raw json.RawMessage) *string {
	return decodeField[string](b, field, "string_type", messageNotAString, raw)
}

// requiredString refuses a field left out or null.
func (b *requestBody) requiredString(field string) string {
	raw, sent := b.fields[field]
	if !sent {
		b.refuseMissing(field)
		return ""
	}
	if value := stringField(b, field, raw); value != nil {
		return *value
	}
	return ""
}

// stringUnlessLeftOut answers nil for a field left out, and refuses null.
func (b *requestBody) stringUnlessLeftOut(field string) *string {
	raw, sent := b.fields[field]
	if !sent {
		return nil
	}
	return stringField(b, field, raw)
}

// nullableString answers nil for a field left out or null.
func (b *requestBody) nullableString(field string) *string {
	value, _ := b.clearableString(field)
	return value
}

// clearableString tells a field left out from one sent as null, which clears it.
func (b *requestBody) clearableString(field string) (value *string, cleared bool) {
	raw, sent := b.fields[field]
	if !sent {
		return nil, false
	}
	if isJSONNull(raw) {
		return nil, true
	}
	return stringField(b, field, raw), false
}

// partial answers nil for a field left out, and refuses null. It reads a partial
// update's field over a column that cannot hold null.
func partial[T any](b *requestBody, field, kind, message string) *T {
	raw, sent := b.fields[field]
	if !sent {
		return nil
	}
	if isJSONNull(raw) {
		b.refuseNull(field)
		return nil
	}
	return decodeField[T](b, field, kind, message, raw)
}

func (b *requestBody) partialString(field string) *string {
	return partial[string](b, field, "string_type", messageNotAString)
}

func (b *requestBody) partialBool(field string) *bool {
	return partial[bool](b, field, "bool_type", messageNotABoolean)
}

func (b *requestBody) partialInt(field string) *int {
	return partial[int](b, field, "int_type", messageNotAnInteger)
}

// intOr answers fallback for a field left out, and refuses null.
func (b *requestBody) intOr(field string, fallback int) int {
	raw, sent := b.fields[field]
	if !sent {
		return fallback
	}
	if value := decodeField[int](b, field, "int_type", messageNotAnInteger, raw); value != nil {
		return *value
	}
	return fallback
}

func (b *requestBody) requiredInt(field string) int {
	raw, sent := b.fields[field]
	if !sent {
		b.refuseMissing(field)
		return 0
	}
	if value := decodeField[int](b, field, "int_type", messageNotAnInteger, raw); value != nil {
		return *value
	}
	return 0
}

// optionalUUID is an id a caller chose, which may be left out or null.
func (b *requestBody) optionalUUID(field string) *string {
	raw, sent := b.fields[field]
	if !sent || isJSONNull(raw) {
		return nil
	}
	value := decodeField[string](b, field, "uuid_type", "UUID input should be a string, bytes or UUID object", raw)
	if value == nil {
		return nil
	}
	if _, err := uuid.Parse(*value); err != nil {
		b.refuseField(field, "uuid_parsing", messageNotAUUID, raw)
		return nil
	}
	return value
}

// requiredItemReference is an item's UUID or number. The number may come as a JSON
// number or as text, because a CLI forwards what a person typed.
func (b *requestBody) requiredItemReference(field string) string {
	raw, sent := b.fields[field]
	if !sent {
		b.refuseMissing(field)
		return ""
	}
	var number int
	if !isJSONNull(raw) && json.Unmarshal(raw, &number) == nil {
		return strconv.Itoa(number)
	}
	if value := stringField(b, field, raw); value != nil {
		return *value
	}
	return ""
}

// requiredStringList refuses a field left out, null, or holding anything but strings.
func (b *requestBody) requiredStringList(field string) []string {
	raw, sent := b.fields[field]
	if !sent {
		b.refuseMissing(field)
		return nil
	}
	var elements []json.RawMessage
	if isJSONNull(raw) || json.Unmarshal(raw, &elements) != nil {
		b.refuseField(field, "list_type", messageNotAList, raw)
		return nil
	}
	values := make([]string, 0, len(elements))
	for i, element := range elements {
		var value string
		if isJSONNull(element) || json.Unmarshal(element, &value) != nil {
			var input any
			_ = json.Unmarshal(element, &input)
			b.refuse("string_type", messageNotAString, input, "body", field, i)
			continue
		}
		values = append(values, value)
	}
	return values
}

// requestQuery reads query parameters the same way, collecting each refusal.
type requestQuery struct {
	values   url.Values
	problems []wire.ValidationError
}

func readRequestQuery(r *http.Request) *requestQuery {
	return &requestQuery{values: r.URL.Query()}
}

func (q *requestQuery) refuse(kind, field, message, input string, details map[string]any) {
	q.problems = append(q.problems, wire.ValidationError{
		Type: kind, Loc: []any{"query", field}, Msg: message, Input: input, Ctx: details,
	})
}

// optional answers nil for a parameter left out. An empty one is an answer of its
// own, which the repo filter reads as the untagged items.
func (q *requestQuery) optional(field string) *string {
	if !q.values.Has(field) {
		return nil
	}
	value := q.values.Get(field)
	return &value
}

func (q *requestQuery) stringOr(field, fallback string) string {
	if value := q.optional(field); value != nil {
		return *value
	}
	return fallback
}

func (q *requestQuery) required(field string) string {
	if !q.values.Has(field) {
		q.problems = append(q.problems, wire.ValidationError{
			Type: "missing", Loc: []any{"query", field}, Msg: messageFieldRequired,
		})
		return ""
	}
	return q.values.Get(field)
}

// limit answers nil for no cap. Zero is a cap, and returns no rows.
func (q *requestQuery) limit() *int {
	text := q.optional("limit")
	if text == nil {
		return nil
	}
	limit, err := strconv.Atoi(*text)
	if err != nil {
		q.refuse("int_parsing", "limit", messageUnparsedInt, *text, nil)
		return nil
	}
	if limit < 0 {
		q.refuse("greater_than_equal", "limit", messageNegativeLimit, *text, map[string]any{"ge": 0})
		return nil
	}
	return &limit
}

// layoutsWithoutOffset parse as a time but name no instant, so they are refused
// for that rather than as unreadable.
var layoutsWithoutOffset = []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02"}

// instant reads an RFC 3339 instant. One with no offset is refused rather than
// guessed at, because it names no instant until a zone is assumed.
func (q *requestQuery) instant(field string) *time.Time {
	text := q.optional(field)
	if text == nil {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, *text); err == nil {
		return &parsed
	}
	for _, layout := range layoutsWithoutOffset {
		if _, err := time.Parse(layout, *text); err == nil {
			q.refuse("timezone_aware", field, messageNoOffset, *text, nil)
			return nil
		}
	}
	q.refuse("datetime_from_date_parsing", field, messageNotAnInstant, *text, nil)
	return nil
}
