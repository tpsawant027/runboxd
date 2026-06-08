package api

import (
	"github.com/tpsawant027/runboxd/internal/sandbox"
)

// ExecuteRequest represents the JSON body of a code execution request.
type ExecuteRequest struct {
	Language       string `json:"language"`
	Version        string `json:"version,omitempty"`
	Code           string `json:"code"`
	Stdin          string `json:"stdin,omitempty"`
	TimeoutSeconds int64  `json:"timeout_seconds,omitempty"`
	MemoryBytes    int64  `json:"memory_bytes,omitempty"`
}

// ExecuteResponse represents the JSON body of a code execution response.
type ExecuteResponse struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

// InfoResponse represents the JSON body of an info response.
type InfoResponse struct {
	Languages []sandbox.LanguageInfo `json:"languages"`
}
