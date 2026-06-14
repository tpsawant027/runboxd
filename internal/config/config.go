package config

import (
	"errors"
	"os"
	"runtime"
	"strconv"
)

type Config struct {
	Port                     string
	RegistryPath             string
	WorkerPoolSize           int
	MaxQueueSize             int
	SandboxBackend           string
	NsjailPath               string
	RootfsPath               string
	CgroupV2Mount            string
	AuthToken                string
	AuthAllowUnauthenticated bool
}

const (
	PortEnv                     = "PORT"
	RegistryPathEnv             = "REGISTRY_PATH"
	WorkerPoolSizeEnv           = "WORKER_POOL_SIZE"
	MaxQueueSizeEnv             = "MAX_QUEUE_SIZE"
	SandboxBackendEnv           = "SANDBOX_BACKEND"
	NsjailPathEnv               = "NSJAIL_PATH"
	RootfsPathEnv               = "ROOTFS_PATH"
	CgroupV2MountEnv            = "CGROUP_V2_MOUNT"
	AuthTokenEnv                = "AUTH_TOKEN"
	AuthAllowUnauthenticatedEnv = "AUTH_ALLOW_UNAUTHENTICATED"
)

var ErrAuthTokenRequired = errors.New("AUTH_TOKEN is required; set AUTH_ALLOW_UNAUTHENTICATED=true to run open (dev only)")

func Load() (*Config, error) {
	cfg := &Config{
		Port:                     getEnvOrDefault(PortEnv, "8080"),
		RegistryPath:             getEnvOrDefault(RegistryPathEnv, "./language_registry.yml"),
		WorkerPoolSize:           parseIntEnvOrDefault(WorkerPoolSizeEnv, runtime.NumCPU(), 1),
		MaxQueueSize:             parseIntEnvOrDefault(MaxQueueSizeEnv, runtime.NumCPU(), 0),
		SandboxBackend:           getEnvOrDefault(SandboxBackendEnv, "docker"),
		NsjailPath:               getEnvOrDefault(NsjailPathEnv, ""),
		RootfsPath:               getEnvOrDefault(RootfsPathEnv, "./_rootfs"),
		CgroupV2Mount:            getEnvOrDefault(CgroupV2MountEnv, ""),
		AuthToken:                getEnvOrDefault(AuthTokenEnv, ""),
		AuthAllowUnauthenticated: getEnvOrDefault(AuthAllowUnauthenticatedEnv, "false") == "true",
	}

	if cfg.AuthToken == "" && !cfg.AuthAllowUnauthenticated {
		return nil, ErrAuthTokenRequired
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func parseIntEnvOrDefault(key string, defaultValue, minValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil || value < minValue {
		return defaultValue
	}
	return value
}
