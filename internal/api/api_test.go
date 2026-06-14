package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

type fakeSandbox struct {
	// LangSpec stubbing
	langSpec sandbox.LangSpec
	langErr  error

	// Run stubbing
	runResult  sandbox.RunResult
	runDelayMs int
	runErr     error

	// capture for translation assertions
	gotSpec   sandbox.RunSpec
	runCalled bool
}

func (f *fakeSandbox) LangSpec(language, version string) (sandbox.LangSpec, error) {
	if f.langErr != nil {
		return sandbox.LangSpec{}, f.langErr
	}
	return f.langSpec, nil
}

func (f *fakeSandbox) Run(ctx context.Context, spec sandbox.RunSpec) (sandbox.RunResult, error) {
	f.runCalled = true
	f.gotSpec = spec
	if f.runDelayMs > 0 {
		time.Sleep(time.Duration(f.runDelayMs) * time.Millisecond)
	}
	return f.runResult, f.runErr
}
func (f *fakeSandbox) Close() error { return nil }

type pingableSandbox struct {
	fakeSandbox
	pingErr error
}

func (p *pingableSandbox) Ping(ctx context.Context) error { return p.pingErr }

type informerSandbox struct {
	fakeSandbox
	info    sandbox.SandboxInfo
	infoErr error
}

func (i *informerSandbox) Info(ctx context.Context) (sandbox.SandboxInfo, error) {
	return i.info, i.infoErr
}

func newTestServer(sb sandbox.Sandbox, authToken string) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewWorkerPool(4, 8)
	return NewServer(logger, authToken, sb, pool)
}

func runTest(srv *Server, method, path, authHeader string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var r io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			r = strings.NewReader(b)
		default:
			bd, _ := json.Marshal(body)
			r = bytes.NewReader(bd)
		}
	}
	req := httptest.NewRequest(method, path, r)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, req)
	var decoded map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w, decoded
}

