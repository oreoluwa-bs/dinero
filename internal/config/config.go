package config

import (
	"os"
	"strconv"
)

type Config struct {
	PORT                string
	DATABASE_URL        string
	RABBITMQ_URL        string
	LOG_LEVEL           string
	OTLP_ENDPOINT       string
	PROVIDER_DELAY_MS   int
	PROVIDER_FAILURE_RATE float64
}

func NewConfig() *Config {
	return &Config{
		PORT:                  getEnv("PORT", "8080"),
		DATABASE_URL:          getEnv("DATABASE_URL", "./data.db"),
		RABBITMQ_URL:          getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		LOG_LEVEL:             getEnv("LOG_LEVEL", "INFO"),
		OTLP_ENDPOINT:         getEnv("OTLP_ENDPOINT", "localhost:4317"),
		PROVIDER_DELAY_MS:     getEnvInt("PROVIDER_DELAY_MS", 0),
		PROVIDER_FAILURE_RATE: getEnvFloat("PROVIDER_FAILURE_RATE", -1),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return fallback
}
