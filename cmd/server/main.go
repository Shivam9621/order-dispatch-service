package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivam/order-dispatch/internal/api"
	"github.com/shivam/order-dispatch/internal/assistant"
	"github.com/shivam/order-dispatch/internal/config"
	"github.com/shivam/order-dispatch/internal/dispatch"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/metrics"
	"github.com/shivam/order-dispatch/internal/store"
	"github.com/shivam/order-dispatch/internal/worker"
)

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var s store.Store
	if cfg.DatabaseURL != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to postgres: %w", err)
		}
		defer pool.Close()
		s = store.NewPostgresStore(pool)
	} else {
		slog.Warn("DATABASE_URL not set; running with in-memory store and seeded partners")
		memStore := store.NewMemoryStore()
		// Seed 10 Bangalore partners for standalone execution
		memStore.SeedPartners([]domain.Partner{
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, Name: "Aarav Kumar (Indiranagar)", Lat: 12.9784, Lng: 77.6408, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, Name: "Bhavna Sharma (Koramangala)", Lat: 12.9352, Lng: 77.6245, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}, Name: "Chirag Patel (HSR Layout)", Lat: 12.9121, Lng: 77.6446, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4}, Name: "Divya Rao (Whitefield)", Lat: 12.9698, Lng: 77.7499, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 5}, Name: "Eshan Reddy (Bellandur)", Lat: 12.9304, Lng: 77.6784, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 6}, Name: "Farhan Ali (Jayanagar)", Lat: 12.9308, Lng: 77.5838, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7}, Name: "Gauri Joshi (JP Nagar)", Lat: 12.9063, Lng: 77.5857, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8}, Name: "Harish Nair (MG Road)", Lat: 12.9756, Lng: 77.6066, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9}, Name: "Ishita Verma (Malleshwaram)", Lat: 13.0031, Lng: 77.5643, Available: true},
			{ID: [16]byte{0xa0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10}, Name: "Jatin Das (Marathahalli)", Lat: 12.9591, Lng: 77.6974, Available: true},
		})
		s = memStore
	}

	// Initialize Haversine nearest-partner dispatcher
	dispatcher := dispatch.NewDispatcher(s, dispatch.Config{})
	workerPool := worker.NewPool(worker.Config{
		WorkerCount:   cfg.WorkerCount,
		QueueCapacity: 500,
	}, s, dispatcher)

	// Initialize Metrics Collector
	collector := metrics.NewCollector(cfg.WorkerCount, workerPool.QueueDepth)
	workerPool.SetMetricsRecorder(collector)

	// Crash recovery: Re-enqueue any orders left in PLACED or CONFIRMED state
	if _, err := worker.RecoverUnfinishedOrders(ctx, workerPool, s); err != nil {
		slog.Warn("crash recovery encountered error", "error", err)
	}

	// Initialize Assistant
	toolExecutor := assistant.NewToolExecutor(s)
	assistantClient := assistant.NewClient(assistant.Config{
		APIKey: cfg.LLMAPIKey,
		Model:  cfg.LLMModel,
	}, toolExecutor)
	assistantHandler := assistant.NewHandler(assistantClient)

	router := api.NewRouter(api.RouterConfig{
		Store:            s,
		Enqueuer:         workerPool,
		AssistantHandler: assistantHandler,
		MetricsCollector: collector,
	})

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	// Start HTTP server in a separate goroutine so it doesn't block signal listening
	go func() {
		slog.Info("server starting", "port", cfg.Port, "worker_count", cfg.WorkerCount)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("starting server: %w", err)
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		slog.Info("shutting down server and worker pool")

		// 1. First, stop accepting new HTTP connections
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown error", "error", err)
		}

		// 2. Next, gracefully drain and shutdown the worker pool
		if err := workerPool.Shutdown(shutdownCtx); err != nil {
			slog.Error("worker pool shutdown error", "error", err)
		}

		return nil
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Listen for termination signals to initiate clean shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("server fatal error", "error", err)
		os.Exit(1)
	}

	slog.Info("server exited cleanly")
}
