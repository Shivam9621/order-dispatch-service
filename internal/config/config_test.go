package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("expected default Port '8080', got %q", cfg.Port)
	}
	if cfg.WorkerCount != 4 {
		t.Errorf("expected default WorkerCount 4, got %d", cfg.WorkerCount)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("expected empty DatabaseURL, got %q", cfg.DatabaseURL)
	}
}

func TestLoad_CustomEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("WORKER_COUNT", "8")
	t.Setenv("LLM_API_KEY", "secret-key")
	t.Setenv("LLM_MODEL", "claude-3-opus")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("expected Port '9090', got %q", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db" {
		t.Errorf("expected DatabaseURL, got %q", cfg.DatabaseURL)
	}
	if cfg.WorkerCount != 8 {
		t.Errorf("expected WorkerCount 8, got %d", cfg.WorkerCount)
	}
	if cfg.LLMAPIKey != "secret-key" {
		t.Errorf("expected LLMAPIKey 'secret-key', got %q", cfg.LLMAPIKey)
	}
	if cfg.LLMModel != "claude-3-opus" {
		t.Errorf("expected LLMModel 'claude-3-opus', got %q", cfg.LLMModel)
	}
}

func TestLoad_InvalidWorkerCount(t *testing.T) {
	os.Clearenv()
	t.Setenv("WORKER_COUNT", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid WORKER_COUNT, got nil")
	}

	t.Setenv("WORKER_COUNT", "-1")
	_, err = Load()
	if err == nil {
		t.Fatal("expected error for negative WORKER_COUNT, got nil")
	}
}
