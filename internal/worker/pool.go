package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
)

// Config configures the worker pool concurrency and timing.
type Config struct {
	WorkerCount      int
	QueueCapacity    int
	ConfirmDuration  time.Duration
	DeliveryDuration time.Duration
}

// Pool coordinates a fixed pool of background worker goroutines processing orders.
type Pool struct {
	cfg        Config
	store      store.Store
	dispatcher Dispatcher

	// jobs is the buffered channel acting as the job queue.
	// Buffered channels decouple incoming request submission from worker execution.
	jobs chan OrderJob

	// wg tracks running worker goroutines ensuring graceful shutdown waits for all in-flight jobs.
	wg sync.WaitGroup

	// mu synchronizes access to isClosed to prevent panic from sending on a closed channel.
	mu       sync.RWMutex
	isClosed bool

	// ctx and cancel provide coordinated cancellation across all workers on shutdown timeout.
	ctx    context.Context
	cancel context.CancelFunc

	// metrics records atomic counters if configured.
	metrics MetricsRecorder
}

// MetricsRecorder defines the interface for collecting worker pool metrics.
type MetricsRecorder interface {
	IncSubmitted()
	IncDelivered()
	IncCancelled()
	IncActiveWorkers()
	DecActiveWorkers()
}

// NewPool initializes and starts a new worker pool.
func NewPool(cfg Config, s store.Store, dispatcher Dispatcher) *Pool {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 100
	}
	if cfg.ConfirmDuration == 0 {
		cfg.ConfirmDuration = 1 * time.Second
	}
	if cfg.DeliveryDuration == 0 {
		cfg.DeliveryDuration = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		cfg:        cfg,
		store:      s,
		dispatcher: dispatcher,
		jobs:       make(chan OrderJob, cfg.QueueCapacity),
		ctx:        ctx,
		cancel:     cancel,
	}

	p.start()
	return p
}

// start spawns N worker goroutines.
func (p *Pool) start() {
	// sync.WaitGroup is incremented for each worker goroutine before launch
	p.wg.Add(p.cfg.WorkerCount)
	for i := 0; i < p.cfg.WorkerCount; i++ {
		workerID := i + 1
		go p.workerLoop(workerID)
	}
}

// workerLoop is the main loop for each worker goroutine.
// Reading from the channel terminates automatically when p.jobs is closed and drained.
func (p *Pool) workerLoop(id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		p.processJob(p.ctx, job)
	}
}

// QueueDepth returns the current number of pending jobs in the buffered channel.
func (p *Pool) QueueDepth() int {
	return len(p.jobs)
}

// SetMetricsRecorder sets the metrics collector for the pool.
func (p *Pool) SetMetricsRecorder(rec MetricsRecorder) {
	p.metrics = rec
}

// Submit enqueues an order job into the buffer in a non-blocking manner.
// If the buffer is full, it immediately returns ErrQueueFull.
func (p *Pool) Submit(job OrderJob) error {
	p.mu.RLock()
	if p.isClosed {
		p.mu.RUnlock()
		return ErrPoolClosed
	}
	p.mu.RUnlock()

	// Non-blocking channel send via select with a default branch
	select {
	case p.jobs <- job:
		if p.metrics != nil {
			p.metrics.IncSubmitted()
		}
		return nil
	default:
		// Reject immediately when queue capacity is reached without blocking the HTTP request
		return ErrQueueFull
	}
}

// Enqueue satisfies the api.OrderEnqueuer interface.
func (p *Pool) Enqueue(ctx context.Context, orderID uuid.UUID) error {
	return p.Submit(OrderJob{OrderID: orderID})
}

// Shutdown gracefully terminates the worker pool:
// 1. Marks the pool as closed under mutex and closes p.jobs so no new jobs are accepted.
// 2. Waits for all workers to finish remaining in-flight jobs.
// 3. Cancels internal context if ctx timeout is exceeded.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.isClosed {
		p.mu.Unlock()
		return nil
	}
	p.isClosed = true
	// Closing channel signals workers to exit once the buffer is emptied
	close(p.jobs)
	p.mu.Unlock()

	// Wait for workers to drain and finish in a separate goroutine
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.cancel()
		return nil
	case <-ctx.Done():
		// Grace period exceeded: cancel internal context to interrupt long-running workers
		p.cancel()
		return fmt.Errorf("worker pool shutdown timed out: %w", ctx.Err())
	}
}

