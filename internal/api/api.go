// Package api implements the HTTP API for Runboxd. It defines the Server struct
// that holds dependencies and the handler methods for each endpoint. The API
// translates between HTTP requests/responses and the internal Sandbox interface,
// including error handling and response formatting.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v3"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

type Server struct {
	logger  *slog.Logger
	sandbox sandbox.Sandbox
}

func NewServer(logger *slog.Logger, sb sandbox.Sandbox) *Server {
	return &Server{logger: logger, sandbox: sb}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(httplog.RequestLogger(s.logger, &httplog.Options{
		Level:         slog.LevelInfo,
		RecoverPanics: true,
	}))
	r.Use(requestIDAttr)

	r.Get("/healthz", handle(s.handleHealthz))
	r.Get("/readyz", handle(s.handleReadyz))
	r.Get("/info", handle(s.handleInfo))
	r.Post("/execute", handle(s.handleExecute))
	return r
}

func requestIDAttr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			httplog.SetAttrs(r.Context(), slog.String("request_id", id))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) error {
	if p, ok := s.sandbox.(sandbox.Pinger); ok {
		if err := p.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	return nil
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) error {
	resp := InfoResponse{Languages: []sandbox.LanguageInfo{}}
	if i, ok := s.sandbox.(sandbox.Informer); ok {
		info, err := i.Info(r.Context())
		if err != nil {
			return internalError("failed to get sandbox info").wrap(err)
		}
		resp.Languages = info.Languages
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleExecute decodes a request, runs it through the Sandbox, and returns a
// structured result.
// TODO: submit s.sandbox.Run via the bounded worker pool so concurrent requests are queued, not run unbounded.
func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) error {
	var req ExecuteRequest
	if err := readBody(w, r, &req); err != nil {
		return err
	}

	if err := validateExecuteRequest(&req); err != nil {
		return err
	}

	start := time.Now()
	result, err := s.sandbox.Run(r.Context(), sandbox.RunSpec{
		Language:    req.Language,
		Version:     req.Version,
		Code:        req.Code,
		Stdin:       req.Stdin,
		Timeout:     time.Duration(req.TimeoutSeconds) * time.Second,
		MemoryBytes: req.MemoryBytes,
	})
	if err != nil {
		return mapRunError(err)
	}

	durationMs := result.Duration.Milliseconds()
	if durationMs == 0 {
		durationMs = time.Since(start).Milliseconds()
	}

	writeJSON(w, http.StatusOK, ExecuteResponse{
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		ExitCode:   result.ExitCode,
		Status:     string(result.Status),
		DurationMs: durationMs,
	})
	return nil
}

func validateExecuteRequest(req *ExecuteRequest) error {
	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		return badRequest("language is required")
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Code == "" {
		return badRequest("code is required")
	}
	if req.TimeoutSeconds < 0 {
		return badRequest("timeout_seconds must be non-negative")
	}
	if req.MemoryBytes < 0 {
		return badRequest("memory_bytes must be non-negative")
	}
	return nil
}

func mapRunError(err error) *apiError {
	if errors.Is(err, sandbox.ErrUnsupportedLanguage) {
		return badRequest("unsupported language")
	}
	if errors.Is(err, sandbox.ErrUnsupportedVersion) {
		return badRequest("unsupported version")
	}
	return internalError("execution failed").wrap(err)
}
