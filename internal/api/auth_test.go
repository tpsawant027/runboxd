package api

import (
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

const validAuthToken = "valid-token"

func TestRequireAuth(t *testing.T) {
	validReqBody := map[string]any{
		"language":        "python",
		"version":         "3.14",
		"code":            "print('hello')",
		"stdin":           "some input",
		"timeout_seconds": 5,
		"memory_bytes":    64 * 1024 * 1024,
		"workspace_files": []map[string]any{
			{"path": "data.txt", "content": "some data"},
		},
	}

	validLimits := sandbox.LangLimits{
		MaxTimeout:     10 * time.Second,
		MinTimeout:     1 * time.Second,
		MinMemoryBytes: 1 * 1024 * 1024,
		MaxMemoryBytes: 512 * 1024 * 1024,
	}

	cases := []struct {
		name            string
		sb              sandbox.Sandbox
		validAuthToken  string
		reqHeader       string
		reqBody         map[string]any
		wantStatus      int
		wantBody        map[string]any
		wantRespHeaders map[string]string
		wantRunCalled   bool
	}{
		{
			name: "success with valid token",
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
			validAuthToken: validAuthToken,
			reqHeader:      "Bearer " + validAuthToken,
			reqBody:        validReqBody,
			wantStatus:     http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "hello\n",
				"stderr":      "",
				"exit_code":   float64(0),
				"status":      "ok",
				"duration_ms": float64(500),
			},
			wantRunCalled: true,
		},
		{
			name:            "missing header",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       "",
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name:            "invalid token",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       "Bearer invalid-token",
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name:            "malformed header",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       "InvalidHeader " + validAuthToken,
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name:            "empty token",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       "Bearer ",
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name:            "Bearer prefix with no token",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       "Bearer",
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name:            "raw token without scheme",
			sb:              &fakeSandbox{},
			validAuthToken:  validAuthToken,
			reqHeader:       validAuthToken,
			reqBody:         validReqBody,
			wantStatus:      http.StatusUnauthorized,
			wantBody:        map[string]any{"error": "unauthorized"},
			wantRespHeaders: map[string]string{"WWW-Authenticate": "Bearer"},
			wantRunCalled:   false,
		},
		{
			name: "success with empty token (allow unauthenticated)",
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
			validAuthToken: "",
			reqHeader:      "",
			reqBody:        validReqBody,
			wantStatus:     http.StatusOK,
			wantBody: map[string]any{
				"stdout":      "hello\n",
				"stderr":      "",
				"exit_code":   float64(0),
				"status":      "ok",
				"duration_ms": float64(500),
			},
			wantRunCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(tc.sb, tc.validAuthToken)
			w, gotBody := runTest(srv, http.MethodPost, "/execute", tc.reqHeader, tc.reqBody)

			if w.Code != tc.wantStatus {
				t.Errorf("unexpected status code: got %d, want %d", w.Code, tc.wantStatus)
			}

			if tc.sb.(*fakeSandbox).runCalled != tc.wantRunCalled {
				t.Errorf("unexpected runCalled: got %v, want %v", tc.sb.(*fakeSandbox).runCalled, tc.wantRunCalled)
			}

			if !maps.Equal(gotBody, tc.wantBody) {
				t.Errorf("unexpected body: got %v, want %v", gotBody, tc.wantBody)
			}

			if tc.wantRespHeaders != nil {
				for k, v := range tc.wantRespHeaders {
					if got := w.Header().Get(k); got != v {
						t.Errorf("unexpected header %q: got %q, want %q", k, got, v)
					}
				}
			}
		})
	}

	for _, path := range []string{"/healthz", "/readyz", "/info"} {
		t.Run("no auth required for "+path, func(t *testing.T) {
			srv := newTestServer(&fakeSandbox{}, validAuthToken)
			w, _ := runTest(srv, http.MethodGet, path, "", nil)
			if w.Code != http.StatusOK {
				t.Errorf("unexpected status code: got %d, want %d", w.Code, http.StatusOK)
			}
		})
	}
}
