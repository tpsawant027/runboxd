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
}

func Load() *Config {
	return &Config{
		Port:           getEnvOrDefault("PORT", "8080"),
		RegistryPath:   getEnvOrDefault("REGISTRY_PATH", "./language_registry.yml"),
		WorkerPoolSize: parseIntEnvOrDefault("WORKER_POOL_SIZE", runtime.NumCPU(), 1),
		MaxQueueSize:   parseIntEnvOrDefault("MAX_QUEUE_SIZE", runtime.NumCPU(), 0),
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
