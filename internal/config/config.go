package config

import "os"

type Config struct {
	PORT         string
	DATABASE_URL string
}

func NewConfig() *Config {
	return &Config{
		PORT:         getEnv("PORT", "8080"),
		DATABASE_URL: getEnv("DATABASE_URL", "./data.db"),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
