package config

import "os"

type Config struct {
	PORT         string
	DATABASE_URL string
	RABBITMQ_URL string
	LOG_LEVEL    string
}

func NewConfig() *Config {
	return &Config{
		PORT:         getEnv("PORT", "8080"),
		DATABASE_URL: getEnv("DATABASE_URL", "./data.db"),
		RABBITMQ_URL: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		LOG_LEVEL:    getEnv("LOG_LEVEL", "INFO"),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