func TestExecute(t *testing.T) {
	validLimits := sandbox.LangLimits{
		MaxTimeout:     10 * time.Second,
		MinTimeout:     1 * time.Second,
		MinMemoryBytes: 1 * 1024 * 1024,   // 1 MB
		MaxMemoryBytes: 512 * 1024 * 1024, // 512 MB
	}
	cases := []struct {
		name          string
		sb            sandbox.Sandbox
		reqBody       any
		wantStatus    int
		wantBody      map[string]any
		wantSpec      sandbox.RunSpec
		wantRunCalled bool
		// checkDurationPositive asserts duration_ms > 0 separately instead of via wantBody, for cases where the value is wall-clock derived.
		checkDurationPositive bool
	}{
		{
			name:       "invalid json",
			sb:         &fakeSandbox{},
			reqBody:    `{ invalid json }`,
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "invalid JSON body"},
		},
		{
			name:       "request body too large",
			sb:         &fakeSandbox{},
			reqBody:    `{"language":"` + strings.Repeat("a", maxRequestBodySize+1) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantBody:   map[string]any{"error": "request body too large"},
		},
		{
			name:       "unsupported language",
			sb:         &fakeSandbox{langErr: sandbox.ErrUnsupportedLanguage},
			reqBody:    map[string]any{"language": "unknown", "code": "print('hello')"},
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "unsupported language"},
		},
		{
			name:       "unsupported version",
			sb:         &fakeSandbox{langErr: sandbox.ErrUnsupportedVersion},
			reqBody:    map[string]any{"language": "python", "version": "unknown", "code": "print('hello')"},
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "unsupported version"},
		},
		{
			name:          "internal error",
			sb:            &fakeSandbox{runErr: errors.New("boom")},
			reqBody:       map[string]any{"language": "python", "version": "3.14", "code": "print('hello')"},
			wantStatus:    http.StatusInternalServerError,
			wantBody:      map[string]any{"error": "execution failed"},
			wantRunCalled: true,
		},
		{
			name:       "validation error - missing code",
			sb:         &fakeSandbox{},
			reqBody:    map[string]any{"language": "python", "version": "3.14"},
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "code is required"},
		},
		{
			name:       "validation error - timeout above max",
			sb:         &fakeSandbox{langSpec: sandbox.LangSpec{Limits: sandbox.LangLimits{MaxTimeout: 10 * time.Second, MinTimeout: 1 * time.Second}}},
			reqBody:    map[string]any{"language": "python", "version": "3.14", "code": "print('hello')", "timeout_seconds": 20},
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "timeout_seconds must be within [1, 10] seconds"},
		},
		{
			name: "validation error - invalid workspace file",
			sb:   &fakeSandbox{},
			reqBody: map[string]any{
				"language": "python",
				"version":  "3.14",
				"code":     "print('hello')",
				"workspace_files": []map[string]any{
					{"path": "../secret.txt", "content": "should not be allowed"},
				},
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]any{"error": "workspace file path must be relative and within the workspace: ../secret.txt"},
		},
		{
			name: "successful execution",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					Stdout:   "hello\n",
					Stderr:   "",
					ExitCode: 0,
					Status:   sandbox.StatusOK,
					Duration: 500 * time.Millisecond,
				},
			},
			reqBody: map[string]any{
				"language":        "python",
				"version":         "3.14",
				"code":            "print('hello')",
				"stdin":           "some input",
				"timeout_seconds": 5,
				"memory_bytes":    64 * 1024 * 1024,
				"workspace_files": []map[string]any{
					{"path": "data.txt", "content": "some data"},
				},
			},
			wantStatus: http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "hello\n",
				"stderr":      "",
				"exit_code":   float64(0),
				"status":      "ok",
				"duration_ms": float64(500),
			},
			wantSpec: sandbox.RunSpec{
				Language:    "python",
				Version:     "3.14",
				Code:        "print('hello')",
				Stdin:       "some input",
				Timeout:     5 * time.Second,
				MemoryBytes: 64 * 1024 * 1024,
				WorkspaceFiles: []sandbox.WorkspaceFile{
					{Path: "data.txt", Content: "some data"},
				},
			},
			wantRunCalled: true,
		},
		{
			name: "successful execution - duration present even if RunResult duration is not set",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					Stdout:   "hello\n",
					Stderr:   "",
					ExitCode: 0,
					Status:   sandbox.StatusOK,
				},
				runDelayMs: 250, // simulate a delay to ensure duration calculated by handler is meaningful (>0)
			},
			reqBody: map[string]any{
				"language":        "python",
				"version":         "3.14",
				"code":            "print('hello')",
				"timeout_seconds": 5,
			},
			wantStatus: http.StatusOK,
			// duration_ms is wall-clock derived here (handler's time.Since fallback),
			// so it is asserted as > 0 via checkDurationPositive, not pinned to a value.
			wantBody: map[string]any{
				"stdout":    "hello\n",
				"stderr":    "",
				"exit_code": float64(0),
				"status":    "ok",
			},
			wantSpec: sandbox.RunSpec{
				Language:    "python",
				Version:     "3.14",
				Code:        "print('hello')",
				Timeout:     5 * time.Second,
				MemoryBytes: 0,
			},
			wantRunCalled:         true,
			checkDurationPositive: true,
		},
		{
			name: "execution error - runtime error",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					ExitCode: 1,
					Status:   sandbox.StatusRuntimeError,
					Duration: 300 * time.Millisecond,
				},
			},
			reqBody: map[string]any{
				"language":        "python",
				"version":         "3.14",
				"code":            "print(1/0)",
				"timeout_seconds": 5,
			},
			wantStatus: http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "",
				"stderr":      "",
				"exit_code":   float64(1),
				"status":      "runtime_error",
				"duration_ms": float64(300),
			},
			wantSpec: sandbox.RunSpec{
				Language:    "python",
				Version:     "3.14",
				Code:        "print(1/0)",
				Timeout:     5 * time.Second,
				MemoryBytes: 0,
			},
			wantRunCalled: true,
		},
		{
			name: "execution error - timeout",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					ExitCode: -1,
					Status:   sandbox.StatusTimeout,
					Duration: 10 * time.Second,
				},
			},
			reqBody: map[string]any{
				"language":        "python",
				"version":         "3.14",
				"code":            "while True: pass",
				"timeout_seconds": 5,
			},
			wantStatus: http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "",
				"stderr":      "",
				"exit_code":   float64(-1),
				"status":      "timeout",
				"duration_ms": float64(10000),
			},
			wantSpec: sandbox.RunSpec{
				Language:    "python",
				Version:     "3.14",
				Code:        "while True: pass",
				Timeout:     5 * time.Second,
				MemoryBytes: 0,
			},
			wantRunCalled: true,
		},
		{
			name: "execution error - out of memory",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					ExitCode: -1,
					Status:   sandbox.StatusOOM,
					Duration: 200 * time.Millisecond,
				},
			},
			reqBody: map[string]any{
				"language":        "python",
				"version":         "3.14",
				"code":            "a = ' ' * (1024 * 1024 * 1024)",
				"timeout_seconds": 5,
				"memory_bytes":    64 * 1024 * 1024,
			},
			wantStatus: http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "",
				"stderr":      "",
				"exit_code":   float64(-1),
				"status":      "oom",
				"duration_ms": float64(200),
			},
			wantSpec: sandbox.RunSpec{
				Language:    "python",
				Version:     "3.14",
				Code:        "a = ' ' * (1024 * 1024 * 1024)",
				Timeout:     5 * time.Second,
				MemoryBytes: 64 * 1024 * 1024,
			},
			wantRunCalled: true,
		},
		{
			name: "execution error - compile error",
			sb: &fakeSandbox{
				langSpec: sandbox.LangSpec{Limits: validLimits},
				runResult: sandbox.RunResult{
					ExitCode: 1,
					Status:   sandbox.StatusCompileError,
					Duration: 100 * time.Millisecond,
				},
			},
			reqBody: map[string]any{
				"language":        "c",
				"version":         "17",
				"code":            "int main() { return }",
				"timeout_seconds": 5,
			},
			wantStatus: http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "",
				"stderr":      "",
				"exit_code":   float64(1),
				"status":      "compile_error",
				"duration_ms": float64(100),
			},
			wantSpec: sandbox.RunSpec{
				Language:    "c",
				Version:     "17",
				Code:        "int main() { return }",
				Timeout:     5 * time.Second,
				MemoryBytes: 0,
			},
			wantRunCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(tc.sb, "")
			w, gotBody := runTest(srv, http.MethodPost, "/execute", "", tc.reqBody)
			if w.Code != tc.wantStatus {
				t.Errorf("unexpected status code: got %d, want %d", w.Code, tc.wantStatus)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("unexpected Content-Type: got %q, want application/json", ct)
			}
			if tc.checkDurationPositive {
				dur, ok := gotBody["duration_ms"].(float64)
				if !ok || dur <= 0 {
					t.Errorf("expected positive duration_ms, got %v", gotBody["duration_ms"])
				}
				delete(gotBody, "duration_ms") // asserted above; exclude from exact body match
			}
			if !maps.Equal(gotBody, tc.wantBody) {
				t.Errorf("unexpected body: got %v, want %v", gotBody, tc.wantBody)
			}
			if tc.sb.(*fakeSandbox).runCalled != tc.wantRunCalled {
				t.Errorf("unexpected Run called: got %v, want %v", tc.sb.(*fakeSandbox).runCalled, tc.wantRunCalled)
			}
			if tc.wantRunCalled && tc.wantStatus == http.StatusOK {
				gotSpec := tc.sb.(*fakeSandbox).gotSpec
				if gotSpec.Language != tc.wantSpec.Language {
					t.Errorf("unexpected Run spec Language: got %s, want %s", gotSpec.Language, tc.wantSpec.Language)
				}
				if gotSpec.Version != tc.wantSpec.Version {
					t.Errorf("unexpected Run spec Version: got %s, want %s", gotSpec.Version, tc.wantSpec.Version)
				}
				if gotSpec.Code != tc.wantSpec.Code {
					t.Errorf("unexpected Run spec Code: got %s, want %s", gotSpec.Code, tc.wantSpec.Code)
				}
				if gotSpec.Stdin != tc.wantSpec.Stdin {
					t.Errorf("unexpected Run spec Stdin: got %s, want %s", gotSpec.Stdin, tc.wantSpec.Stdin)
				}
				if gotSpec.Timeout != tc.wantSpec.Timeout {
					t.Errorf("unexpected Run spec Timeout: got %v, want %v", gotSpec.Timeout, tc.wantSpec.Timeout)
				}
				if gotSpec.MemoryBytes != tc.wantSpec.MemoryBytes {
					t.Errorf("unexpected Run spec MemoryBytes: got %d, want %d", gotSpec.MemoryBytes, tc.wantSpec.MemoryBytes)
				}
				if len(gotSpec.WorkspaceFiles) != len(tc.wantSpec.WorkspaceFiles) {
					t.Errorf("unexpected number of WorkspaceFiles: got %d, want %d", len(gotSpec.WorkspaceFiles), len(tc.wantSpec.WorkspaceFiles))
				}
				for i := range gotSpec.WorkspaceFiles {
					if gotSpec.WorkspaceFiles[i].Path != tc.wantSpec.WorkspaceFiles[i].Path {
						t.Errorf("unexpected Run spec WorkspaceFiles[%d] Path: got %s, want %s", i, gotSpec.WorkspaceFiles[i].Path, tc.wantSpec.WorkspaceFiles[i].Path)
					}
					if gotSpec.WorkspaceFiles[i].Content != tc.wantSpec.WorkspaceFiles[i].Content {
						t.Errorf("unexpected Run spec WorkspaceFiles[%d] Content: got %s, want %s", i, gotSpec.WorkspaceFiles[i].Content, tc.wantSpec.WorkspaceFiles[i].Content)
					}
				}
			}
		})
	}
}

