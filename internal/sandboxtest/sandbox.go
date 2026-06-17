package sandboxtest

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/tpsawant027/runboxd/internal/config"
	"github.com/tpsawant027/runboxd/internal/sandbox"
)

func NewTestSandbox(t *testing.T, backend string) sandbox.Sandbox {
	t.Helper()
	cfg := sandbox.GetTestSandboxCfg(backend)
	sb, err := sandbox.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func NewTestSandboxFromEnv(t *testing.T) sandbox.Sandbox {
	t.Helper()
	sbBackend := os.Getenv(config.SandboxBackendEnv)
	if sbBackend == "" {
		sbBackend = "docker"
	}
	return NewTestSandbox(t, sbBackend)
}

func SkipUnsupportedOnNsjail(t *testing.T, lang string) {
	t.Helper()
	if r := sandbox.UnsupportedOnNsjailReason(lang); r != "" {
		t.Skip(r)
	}
}
