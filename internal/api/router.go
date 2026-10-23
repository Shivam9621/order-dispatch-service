package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shivam/order-dispatch/internal/assistant"
	"github.com/shivam/order-dispatch/internal/metrics"
	"github.com/shivam/order-dispatch/internal/store"
)

// RouterConfig contains dependencies for building the HTTP router.
type RouterConfig struct {
	Store            store.Store
	Enqueuer         OrderEnqueuer
	AssistantHandler *assistant.Handler
	MetricsCollector *metrics.Collector
}

// NewRouter configures middleware and API routes using chi.
func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(StructuredLogger)
	r.Use(middleware.Recoverer)

	// Health check endpoint
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Metrics endpoint (optional interview feature)
	if cfg.MetricsCollector != nil {
		r.Get("/metrics", cfg.MetricsCollector.Handler())
	}

	orderHandler := NewOrderHandler(cfg.Store, cfg.Enqueuer)

	// Order routes (per-request 5s timeout)
	r.Route("/orders", func(r chi.Router) {
		r.Use(RequestTimeoutMiddleware(5 * time.Second))

		r.Post("/", orderHandler.CreateOrder)
		r.Get("/", orderHandler.ListOrders)
		r.Get("/{id}", orderHandler.GetOrder)
		r.Post("/{id}/cancel", orderHandler.CancelOrder)
	})

	// AI Assistant route (15s bounded timeout to accommodate 10s LLM client timeout)
	if cfg.AssistantHandler != nil {
		r.Route("/assistant", func(r chi.Router) {
			r.Use(RequestTimeoutMiddleware(15 * time.Second))
			r.Post("/", cfg.AssistantHandler.HandleAssistant)
		})
	}

	return r
}
