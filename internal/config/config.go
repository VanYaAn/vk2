package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config хранит конфигурацию приложения.
type Config struct {
	GRPCPort int
	LogLevel string
}

// Load загружает конфигурацию из переменных окружения или использует значения по умолчанию.
func Load() (*Config, error) {
	portStr := os.Getenv("GRPC_PORT")
	if portStr == "" {
		portStr = "50051" // Порт по умолчанию
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("неверный GRPC_PORT: %w", err)
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info" // Уровень логирования по умолчанию
	}

	return &Config{
		GRPCPort: port,
		LogLevel: logLevel,
	}, nil
}
