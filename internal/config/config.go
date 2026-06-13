package config

import (
	"os"
	"runtime"
	"strconv"
)

type Config struct {
	Port           string
	RegistryPath   string
	WorkerPoolSize int
	MaxQueueSize   int
	SandboxBackend string
	NsjailPath     string
	RootfsPath     string
	CgroupV2Mount  string
}

const (
	PortEnv           = "PORT"
	RegistryPathEnv   = "REGISTRY_PATH"
	WorkerPoolSizeEnv = "WORKER_POOL_SIZE"
	MaxQueueSizeEnv   = "MAX_QUEUE_SIZE"
	SandboxBackendEnv = "SANDBOX_BACKEND"
	NsjailPathEnv     = "NSJAIL_PATH"
	RootfsPathEnv     = "ROOTFS_PATH"
	CgroupV2MountEnv  = "CGROUP_V2_MOUNT"
)

func Load() *Config {
	return &Config{
		Port:           getEnvOrDefault(PortEnv, "8080"),
		RegistryPath:   getEnvOrDefault(RegistryPathEnv, "./language_registry.yml"),
		WorkerPoolSize: parseIntEnvOrDefault(WorkerPoolSizeEnv, runtime.NumCPU(), 1),
		MaxQueueSize:   parseIntEnvOrDefault(MaxQueueSizeEnv, runtime.NumCPU(), 0),
		SandboxBackend: getEnvOrDefault(SandboxBackendEnv, "docker"),
		NsjailPath:     getEnvOrDefault(NsjailPathEnv, ""),
		RootfsPath:     getEnvOrDefault(RootfsPathEnv, "./_rootfs"),
		CgroupV2Mount:  getEnvOrDefault(CgroupV2MountEnv, ""),
	}
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
