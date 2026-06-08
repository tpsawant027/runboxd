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
)

// RunSpec is a single execution request handed to a Sandbox.
type RunSpec struct {
	Language    string
	Version     string
	Code        string
	Stdin       string
	Timeout     time.Duration
	MemoryBytes int64
}

// RunResult is the outcome of running code in a Sandbox.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Status   Status
	Duration time.Duration
}

type LanguageInfo struct {
	Name           string
	DefaultVersion string
	Versions       []string
}

type SandboxInfo struct {
	Languages []LanguageInfo
}

// Sandbox isolates and runs untrusted code.
type Sandbox interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

// Pinger is an optional interface that a Sandbox can implement to allow health checks.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Informer is an optional interface that a Sandbox can implement to provide information about supported languages, versions, etc.
type Informer interface {
	Info(ctx context.Context) (SandboxInfo, error)
}
