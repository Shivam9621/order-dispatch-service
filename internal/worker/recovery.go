package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
)

// RecoverUnfinishedOrders finds any orders still in PLACED or CONFIRMED state
// (e.g. from a previous server restart/crash) and re-enqueues them into the worker pool.
func RecoverUnfinishedOrders(ctx context.Context, pool *Pool, s store.Store) (int, error) {
	orders, err := s.ListOrdersByStatus(ctx, domain.StatusPlaced, domain.StatusConfirmed)
	if err != nil {
		return 0, fmt.Errorf("recovering unfinished orders: %w", err)
	}

	enqueued := 0
	for _, ord := range orders {
		if err := pool.Submit(OrderJob{OrderID: ord.ID}); err != nil {
			slog.Warn("failed to re-enqueue unfinished order", "order_id", ord.ID, "error", err)
			continue
		}
		enqueued++
	}

	slog.Info("crash recovery finished", "recovered_orders_count", enqueued)
	return enqueued, nil
}
