package api

import (
	"github.com/tpsawant027/runboxd/internal/sandbox"
)

// WorkspaceFile represents a file in the workspace.
type WorkspaceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ExecuteRequest represents the JSON body of a code execution request.
type ExecuteRequest struct {
	Language       string          `json:"language"`
	Version        string          `json:"version,omitempty"`
	Code           string          `json:"code"`
	Stdin          string          `json:"stdin,omitempty"`
	WorkspaceFiles []WorkspaceFile `json:"workspace_files,omitempty"`
	TimeoutSeconds int64           `json:"timeout_seconds,omitempty"`
	MemoryBytes    int64           `json:"memory_bytes,omitempty"`
}

// ExecuteResponse represents the JSON body of a code execution response.
type ExecuteResponse struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

// LimitsResponse represents the JSON body of the limits information in an info response.
type LimitsResponse struct {
	MinTimeoutSeconds         float64 `json:"min_timeout_seconds"`
	MaxTimeoutSeconds         float64 `json:"max_timeout_seconds"`
	MinMemoryBytes            int64   `json:"min_memory_bytes"`
	MaxMemoryBytes            int64   `json:"max_memory_bytes"`
	MaxWorkspaceFiles         int     `json:"max_workspace_files"`
	MaxWorkspaceFileSizeBytes int     `json:"max_workspace_file_size_bytes"`
}

// InfoResponse represents the JSON body of an info response.
type InfoResponse struct {
	Languages []sandbox.LanguageInfo `json:"languages"`
	Limits    LimitsResponse         `json:"limits"`
}
