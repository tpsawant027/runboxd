// Package api implements the HTTP API for Runboxd. It defines the Server struct
// that holds dependencies and the handler methods for each endpoint. The API
// translates between HTTP requests/responses and the internal Sandbox interface,
// including error handling and response formatting.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v3"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

const (
	maxWorkspaceFiles    = 12
	maxWorkspaceFileSize = 100 * 1024 // 100 KB
)

type Server struct {
	logger  *slog.Logger
	sandbox sandbox.Sandbox
	pool    *WorkerPool

	sandboxSupportsInfo bool
	sandboxInfo         *sandbox.SandboxInfo
	sandboxLimits       sandbox.Limits
}

func NewServer(logger *slog.Logger, sb sandbox.Sandbox, pool *WorkerPool) *Server {
	var info *sandbox.SandboxInfo
	var supportsInfo bool
	if i, ok := sb.(sandbox.Informer); ok {
		sandboxInfo, err := i.Info(context.Background())
		if err != nil {
			logger.Warn("failed to get sandbox info during server initialization", "error", err)
		} else {
			info = &sandboxInfo
		}
		supportsInfo = true
	} else {
		logger.Info("sandbox does not support Info() method, info endpoint will be limited")
	}

	limits := sb.Limits()

	return &Server{
		logger: logger, sandbox: sb, pool: pool, sandboxSupportsInfo: supportsInfo,
		sandboxInfo: info, sandboxLimits: limits,
	}
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
	r.With(s.pool.Middleware()).Post("/execute", handle(s.handleExecute))
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

	info := s.sandboxInfo
	if info == nil && s.sandboxSupportsInfo {
		if i, ok := s.sandbox.(sandbox.Informer); ok {
			got, err := i.Info(r.Context())
			if err != nil {
				return internalError("failed to get sandbox info").wrap(err)
			}
			info = &got
		}
	}
	if info != nil {
		resp.Languages = info.Languages
	}

	resp.Limits = LimitsResponse{
		MinTimeoutSeconds:         s.sandboxLimits.MinTimeout.Seconds(),
		MaxTimeoutSeconds:         s.sandboxLimits.MaxTimeout.Seconds(),
		MinMemoryBytes:            s.sandboxLimits.MinMemoryBytes,
		MaxMemoryBytes:            s.sandboxLimits.MaxMemoryBytes,
		MaxWorkspaceFiles:         maxWorkspaceFiles,
		MaxWorkspaceFileSizeBytes: maxWorkspaceFileSize,
	}

	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) error {
	var req ExecuteRequest
	if err := readBody(w, r, &req); err != nil {
		return err
	}

	if err := validateExecuteRequest(&req, s.sandboxLimits); err != nil {
		return err
	}

	start := time.Now()
	result, err := s.sandbox.Run(r.Context(), sandbox.RunSpec{
		Language:       req.Language,
		Version:        req.Version,
		Code:           req.Code,
		Stdin:          req.Stdin,
		WorkspaceFiles: toSandboxWorkspaceFiles(req.WorkspaceFiles),
		Timeout:        time.Duration(req.TimeoutSeconds) * time.Second,
		MemoryBytes:    req.MemoryBytes,
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

func validateExecuteRequest(req *ExecuteRequest, sandboxLimits sandbox.Limits) error {
	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		return badRequest("language is required")
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Code == "" {
		return badRequest("code is required")
	}
	if req.TimeoutSeconds != 0 {
		if req.TimeoutSeconds < int64(sandboxLimits.MinTimeout.Seconds()) {
			return badRequest(fmt.Sprintf("timeout_seconds must be at least %d", int64(sandboxLimits.MinTimeout.Seconds())))
		}
		if req.TimeoutSeconds > int64(sandboxLimits.MaxTimeout.Seconds()) {
			return badRequest(fmt.Sprintf("timeout_seconds must be at most %d", int64(sandboxLimits.MaxTimeout.Seconds())))
		}
	}
	if req.MemoryBytes != 0 {
		if req.MemoryBytes < sandboxLimits.MinMemoryBytes {
			return badRequest(fmt.Sprintf("memory_bytes must be at least %d", sandboxLimits.MinMemoryBytes))
		}
		if req.MemoryBytes > sandboxLimits.MaxMemoryBytes {
			return badRequest(fmt.Sprintf("memory_bytes must be at most %d", sandboxLimits.MaxMemoryBytes))
		}
	}
	if err := validateWorkspaceFiles(req.WorkspaceFiles); err != nil {
		return err
	}
	return nil
}

func validateWorkspaceFiles(files []WorkspaceFile) error {
	if len(files) > maxWorkspaceFiles {
		return badRequest(fmt.Sprintf("too many workspace files (max %d)", maxWorkspaceFiles))
	}
	for _, f := range files {
		if !filepath.IsLocal(f.Path) {
			return badRequest("workspace file path must be relative and within the workspace: " + f.Path)
		}
		if len(f.Content) > maxWorkspaceFileSize {
			return badRequest(fmt.Sprintf("workspace file %s exceeds max size of %d bytes", f.Path, maxWorkspaceFileSize))
		}
	}
	return nil
}

func toSandboxWorkspaceFiles(files []WorkspaceFile) []sandbox.WorkspaceFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]sandbox.WorkspaceFile, len(files))
	for i, f := range files {
		out[i] = sandbox.WorkspaceFile{Path: f.Path, Content: f.Content}
	}
	return out
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
