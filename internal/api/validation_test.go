package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

func TestValidateWorkspaceFiles(t *testing.T) {
	cases := []struct {
		name          string
		files         []WorkspaceFile
		wantErr       bool
		wantErrStatus int
	}{
		{
			name: "valid files",
			files: []WorkspaceFile{
				{Path: "file1.txt", Content: "hello"},
				{Path: "subdir/file2.txt", Content: "world"},
			},
			wantErr: false,
		},
		{
			name: "too many files",
			files: func() []WorkspaceFile {
				fs := make([]WorkspaceFile, maxWorkspaceFiles+1)
				return fs
			}(),
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "absolute path",
			files: []WorkspaceFile{
				{Path: "/etc/passwd", Content: "should not be allowed"},
			},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "escaping path",
			files: []WorkspaceFile{
				{Path: "../secret.txt", Content: "should not be allowed"},
				{Path: "a/../../secret.txt", Content: "should not be allowed"},
			},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "empty path",
			files: []WorkspaceFile{
				{Path: "", Content: "should not be allowed"},
			},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "file too large",
			files: []WorkspaceFile{
				{Path: "bigfile.txt", Content: strings.Repeat("x", maxWorkspaceFileSize+1)},
			},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorkspaceFiles(tc.files)
			if tc.wantErr && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.wantErr && err != nil {
				if apiErr, ok := errors.AsType[*apiError](err); !ok {
					t.Fatalf("expected apiError but got %T", err)
				} else {
					if apiErr.Status != tc.wantErrStatus {
						t.Errorf("expected status code %d but got %d", tc.wantErrStatus, apiErr.Status)
					}
				}
			}
		})
	}
}

func TestValidateExecuteRequestBasic(t *testing.T) {
	cases := []struct {
		name          string
		req           ExecuteRequest
		wantReq       ExecuteRequest
		wantErr       bool
		wantErrStatus int
	}{
		{
			name: "valid request",
			req: ExecuteRequest{
				Language: "python   ",
				Version:  "3.14",
				Code:     "print('hello world')",
			},
			wantReq: ExecuteRequest{
				Language: "python",
				Version:  "3.14",
				Code:     "print('hello world')",
			},
			wantErr: false,
		},
		{
			name: "missing language",
			req: ExecuteRequest{
				Version: "3.14",
				Code:    "print('hello world')",
			},
			wantReq:       ExecuteRequest{},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "missing code",
			req: ExecuteRequest{
				Language: "python",
				Version:  "3.14",
			},
			wantReq:       ExecuteRequest{},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "missing version succeeds",
			req: ExecuteRequest{
				Language: "python",
				Version:  "   ",
				Code:     "print('hello world')",
			},
			wantReq: ExecuteRequest{
				Language: "python",
				Version:  "",
				Code:     "print('hello world')",
			},
			wantErr: false,
		},
		{
			name: "negative timeout",
			req: ExecuteRequest{
				Language:       "python",
				Version:        "3.14",
				Code:           "print('hello world')",
				TimeoutSeconds: -1,
			},
			wantReq:       ExecuteRequest{},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name: "negative memory",
			req: ExecuteRequest{
				Language:    "python",
				Version:     "3.14",
				Code:        "print('hello world')",
				MemoryBytes: -1024,
			},
			wantReq:       ExecuteRequest{},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExecuteRequestBasic(&tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				if apiErr, ok := errors.AsType[*apiError](err); !ok {
					t.Fatalf("expected apiError but got %T", err)
				} else {
					if apiErr.Status != tc.wantErrStatus {
						t.Errorf("expected status code %d but got %d", tc.wantErrStatus, apiErr.Status)
					}
				}
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				got := tc.req
				want := tc.wantReq
				if got.Language != want.Language {
					t.Errorf("expected language %q but got %q", want.Language, got.Language)
				}
				if got.Version != want.Version {
					t.Errorf("expected version %q but got %q", want.Version, got.Version)
				}
				if got.Code != want.Code {
					t.Errorf("expected code %q but got %q", want.Code, got.Code)
				}
			}
		})
	}
}

func TestValidateLimits(t *testing.T) {
	limits := sandbox.LangLimits{
		MinTimeout:     5 * time.Second,
		MaxTimeout:     10 * time.Second,
		MinMemoryBytes: 64 * 1024 * 1024,
		MaxMemoryBytes: 128 * 1024 * 1024,
		MaxCPUs:        sandbox.DefaultMaxCPUs,
	}

	cases := []struct {
		name          string
		req           ExecuteRequest
		wantErr       bool
		wantErrStatus int
	}{
		{
			name:    "values unspecified",
			req:     ExecuteRequest{},
			wantErr: false,
		},
		{
			name:    "valid timeout",
			req:     ExecuteRequest{TimeoutSeconds: 5},
			wantErr: false,
		},
		{
			name:    "valid memory",
			req:     ExecuteRequest{MemoryBytes: 100 * 1024 * 1024},
			wantErr: false,
		},
		{
			name:          "timeout too low",
			req:           ExecuteRequest{TimeoutSeconds: 2},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name:          "timeout too high",
			req:           ExecuteRequest{TimeoutSeconds: 15},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name:          "memory too low",
			req:           ExecuteRequest{MemoryBytes: 32 * 1024 * 1024},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
		{
			name:          "memory too high",
			req:           ExecuteRequest{MemoryBytes: 256 * 1024 * 1024},
			wantErr:       true,
			wantErrStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimits(&tc.req, limits)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				if apiErr, ok := errors.AsType[*apiError](err); !ok {
					t.Fatalf("expected apiError but got %T", err)
				} else {
					if apiErr.Status != tc.wantErrStatus {
						t.Errorf("expected status code %d but got %d", tc.wantErrStatus, apiErr.Status)
					}
				}
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
