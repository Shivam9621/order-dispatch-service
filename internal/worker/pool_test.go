package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDispatcher struct {
	store store.Store
}

func (m *mockDispatcher) Dispatch(ctx context.Context, order *domain.Order) (*domain.Partner, error) {
	partners, err := m.store.ListAvailablePartners(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range partners {
		claimed, err := m.store.MarkPartnerBusy(ctx, p.ID)
		if err == nil && claimed {
			return &p, nil
		}
	}
	return nil, errors.New("no partners available")
}

func TestWorkerPool_ConcurrentJobsToDelivered(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Seed 10 partners
	var seedPartners []domain.Partner
	for i := 0; i < 10; i++ {
		seedPartners = append(seedPartners, domain.Partner{
			ID:        uuid.New(),
			Name:      "Partner",
			Lat:       12.9716,
			Lng:       77.5946,
			Available: true,
		})
	}
	memStore.SeedPartners(seedPartners)

	dispatcher := &mockDispatcher{store: memStore}

	// Fast durations for testing
	cfg := Config{
		WorkerCount:      8,
		QueueCapacity:    100,
		ConfirmDuration:  5 * time.Millisecond,
		DeliveryDuration: 10 * time.Millisecond,
	}

	pool := NewPool(cfg, memStore, dispatcher)

	// Create and submit 30 orders concurrently
	const totalOrders = 30
	var orderIDs []uuid.UUID

	for i := 0; i < totalOrders; i++ {
		ord := &domain.Order{
			ID:         uuid.New(),
			CustomerID: "cust_concurrent",
			Items:      []domain.OrderItem{{Name: "Item", Qty: 1, PricePaise: 1000}},
			TotalPaise: 1000,
			Status:     domain.StatusPlaced,
		}
		require.NoError(t, memStore.CreateOrder(ctx, ord))
		orderIDs = append(orderIDs, ord.ID)
	}

	var wg sync.WaitGroup
	wg.Add(totalOrders)
	for _, id := range orderIDs {
		orderID := id
		go func() {
			defer wg.Done()
			for {
				err := pool.Submit(OrderJob{OrderID: orderID})
				if err == nil {
					break
				}
				if errors.Is(err, ErrQueueFull) {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				break
			}
		}()
	}
	wg.Wait()

	// Wait for all orders to reach DELIVERED (or poll with timeout)
	assert.Eventually(t, func() bool {
		deliveredCount := 0
		for _, id := range orderIDs {
			ord, err := memStore.GetOrder(ctx, id)
			if err == nil && ord.Status == domain.StatusDelivered {
				deliveredCount++
			}
		}
		return deliveredCount == totalOrders
	}, 10*time.Second, 20*time.Millisecond, "all 30 orders should transition to DELIVERED")

	// Shutdown pool cleanly
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, pool.Shutdown(shutdownCtx))
}

func TestWorkerPool_SubmitQueueFull(t *testing.T) {
	memStore := store.NewMemoryStore()
	dispatcher := &mockDispatcher{store: memStore}

	// Tiny queue with 1 capacity and high sleep to simulate backlog
	cfg := Config{
		WorkerCount:      1,
		QueueCapacity:    1,
		ConfirmDuration:  500 * time.Millisecond,
		DeliveryDuration: 500 * time.Millisecond,
	}

	pool := NewPool(cfg, memStore, dispatcher)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	}()

	// First job fills worker or queue
	_ = pool.Submit(OrderJob{OrderID: uuid.New()})
	_ = pool.Submit(OrderJob{OrderID: uuid.New()})

	// Third job should encounter ErrQueueFull immediately
	err := pool.Submit(OrderJob{OrderID: uuid.New()})
	assert.ErrorIs(t, err, ErrQueueFull)
}

