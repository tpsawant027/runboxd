package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/httplog/v3"
)

type apiError struct {
	Status int
	Msg    string
	Err    error
}

func (e *apiError) Error() string { return e.Msg }

func (e *apiError) wrap(err error) *apiError {
	e.Err = err
	return e
}

func badRequest(msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Msg: msg}
}

func internalError(msg string) *apiError {
	return &apiError{Status: http.StatusInternalServerError, Msg: msg}
}

func handle(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		if apiErr, ok := errors.AsType[*apiError](err); ok {
			if apiErr.Err != nil {
				httplog.SetError(r.Context(), apiErr.Err)
			}
			writeError(w, apiErr.Status, apiErr.Msg)
			return
		}
		httplog.SetError(r.Context(), err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func readBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if mbErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return (&apiError{Status: http.StatusRequestEntityTooLarge, Msg: "request body too large"}).wrap(mbErr)
		}
		return badRequest("invalid JSON body").wrap(err)
	}
	return nil
}
