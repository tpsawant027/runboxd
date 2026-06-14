// Package sandbox defines the isolation boundary for running untrusted code.
package sandbox

import (
	"context"
	"time"
)

// Status classifies the outcome of an execution.
type Status string

const (
	StatusOK            Status = "ok"             // ran to completion, exit 0
	StatusRuntimeError  Status = "runtime_error"  // ran, exited non-zero
	StatusTimeout       Status = "timeout"        // exceeded wall-clock limit, killed
	StatusOOM           Status = "oom"            // exceeded memory limit, killed
	StatusInternalError Status = "internal_error" // the sandbox itself failed
	StatusCompileError  Status = "compile_error"  // failed to compile the code (if applicable)
)

// WorkspaceFile represents a file in the sandbox's workspace.
type WorkspaceFile struct {
	Path    string
	Content string
}

// RunSpec is a single execution request handed to a Sandbox.
type RunSpec struct {
	Language       string
	Version        string
	Code           string
	WorkspaceFiles []WorkspaceFile
	Stdin          string
	Timeout        time.Duration
	MemoryBytes    int64
}

// RunResult is the outcome of running code in a Sandbox.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Status   Status
	Duration time.Duration
}

// SandboxInfo provides information about the sandbox's capabilities.
type SandboxInfo struct {
	Languages []LanguageInfo
}

// LanguageInfo describes a programming language supported by the sandbox, including its versions and resource limits.
type LanguageInfo struct {
	Name           string
	DefaultVersion string
	Versions       []string
	Filename       string
	Limits         LangLimits
}

type LangLimits struct {
	MinTimeout, MaxTimeout         time.Duration
	MinMemoryBytes, MaxMemoryBytes int64
	MaxPids                        int64
	MaxCPUs                        float64
}

type LangCompileLimits struct {
	MemoryBytes int64
	Timeout     time.Duration
	MaxPids     int64
	MaxCPUs     float64
}

type LangSpec struct {
	Filename string
	Limits   LangLimits
}

// Sandbox isolates and runs untrusted code.
type Sandbox interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
	LangSpec(language, version string) (LangSpec, error)
	Close() error
}

// Pinger is an optional interface that a Sandbox can implement to allow health checks.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Informer is an optional interface that a Sandbox can implement to provide information about supported languages, versions, etc.
type Informer interface {
	Info(ctx context.Context) (SandboxInfo, error)
}

// Reaper is an optional interface for backends that accumulate orphaned
// resources needing periodic cleanup (DockerSandbox's leaked containers).
// Backends with no such resources (NsjailSandbox) don't implement it.
type Reaper interface {
	ReapOrphans(ctx context.Context)
}
