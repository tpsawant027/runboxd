package config

import "os"

type Config struct {
	Port         string
	RegistryPath string
}

func Load() *Config {
	return &Config{
		Port:         getEnvOrDefault("PORT", "8080"),
		RegistryPath: getEnvOrDefault("REGISTRY_PATH", "./language_registry.yml"),
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
