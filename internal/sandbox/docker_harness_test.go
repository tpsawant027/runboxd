//go:build integration || adversarial || stress

package sandbox

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

const testTimeout = 30 * time.Second

func newTestSandbox(t *testing.T) *DockerSandbox {
	t.Helper()
	registryPath := os.Getenv("REGISTRY_PATH")
	if registryPath == "" {
		registryPath = "../../language_registry.yml"
	}
	sb, err := NewDockerSandbox(registryPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skip("docker unavailable:", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func ensureRunResult(t *testing.T, got, want RunResult, wantStderrContains string) {
	t.Helper()
	if got.Status != want.Status {
		t.Errorf("status = %q, want %q", got.Status, want.Status)
	}
	if got.Stdout != want.Stdout {
		t.Errorf("stdout = %q, want %q", got.Stdout, want.Stdout)
	}
	if got.ExitCode != want.ExitCode {
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