// processJob coordinates the pipeline: PLACED -> CONFIRMED -> ASSIGNED -> DELIVERED.
// It checks for context cancellation and customer cancellation at every transition step.
func (p *Pool) processJob(ctx context.Context, job OrderJob) {
	if p.metrics != nil {
		p.metrics.IncActiveWorkers()
		defer p.metrics.DecActiveWorkers()
	}

	order, err := p.store.GetOrder(ctx, job.OrderID)
	if err != nil {
		slog.Error("worker could not retrieve order", "order_id", job.OrderID, "error", err)
		return
	}

	// If already in a terminal state, nothing to do
	if order.Status == domain.StatusCancelled || order.Status == domain.StatusDelivered {
		return
	}

	var assignedPartnerID *uuid.UUID = order.PartnerID

	// 1. PLACED -> CONFIRMED
	if order.Status == domain.StatusPlaced {
		// Simulate initial kitchen/order confirmation work
		if !sleepWithContext(ctx, p.cfg.ConfirmDuration) {
			return
		}

		// Re-check order in store to see if customer cancelled during sleep
		current, err := p.store.GetOrder(ctx, order.ID)
		if err != nil || current.Status == domain.StatusCancelled {
			if p.metrics != nil {
				p.metrics.IncCancelled()
			}
			return
		}

		if err := p.store.UpdateOrderStatus(ctx, order.ID, domain.StatusConfirmed, nil); err != nil {
			slog.Warn("worker failed to confirm order", "order_id", order.ID, "error", err)
			return
		}
		order.Status = domain.StatusConfirmed
	}

	// 2. CONFIRMED -> ASSIGNED (call dispatcher)
	if order.Status == domain.StatusConfirmed {
		if ctx.Err() != nil {
			return
		}

		partner, err := p.dispatcher.Dispatch(ctx, order)
		if err != nil {
			slog.Warn("dispatch failed to assign partner", "order_id", order.ID, "error", err)
			if p.metrics != nil {
				p.metrics.IncCancelled()
			}
			return
		}

		assignedPartnerID = &partner.ID

		// Update order to ASSIGNED
		if err := p.store.UpdateOrderStatus(ctx, order.ID, domain.StatusAssigned, &partner.ID); err != nil {
			// If order was cancelled while dispatch was finding partner, immediately free the partner
			_ = p.store.MarkPartnerFree(ctx, partner.ID)
			if p.metrics != nil {
				p.metrics.IncCancelled()
			}
			return
		}
		order.Status = domain.StatusAssigned
		order.PartnerID = &partner.ID
	}

	// 3. ASSIGNED -> DELIVERED
	if order.Status == domain.StatusAssigned {
		// Simulate delivery transit time
		if !sleepWithContext(ctx, p.cfg.DeliveryDuration) {
			return
		}

		// Re-check if cancelled while out for delivery
		current, err := p.store.GetOrder(ctx, order.ID)
		if err != nil || current.Status == domain.StatusCancelled {
			if assignedPartnerID != nil {
				_ = p.store.MarkPartnerFree(ctx, *assignedPartnerID)
			}
			if p.metrics != nil {
				p.metrics.IncCancelled()
			}
			return
		}

		if err := p.store.UpdateOrderStatus(ctx, order.ID, domain.StatusDelivered, nil); err != nil {
			slog.Warn("worker failed to deliver order", "order_id", order.ID, "error", err)
			if assignedPartnerID != nil {
				_ = p.store.MarkPartnerFree(ctx, *assignedPartnerID)
			}
			return
		}
		order.Status = domain.StatusDelivered

		// Delivery completed successfully: free the partner for subsequent orders
		if assignedPartnerID != nil {
			_ = p.store.MarkPartnerFree(ctx, *assignedPartnerID)
		}
		if p.metrics != nil {
			p.metrics.IncDelivered()
		}
	}
}

// sleepWithContext pauses execution for duration d unless ctx is cancelled first.
// Using time.NewTimer with defer Stop ensures no timer resources leak if context cancels early.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
