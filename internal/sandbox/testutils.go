package sandbox

import (
	"os"

	"github.com/tpsawant027/runboxd/internal/config"
)

func GetTestSandboxCfg(backend string) SandboxConfig {
	if backend == "" {
		backend = os.Getenv(config.SandboxBackendEnv)
	}
	registryPath := os.Getenv(config.RegistryPathEnv)
	if registryPath == "" {
		registryPath = "../../language_registry.yml"
	}
	rootfsRoot := os.Getenv(config.RootfsPathEnv)
	if rootfsRoot == "" {
		rootfsRoot = "../../_rootfs"
	}
	nsjailPath := os.Getenv(config.NsjailPathEnv)

	switch backend {
	case "nsjail":
		return NsjailSandboxConfig{
			RegistryPath: registryPath,
			NsjailPath:   nsjailPath,
			RootfsRoot:   rootfsRoot,
		}
	case "docker":
		return DockerSandboxConfig{RegistryPath: registryPath}
	default:
		return DockerSandboxConfig{RegistryPath: registryPath}
	}
}

func UnsupportedOnNsjailReason(lang string) string {
	if os.Getenv(config.SandboxBackendEnv) != "nsjail" || cgroupsActive() {
		return ""
	}
	switch lang {
	case "java", "nodejs":
		return lang + " reserves large virtual AS; rlimit_as can't bound it - needs cgroup memory.max"
	}
	return ""
}
