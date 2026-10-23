package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shivam/order-dispatch/internal/api"
	"github.com/shivam/order-dispatch/internal/store"
)

func TestHealthzEndpoint(t *testing.T) {
	router := api.NewRouter(api.RouterConfig{
		Store: store.NewMemoryStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	var response map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", response["status"])
	}
}
