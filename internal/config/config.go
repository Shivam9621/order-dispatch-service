package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds environment configurations for the application.
type Config struct {
	Port        string
	DatabaseURL string
	WorkerCount int
	LLMAPIKey   string
	LLMModel    string
}

// Load reads configuration from environment variables, falling back to sensible defaults.
func Load() (*Config, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")

	workerCount := 4
	if workerCountStr := os.Getenv("WORKER_COUNT"); workerCountStr != "" {
		val, err := strconv.Atoi(workerCountStr)
		if err != nil || val <= 0 {
			return nil, fmt.Errorf("parsing WORKER_COUNT: %w", err)
		}
		workerCount = val
	}

	llmAPIKey := os.Getenv("LLM_API_KEY")
	llmModel := os.Getenv("LLM_MODEL")

	return &Config{
		Port:        port,
		DatabaseURL: dbURL,
		WorkerCount: workerCount,
		LLMAPIKey:   llmAPIKey,
		LLMModel:    llmModel,
	}, nil
}