func TestWorkerPool_MidProcessingCancellationFreesPartner(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	partnerID := uuid.New()
	memStore.SeedPartners([]domain.Partner{
		{ID: partnerID, Name: "Delivery Partner", Available: true},
	})

	dispatcher := &mockDispatcher{store: memStore}

	cfg := Config{
		WorkerCount:      1,
		QueueCapacity:    10,
		ConfirmDuration:  5 * time.Millisecond,
		DeliveryDuration: 100 * time.Millisecond, // long delivery
	}

	pool := NewPool(cfg, memStore, dispatcher)
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pool.Shutdown(shCtx)
	}()

	orderID := uuid.New()
	ord := &domain.Order{
		ID:         orderID,
		CustomerID: "cust_cancel_mid",
		Status:     domain.StatusPlaced,
	}
	require.NoError(t, memStore.CreateOrder(ctx, ord))
	require.NoError(t, pool.Submit(OrderJob{OrderID: orderID}))

	// Wait until order reaches ASSIGNED
	assert.Eventually(t, func() bool {
		o, err := memStore.GetOrder(ctx, orderID)
		return err == nil && o.Status == domain.StatusAssigned
	}, 2*time.Second, 10*time.Millisecond)

	// Partner should currently be busy
	available, err := memStore.ListAvailablePartners(ctx)
	require.NoError(t, err)
	assert.Empty(t, available)

	// Simulate customer cancellation mid-delivery
	err = memStore.UpdateOrderStatus(ctx, orderID, domain.StatusCancelled, nil)
	require.NoError(t, err)

	// Worker should detect cancellation on next step and free partner
	assert.Eventually(t, func() bool {
		partners, err := memStore.ListAvailablePartners(ctx)
		return err == nil && len(partners) == 1
	}, 2*time.Second, 10*time.Millisecond, "partner should be freed after order cancellation")
}

func TestWorkerPool_ShutdownGraceful(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	partnerID := uuid.New()
	memStore.SeedPartners([]domain.Partner{
		{ID: partnerID, Name: "Partner", Available: true},
	})
	dispatcher := &mockDispatcher{store: memStore}

	cfg := Config{
		WorkerCount:      2,
		QueueCapacity:    10,
		ConfirmDuration:  10 * time.Millisecond,
		DeliveryDuration: 10 * time.Millisecond,
	}

	pool := NewPool(cfg, memStore, dispatcher)

	orderID := uuid.New()
	_ = memStore.CreateOrder(ctx, &domain.Order{
		ID:         orderID,
		CustomerID: "cust_shutdown",
		Status:     domain.StatusPlaced,
	})
	_ = pool.Submit(OrderJob{OrderID: orderID})

	// Shutdown pool
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := pool.Shutdown(shutdownCtx)
	require.NoError(t, err)

	// New submit should be rejected with ErrPoolClosed
	err = pool.Submit(OrderJob{OrderID: uuid.New()})
	assert.ErrorIs(t, err, ErrPoolClosed)

	// In-flight order should have completed
	ord, err := memStore.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDelivered, ord.Status)
}

func TestWorkerPool_CrashRecovery(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	partnerID := uuid.New()
	memStore.SeedPartners([]domain.Partner{
		{ID: partnerID, Name: "Partner", Available: true},
	})
	dispatcher := &mockDispatcher{store: memStore}

	// Create orders in PLACED, CONFIRMED, and DELIVERED states
	order1 := &domain.Order{ID: uuid.New(), Status: domain.StatusPlaced}
	order2 := &domain.Order{ID: uuid.New(), Status: domain.StatusConfirmed}
	order3 := &domain.Order{ID: uuid.New(), Status: domain.StatusDelivered}

	_ = memStore.CreateOrder(ctx, order1)
	_ = memStore.CreateOrder(ctx, order2)
	_ = memStore.CreateOrder(ctx, order3)

	cfg := Config{
		WorkerCount:      2,
		QueueCapacity:    10,
		ConfirmDuration:  5 * time.Millisecond,
		DeliveryDuration: 5 * time.Millisecond,
	}

	pool := NewPool(cfg, memStore, dispatcher)
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pool.Shutdown(shCtx)
	}()

	recovered, err := RecoverUnfinishedOrders(ctx, pool, memStore)
	require.NoError(t, err)
	assert.Equal(t, 2, recovered, "should recover exactly the 2 unfinished orders")

	assert.Eventually(t, func() bool {
		o1, _ := memStore.GetOrder(ctx, order1.ID)
		o2, _ := memStore.GetOrder(ctx, order2.ID)
		return o1.Status == domain.StatusDelivered && o2.Status == domain.StatusDelivered
	}, 2*time.Second, 10*time.Millisecond)
}