func TestExecutePoolSaturated(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewWorkerPool(1, 0)
	sb := &fakeSandbox{}
	srv := NewServer(logger, "", sb, pool)
	pool.sem <- struct{}{} // occupy the only worker slot

	reqBody := map[string]any{"language": "python", "version": "3.14", "code": "print('hello')"}
	w, gotBody := runTest(srv, http.MethodPost, "/execute", "", reqBody)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("unexpected status code: got %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	wantBody := map[string]any{"error": "Worker pool is full, try again later"}
	if !maps.Equal(gotBody, wantBody) {
		t.Errorf("unexpected body: got %v, want %v", gotBody, wantBody)
	}
	if sb.runCalled {
		t.Error("Run should not be called when the pool sheds the request")
	}
}

func TestHealthz(t *testing.T) {
	t.Run("healthy sandbox", func(t *testing.T) {
		sb := &pingableSandbox{fakeSandbox: fakeSandbox{}, pingErr: nil}
		srv := newTestServer(sb, "")
		w, gotBody := runTest(srv, http.MethodGet, "/healthz", "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("unexpected status code: got %d, want %d", w.Code, http.StatusOK)
		}
		wantBody := map[string]any{"status": "ok"}
		if !maps.Equal(gotBody, wantBody) {
			t.Errorf("unexpected body: got %v, want %v", gotBody, wantBody)
		}
	})
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		name       string
		sb         sandbox.Sandbox
		wantStatus int
		wantBody   map[string]any
	}{
		{
			name:       "sandbox without Ping should be ready",
			sb:         &fakeSandbox{},
			wantStatus: http.StatusOK,
			wantBody:   map[string]any{"status": "ready"},
		},
		{
			name:       "sandbox with Ping that succeeds should be ready",
			sb:         &pingableSandbox{fakeSandbox: fakeSandbox{}, pingErr: nil},
			wantStatus: http.StatusOK,
			wantBody:   map[string]any{"status": "ready"},
		},
		{
			name:       "sandbox with Ping that fails should be unavailable",
			sb:         &pingableSandbox{fakeSandbox: fakeSandbox{}, pingErr: errors.New("ping failed")},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   map[string]any{"status": "unavailable"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(tc.sb, "")
			w, gotBody := runTest(srv, http.MethodGet, "/readyz", "", nil)
			if w.Code != tc.wantStatus {
				t.Errorf("unexpected status code: got %d, want %d", w.Code, tc.wantStatus)
			}
			if !maps.Equal(gotBody, tc.wantBody) {
				t.Errorf("unexpected body: got %v, want %v", gotBody, tc.wantBody)
			}
		})
	}
}

