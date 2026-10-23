package worker

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
)

var (
	// ErrQueueFull is returned when the worker pool's buffered channel is at maximum capacity.
	ErrQueueFull = errors.New("queue is full")

	// ErrPoolClosed is returned when attempting to submit a job to a shut-down pool.
	ErrPoolClosed = errors.New("worker pool is closed")
)

// OrderJob represents a dispatch unit of work submitted to the worker pool.
type OrderJob struct {
	OrderID uuid.UUID
}

// Dispatcher assigns an available delivery partner to an order.
type Dispatcher interface {
	Dispatch(ctx context.Context, order *domain.Order) (*domain.Partner, error)
}
