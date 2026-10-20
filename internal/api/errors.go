// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/mayura-andrew/eventpass/internal/storage"
)

// apiError is the one error shape: {"error": {"code", "message", "details"}}.
// Codes are stable; the UI translates them.
type apiError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errorf(status int, code, msg string) *apiError {
	return &apiError{Status: status, Code: code, Message: msg}
}

var (
	errUnauthenticated = errorf(http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
	errForbidden       = errorf(http.StatusForbidden, "forbidden", "You don't have permission to do that.")
	errNotFound        = errorf(http.StatusNotFound, "not_found", "Not found.")
	errCSRF            = errorf(http.StatusForbidden, "csrf_missing", "Missing request header.")
	errRateLimited     = errorf(http.StatusTooManyRequests, "rate_limited", "Too many requests. Wait a minute and try again.")
	errBusy            = errorf(http.StatusServiceUnavailable, "busy", "The spreadsheet is busy. Try again in a few seconds.")
)

func badRequest(code, msg string) *apiError { return errorf(http.StatusBadRequest, code, msg) }

// validationError reports field problems: details.fields maps field → code.
func validationError[V any](fields map[string]V) *apiError {
	e := errorf(http.StatusUnprocessableEntity, "validation_failed", "Some values need fixing.")
	e.Details = map[string]any{"fields": fields}
	return e
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
	case errors.Is(err, storage.ErrNotFound):
		ae = errNotFound
	case errors.Is(err, storage.ErrBusy):
		ae = errBusy
	case errors.Is(err, storage.ErrConflict):
		ae = errorf(http.StatusConflict, "conflict", "That value is already in use.")
	default:
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		ae = errorf(http.StatusInternalServerError, "internal", "Something went wrong. Try again.")
	}
	writeJSON(w, ae.Status, map[string]*apiError{"error": ae})
}

// maxBody bounds JSON request bodies. Uploads have their own limits.
const maxBody = 1 << 20

// decode reads a JSON body strictly: unknown fields are an error, so typos
// in API clients fail loudly instead of being ignored.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return errorf(http.StatusRequestEntityTooLarge, "too_large", "Request body is too large.")
		}
		return badRequest("invalid_json", "Request body is not valid JSON for this endpoint.")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return badRequest("invalid_json", "Request body has trailing data.")
	}
	return nil
}
