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
}

func Load() *Config {
	return &Config{
		Port:           getEnvOrDefault("PORT", "8080"),
		RegistryPath:   getEnvOrDefault("REGISTRY_PATH", "./language_registry.yml"),
		WorkerPoolSize: parseIntEnvOrDefault("WORKER_POOL_SIZE", runtime.NumCPU(), 1),
		MaxQueueSize:   parseIntEnvOrDefault("MAX_QUEUE_SIZE", runtime.NumCPU(), 0),
		SandboxBackend: getEnvOrDefault("SANDBOX_BACKEND", "docker"),
		NsjailPath:     getEnvOrDefault("NSJAIL_PATH", ""),
		RootfsPath:     getEnvOrDefault("ROOTFS_PATH", "./_rootfs"),
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
