package sandbox

import (
	"errors"
	"log/slog"
)

var ErrUnsupportedSandboxConfig = errors.New("unsupported sandbox config type")

func New(cfg SandboxConfig, logger *slog.Logger) (Sandbox, error) {
	switch c := cfg.(type) {
	case DockerSandboxConfig:
		return NewDockerSandbox(c, logger)
	case NsjailSandboxConfig:
		return NewNsjailSandbox(c, logger)
	default:
		return nil, ErrUnsupportedSandboxConfig
	}
}
