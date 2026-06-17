//go:build integration || adversarial

package sandbox

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/config"
)

const (
	testTimeout = 30 * time.Second
	exitCodeAny = -999
)

func ensureRunResult(t *testing.T, got, want RunResult, wantStderrContains string) {
	t.Helper()
	if got.Status != want.Status {
		t.Errorf("status = %q, want %q", got.Status, want.Status)
	}
	if got.Stdout != want.Stdout {
		t.Errorf("stdout = %q, want %q", got.Stdout, want.Stdout)
	}
	if want.ExitCode != exitCodeAny && got.ExitCode != want.ExitCode {
		t.Errorf("exit code = %d, want %d", got.ExitCode, want.ExitCode)
	}
	if wantStderrContains != "" {
		if !strings.Contains(got.Stderr, wantStderrContains) {
			t.Errorf("stderr = %q, want to contain %q", got.Stderr, wantStderrContains)
		}
	} else if got.Stderr != want.Stderr {
		t.Errorf("stderr = %q, want %q", got.Stderr, want.Stderr)
	}
}

func NewTestSandbox(t *testing.T, backend string) Sandbox {
	t.Helper()
	cfg := GetTestSandboxCfg(backend)
	sb, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func NewTestSandboxFromEnv(t *testing.T) Sandbox {
	t.Helper()
	sbBackend := os.Getenv(config.SandboxBackendEnv)
	if sbBackend == "" {
		sbBackend = "docker"
	}
	return NewTestSandbox(t, sbBackend)
}

func SkipUnsupportedOnNsjail(t *testing.T, lang string) {
	t.Helper()
	if r := UnsupportedOnNsjailReason(lang); r != "" {
		t.Skip(r)
	}
}
