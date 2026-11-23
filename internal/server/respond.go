package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// apiError is an error with an HTTP status and a message safe to show.
type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) error {
	return &apiError{http.StatusNotFound, fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) error {
	return &apiError{http.StatusConflict, fmt.Sprintf(format, args...)}
}

// handle adapts a handler that returns an error. Known errors map to
// their status; anything else is logged and reported as a generic 500, so
// internal details (paths, SQL) never reach the client.
func (s *Server) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		if ae, ok := errors.AsType[*apiError](err); ok {
			writeError(w, ae.code, ae.msg)
			return
		}
		if ie, ok := errors.AsType[*app.InvalidError](err); ok {
			writeError(w, http.StatusBadRequest, ie.Error())
			return
		}
		if be, ok := errors.AsType[*app.BoardError](err); ok {
			writeError(w, http.StatusBadRequest, be.Error())
			return
		}
		switch {
		case isTooBig(err):
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoPreset), errors.Is(err, store.ErrNotTracked), errors.Is(err, store.ErrNoCompany):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, store.ErrAmbiguousKey):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
			// The client went away; there's no one to answer.
		default:
			s.log.Error("api request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decode reads a JSON body into v. It requires a JSON content type (which
// an HTML form can't send cross-site) and rejects unknown fields so typos
// fail loudly instead of being ignored.
func decode(r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return &apiError{http.StatusUnsupportedMediaType, "Content-Type must be application/json"}
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if isTooBig(err) {
			return err
		}
		return badRequest("invalid JSON body: %v", err)
	}
	if dec.More() {
		return badRequest("invalid JSON body: more than one value")
	}
	return nil
}

func isTooBig(err error) bool {
	_, ok := errors.AsType[*http.MaxBytesError](err)
	return ok
}
