//go:build integration || adversarial || stress

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

func newDockerTestSandbox(t *testing.T) *DockerSandbox {
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

func newNsjailTestSandbox(t *testing.T) *NsjailSandbox {
	t.Helper()
	registryPath := os.Getenv("REGISTRY_PATH")
	if registryPath == "" {
		registryPath = "../../language_registry.yml"
	}
	rootfsRoot := os.Getenv("ROOTFS_PATH")
	if rootfsRoot == "" {
		rootfsRoot = "../../_rootfs"
	}
	sb, err := NewNsjailSandbox(registryPath, os.Getenv("NSJAIL_PATH"), rootfsRoot, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skip("nsjail unavailable:", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func newTestSandbox(t *testing.T) Sandbox {
	t.Helper()
	switch os.Getenv(config.SandboxBackendEnv) {
	case "nsjail":
		return newNsjailTestSandbox(t)
	default:
		return newDockerTestSandbox(t)
	}
}

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

func skipUnsupportedOnNsjail(t *testing.T, lang string) {
	if os.Getenv(config.SandboxBackendEnv) != "nsjail" {
		return
	}
	if cgroupsActive() {
		return
	}
	switch lang {
	case "java", "nodejs":
		t.Skipf("%s reserves large virtual AS; rlimit_as can't bound it — needs cgroup memory.max", lang)
	}
}