func TestInfo(t *testing.T) {
	validLimits := sandbox.LangLimits{
		MaxTimeout:     10 * time.Second,
		MinTimeout:     1 * time.Second,
		MinMemoryBytes: 1 * 1024 * 1024,
		MaxMemoryBytes: 512 * 1024 * 1024,
		MaxPids:        100,
		MaxCPUs:        0.5,
	}
	wantLimits := LimitsResponse{
		MinTimeoutSeconds: 1,
		MaxTimeoutSeconds: 10,
		MinMemoryBytes:    1 * 1024 * 1024,
		MaxMemoryBytes:    512 * 1024 * 1024,
		MaxPids:           100,
		MaxCPUs:           0.5,
	}
	wantWorkspace := WorkspaceLimitsResponse{
		MaxFiles:         maxWorkspaceFiles,
		MaxFileSizeBytes: maxWorkspaceFileSize,
	}

	cases := []struct {
		name       string
		sb         sandbox.Sandbox
		wantStatus int
		// Exactly one of wantInfo / wantErr is set, keyed off wantStatus.
		wantInfo *InfoResponse
		wantErr  map[string]any
	}{
		{
			name:       "sandbox without Info should return empty languages",
			sb:         &fakeSandbox{},
			wantStatus: http.StatusOK,
			wantInfo: &InfoResponse{
				Languages: []LanguageInfoResponse{},
				Workspace: wantWorkspace,
			},
		},
		{
			name: "sandbox with Info should return languages from Info",
			sb: &informerSandbox{
				fakeSandbox: fakeSandbox{},
				info: sandbox.SandboxInfo{
					Languages: []sandbox.LanguageInfo{
						{Name: "python", DefaultVersion: "3.14", Versions: []string{"3.14", "3.15"}, Filename: "main.py", Limits: validLimits},
						{Name: "c", DefaultVersion: "17", Versions: []string{"17"}, Filename: "main.c", Limits: validLimits},
					},
				},
			},
			wantStatus: http.StatusOK,
			wantInfo: &InfoResponse{
				Languages: []LanguageInfoResponse{
					{Name: "python", DefaultVersion: "3.14", Versions: []string{"3.14", "3.15"}, Filename: "main.py", Limits: wantLimits},
					{Name: "c", DefaultVersion: "17", Versions: []string{"17"}, Filename: "main.c", Limits: wantLimits},
				},
				Workspace: wantWorkspace,
			},
		},
		{
			name: "sandbox with Info that returns error should return internal server error",
			sb: &informerSandbox{
				fakeSandbox: fakeSandbox{},
				infoErr:     errors.New("info error"),
			},
			wantStatus: http.StatusInternalServerError,
			wantErr:    map[string]any{"error": "failed to get sandbox info"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(tc.sb, "")
			w, gotBody := runTest(srv, http.MethodGet, "/info", "", nil)
			if w.Code != tc.wantStatus {
				t.Errorf("unexpected status code: got %d, want %d", w.Code, tc.wantStatus)
			}

			if tc.wantInfo != nil {
				var got InfoResponse
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatalf("failed to decode info response: %v", err)
				}
				if !reflect.DeepEqual(got, *tc.wantInfo) {
					t.Errorf("unexpected info response:\n got  %+v\n want %+v", got, *tc.wantInfo)
				}
				return
			}

			if !maps.Equal(gotBody, tc.wantErr) {
				t.Errorf("unexpected body: got %v, want %v", gotBody, tc.wantErr)
			}
		})
	}
}
