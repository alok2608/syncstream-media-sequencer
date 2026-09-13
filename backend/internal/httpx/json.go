// Package httpx holds transport-level helpers: a consistent JSON envelope,
// request decoding with sane limits, and middleware.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/service"
)

// maxRequestBytes caps request bodies; every payload here is small.
const maxRequestBytes = 1 << 20 // 1 MiB

// Envelope is the success shape: {"data": ...}.
type Envelope struct {
	Data any `json:"data"`
}

// ErrorBody is the failure shape: {"error": {...}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes what went wrong. Fields is populated for validation
// failures so a form can highlight the offending inputs.
type ErrorDetail struct {
	Code    string                   `json:"code"`
	Message string                   `json:"message"`
	Fields  []models.ValidationError `json:"fields,omitempty"`
}

// Error codes returned by the API.
const (
	CodeBadRequest  = "bad_request"
	CodeValidation  = "validation_failed"
	CodeNotFound    = "not_found"
	CodeInternal    = "internal_error"
	CodeUnavailable = "service_unavailable"
)

// WriteJSON writes any payload with the given status.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already sent; all that is left is to record it.
		slog.Error("write json response", "error", err)
	}
}

// WriteData writes a success envelope.
func WriteData(w http.ResponseWriter, status int, data any) {
	WriteJSON(w, status, Envelope{Data: data})
}

// WriteError writes a failure envelope.
func WriteError(w http.ResponseWriter, status int, code, message string, fields ...models.ValidationError) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message, Fields: fields}})
}

// WriteServiceError maps a service-layer error onto the right HTTP status.
// Unexpected errors are logged in full but reported generically.
func WriteServiceError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var validation *service.ValidationFailure
	switch {
	case errors.As(err, &validation):
		WriteError(w, http.StatusUnprocessableEntity, CodeValidation,
			"the request contains invalid values", validation.Errors...)

	case errors.Is(err, models.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, "the requested resource does not exist")

	case errors.Is(err, io.EOF):
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "a JSON request body is required")

	default:
		logger.Error("unhandled request error", "error", err)
		WriteError(w, http.StatusInternalServerError, CodeInternal, "something went wrong on our side")
	}
}

// DecodeJSON reads a JSON body, rejecting oversized bodies, unknown fields and
// trailing content so typos in a request surface as clear errors.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return describeDecodeError(err)
	}
	if dec.More() {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func describeDecodeError(err error) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var maxBytesErr *http.MaxBytesError

	switch {
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("malformed JSON at byte %d", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return fmt.Errorf("field %q expects a %s", typeErr.Field, typeErr.Type)
		}
		return fmt.Errorf("expected a %s", typeErr.Type)
	case errors.As(err, &maxBytesErr):
		return fmt.Errorf("request body must be smaller than %d bytes", maxBytesErr.Limit)
	case errors.Is(err, io.EOF):
		return errors.New("a JSON request body is required")
	default:
		return err
	}
}

// PathID parses a numeric path parameter.
func PathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, raw)
	}
	return id, nil
}
