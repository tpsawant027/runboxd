package sandbox

import (
	"fmt"
	"math"
	"time"

	"github.com/tpsawant027/runboxd/internal/imagespec"
)

const (
	MaxPids                 = 100
	MinTimeout              = 1 * time.Second
	MaxTimeout              = 10 * time.Second
	MinMemoryBytes          = 64 * 1024 * 1024  // 64 MiB
	MaxMemoryBytes          = 256 * 1024 * 1024 // 256 MiB
	DefaultMaxCPUs          = 0.5
	MaxOutputBytes          = 1 * 1024 * 1024 // 1 MiB per stream
	DefaultWorkspaceSizeMiB = 10
	DefaultTmpSizeMiB       = 5
)

type LangLimits struct {
	MinTimeout, MaxTimeout         time.Duration
	MinMemoryBytes, MaxMemoryBytes int64
	MaxPids                        int64
	MaxCPUs                        float64
	WorkspaceSizeBytes             int64
	TmpSizeBytes                   int64
}

type LangCompileLimits struct {
	MemoryBytes        int64
	Timeout            time.Duration
	MaxPids            int64
	MaxCPUs            float64
	WorkspaceSizeBytes int64
	TmpSizeBytes       int64
}

// valueWithDefault returns defaultValue when value is the zero value of T.
// DO NOT use for fields where zero is a valid explicit value.
func valueWithDefault[T comparable](value, defaultValue T) T {
	var zero T
	if value == zero {
		return defaultValue
	}
	return value
}

// resolveLangLimits converts the registry's ergonomic limits (MiB/seconds, zero =
// unset) into resolved LangLimits, filling unset fields from the package-const
// defaults. Max is resolved first; an unset min defaults to the package floor but
// is clamped to never exceed the resolved max (so setting only a low max doesn't
// trip the min<=max check). An explicit min>max is left intact for validation.
func resolveLangLimits(l imagespec.Limits) LangLimits {
	maxTimeout := valueWithDefault(time.Duration(l.MaxTimeoutSeconds)*time.Second, MaxTimeout)
	maxMemory := valueWithDefault(int64(l.MaxMemoryMiB)*1024*1024, MaxMemoryBytes)

	minTimeout := time.Duration(l.MinTimeoutSeconds) * time.Second
	if minTimeout == 0 {
		minTimeout = min(MinTimeout, maxTimeout)
	}
	minMemory := int64(l.MinMemoryMiB) * 1024 * 1024
	if minMemory == 0 {
		minMemory = min(MinMemoryBytes, maxMemory)
	}

	return LangLimits{
		MinTimeout:         minTimeout,
		MaxTimeout:         maxTimeout,
		MinMemoryBytes:     minMemory,
		MaxMemoryBytes:     maxMemory,
		MaxPids:            valueWithDefault(int64(l.MaxPids), MaxPids),
		MaxCPUs:            valueWithDefault(l.MaxCPUs, DefaultMaxCPUs),
		WorkspaceSizeBytes: valueWithDefault(int64(l.WorkspaceSizeMiB), DefaultWorkspaceSizeMiB) * 1024 * 1024,
		TmpSizeBytes:       valueWithDefault(int64(l.TmpSizeMiB), DefaultTmpSizeMiB) * 1024 * 1024,
	}
}

func validateLangLimits(limits LangLimits) error {
	if limits.MinTimeout < 0 || limits.MaxTimeout < 0 {
		return fmt.Errorf("timeout limits must be non-negative")
	}
	if limits.MinMemoryBytes < 0 || limits.MaxMemoryBytes < 0 {
		return fmt.Errorf("memory limits must be non-negative")
	}
	if limits.MaxPids < 1 {
		return fmt.Errorf("MaxPids must be at least 1")
	}
	// MaxCPUs must be positive and finite. resolveLang*Limits already substitutes
	// the default for a zero, so this is a backstop: a 0 reaching the backend means
	// UNLIMITED (docker NanoCPUs = ceil(0) = 0), and NaN/+Inf slip past a bare
	// `<= 0` check yet convert to a garbage int64.
	if !(limits.MaxCPUs > 0) || math.IsInf(limits.MaxCPUs, 1) {
		return fmt.Errorf("MaxCPUs must be a positive, finite number")
	}

	if limits.WorkspaceSizeBytes <= 0 {
		return fmt.Errorf("WorkspaceSizeBytes must be positive")
	}

	if limits.TmpSizeBytes <= 0 {
		return fmt.Errorf("TmpSizeBytes must be positive")
	}

	if limits.MinTimeout > limits.MaxTimeout {
		return fmt.Errorf("MinTimeout cannot be greater than MaxTimeout")
	}
	if limits.MinMemoryBytes > limits.MaxMemoryBytes {
		return fmt.Errorf("MinMemoryBytes cannot be greater than MaxMemoryBytes")
	}

	return nil
}

func resolveLangCompileLimits(l imagespec.CompileLimits) LangCompileLimits {
	return LangCompileLimits{
		MemoryBytes:        valueWithDefault(int64(l.MemoryMiB)*1024*1024, MaxMemoryBytes),
		Timeout:            valueWithDefault(time.Duration(l.TimeoutSeconds)*time.Second, MaxTimeout),
		MaxPids:            valueWithDefault(int64(l.MaxPids), MaxPids),
		MaxCPUs:            valueWithDefault(l.MaxCPUs, DefaultMaxCPUs),
		WorkspaceSizeBytes: valueWithDefault(int64(l.WorkspaceSizeMiB), DefaultWorkspaceSizeMiB) * 1024 * 1024,
		TmpSizeBytes:       valueWithDefault(int64(l.TmpSizeMiB), DefaultTmpSizeMiB) * 1024 * 1024,
	}
}

func validateLangCompileLimits(limits LangCompileLimits) error {
	if limits.Timeout < 0 {
		return fmt.Errorf("compile timeout must be non-negative")
	}
	if limits.MemoryBytes < 0 {
		return fmt.Errorf("compile memory limit must be non-negative")
	}
	if limits.MaxPids < 1 {
		return fmt.Errorf("compile MaxPids must be at least 1")
	}
	// MaxCPUs must be positive and finite. resolveLang*Limits already substitutes
	// the default for a zero, so this is a backstop: a 0 reaching the backend means
	// UNLIMITED (docker NanoCPUs = ceil(0) = 0), and NaN/+Inf slip past a bare
	// `<= 0` check yet convert to a garbage int64.
	if !(limits.MaxCPUs > 0) || math.IsInf(limits.MaxCPUs, 1) {
		return fmt.Errorf("MaxCPUs must be a positive, finite number")
	}
	if limits.WorkspaceSizeBytes <= 0 {
		return fmt.Errorf("WorkspaceSizeBytes must be positive")
	}
	if limits.TmpSizeBytes <= 0 {
		return fmt.Errorf("TmpSizeBytes must be positive")
	}
	return nil
}
