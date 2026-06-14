package api

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

// LimitsResponse is the per-language resource limits in an info response.
type LimitsResponse struct {
	MinTimeoutSeconds int64   `json:"min_timeout_seconds"`
	MaxTimeoutSeconds int64   `json:"max_timeout_seconds"`
	MinMemoryBytes    int64   `json:"min_memory_bytes"`
	MaxMemoryBytes    int64   `json:"max_memory_bytes"`
	MaxPids           int64   `json:"max_pids"`
	MaxCPUs           float64 `json:"max_cpus"`
}

// LanguageInfoResponse describes a supported language in an info response.
type LanguageInfoResponse struct {
	Name           string         `json:"name"`
	DefaultVersion string         `json:"default_version"`
	Versions       []string       `json:"versions"`
	Filename       string         `json:"filename"`
	Limits         LimitsResponse `json:"limits"`
}

// WorkspaceLimitsResponse is the global workspace-file limits in an info response.
type WorkspaceLimitsResponse struct {
	MaxFiles         int `json:"max_files"`
	MaxFileSizeBytes int `json:"max_file_size_bytes"`
}

// InfoResponse represents the JSON body of an info response.
type InfoResponse struct {
	Languages []LanguageInfoResponse  `json:"languages"`
	Workspace WorkspaceLimitsResponse `json:"workspace"`
}
