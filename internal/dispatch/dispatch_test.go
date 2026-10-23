package dispatch

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
	"github.com/shivam/order-dispatch/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHaversine(t *testing.T) {
	t.Run("zero distance for identical coordinates", func(t *testing.T) {
		dist := Haversine(12.9716, 77.5946, 12.9716, 77.5946)
		assert.Equal(t, 0.0, dist)
	})

	t.Run("distance between Bangalore MG Road and Indiranagar (~3.7 km)", func(t *testing.T) {
		// MG Road: 12.9756, 77.6066
		// Indiranagar: 12.9784, 77.6408
		dist := Haversine(12.9756, 77.6066, 12.9784, 77.6408)
		assert.True(t, dist > 3.5 && dist < 4.0, "expected ~3.7 km, got %f", dist)
	})

	t.Run("long distance: New York to London (~5570 km)", func(t *testing.T) {
		nycLat, nycLng := 40.7128, -74.0060
		ldnLat, ldnLng := 51.5074, -0.1278
		dist := Haversine(nycLat, nycLng, ldnLat, ldnLng)
		assert.True(t, math.Abs(dist-5570) < 50, "expected ~5570 km, got %f", dist)
	})
}

func TestDispatcher_NearestSelection(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Order location: Bangalore central (12.9716, 77.5946)
	orderLat, orderLng := 12.9716, 77.5946

	// Partner 1: Far away (Whitefield ~17 km away)
	p1 := domain.Partner{
		ID:        uuid.New(),
		Name:      "Whitefield Partner (Far)",
		Lat:       12.9698,
		Lng:       77.7499,
		Available: true,
	}
	// Partner 2: Very close (MG Road ~1.4 km away)
	p2 := domain.Partner{
		ID:        uuid.New(),
		Name:      "MG Road Partner (Nearest)",
		Lat:       12.9756,
		Lng:       77.6066,
		Available: true,
	}
	// Partner 3: Medium distance (Koramangala ~5 km away)
	p3 := domain.Partner{
		ID:        uuid.New(),
		Name:      "Koramangala Partner (Medium)",
		Lat:       12.9352,
		Lng:       77.6245,
		Available: true,
	}

	memStore.SeedPartners([]domain.Partner{p1, p2, p3})

	disp := NewDispatcher(memStore, Config{
		MaxRetries:     1,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})

	order1 := &domain.Order{
		ID:         uuid.New(),
		CustomerID: "cust_1",
		Lat:        orderLat,
		Lng:        orderLng,
		Status:     domain.StatusConfirmed,
	}

	// First dispatch should pick nearest: p2 (MG Road)
	assigned1, err := disp.Dispatch(ctx, order1)
	require.NoError(t, err)
	assert.Equal(t, p2.ID, assigned1.ID)

	order2 := &domain.Order{
		ID:         uuid.New(),
		CustomerID: "cust_2",
		Lat:        orderLat,
		Lng:        orderLng,
		Status:     domain.StatusConfirmed,
	}

	// Second dispatch should pick next nearest: p3 (Koramangala) since p2 is now busy
	assigned2, err := disp.Dispatch(ctx, order2)
	require.NoError(t, err)
	assert.Equal(t, p3.ID, assigned2.ID)
}

func TestDispatcher_RetryAndCancellation(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Seed 0 partners
	disp := NewDispatcher(memStore, Config{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})

	order := &domain.Order{
		ID:         uuid.New(),
		CustomerID: "cust_no_partner",
		Lat:        12.9716,
		Lng:        77.5946,
		Status:     domain.StatusConfirmed,
	}
	require.NoError(t, memStore.CreateOrder(ctx, order))

	// Dispatch should exhaust retries and cancel the order
	partner, err := disp.Dispatch(ctx, order)
	assert.Nil(t, partner)
	assert.ErrorIs(t, err, ErrNoPartnersAvailable)

	// Order in store must now be CANCELLED
	updated, err := memStore.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, updated.Status)
}

// TrackingStore wraps MemoryStore to verify concurrent partner claims.
// It tracks concurrent active assignments for each partner to assert no partner is double-assigned.
type TrackingStore struct {
	*store.MemoryStore
	mu                sync.Mutex
	activeAssignments map[uuid.UUID]int
	doubleAssignCount int64
}

func newTrackingStore() *TrackingStore {
	return &TrackingStore{
		MemoryStore:       store.NewMemoryStore(),
		activeAssignments: make(map[uuid.UUID]int),
	}
}

func (ts *TrackingStore) MarkPartnerBusy(ctx context.Context, id uuid.UUID) (bool, error) {
	claimed, err := ts.MemoryStore.MarkPartnerBusy(ctx, id)
	if err == nil && claimed {
		ts.mu.Lock()
		ts.activeAssignments[id]++
		if ts.activeAssignments[id] > 1 {
			atomic.AddInt64(&ts.doubleAssignCount, 1)
		}
		ts.mu.Unlock()
	}
	return claimed, err
}

func (ts *TrackingStore) MarkPartnerFree(ctx context.Context, id uuid.UUID) error {
	ts.mu.Lock()
	if ts.activeAssignments[id] > 0 {
		ts.activeAssignments[id]--
	}
	ts.mu.Unlock()
	return ts.MemoryStore.MarkPartnerFree(ctx, id)
}

func TestDispatcher_Concurrency_50Orders_5Partners(t *testing.T) {
	ctx := context.Background()
	trackStore := newTrackingStore()

	// Seed 5 partners at Bangalore coordinates
	var partners []domain.Partner
	for i := 0; i < 5; i++ {
		partners = append(partners, domain.Partner{
			ID:        uuid.New(),
			Name:      "Partner",
			Lat:       12.9300 + float64(i)*0.01,
			Lng:       77.6000 + float64(i)*0.01,
			Available: true,
		})
	}
	trackStore.SeedPartners(partners)

	disp := NewDispatcher(trackStore, Config{
		MaxRetries:     5,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     30 * time.Millisecond,
	})

	workerPool := worker.NewPool(worker.Config{
		WorkerCount:      10,
		QueueCapacity:    100,
		ConfirmDuration:  5 * time.Millisecond,
		DeliveryDuration: 20 * time.Millisecond,
	}, trackStore, disp)

	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = workerPool.Shutdown(shCtx)
	}()

	const totalOrders = 50
	var orderIDs []uuid.UUID

	for i := 0; i < totalOrders; i++ {
		ord := &domain.Order{
			ID:         uuid.New(),
			CustomerID: "cust_stress",
			Lat:        12.9352,
			Lng:        77.6245,
			Status:     domain.StatusPlaced,
		}
		require.NoError(t, trackStore.CreateOrder(ctx, ord))
		orderIDs = append(orderIDs, ord.ID)
	}

	// Submit all 50 orders concurrently
	var wg sync.WaitGroup
	wg.Add(totalOrders)
	for _, id := range orderIDs {
		orderID := id
		go func() {
			defer wg.Done()
			for {
				err := workerPool.Submit(worker.OrderJob{OrderID: orderID})
				if err == nil {
					break
				}
				if err == worker.ErrQueueFull {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				break
			}
		}()
	}
	wg.Wait()

	// Wait for all 50 orders to reach terminal state (DELIVERED or CANCELLED)
	assert.Eventually(t, func() bool {
		completed := 0
		for _, id := range orderIDs {
			ord, err := trackStore.GetOrder(ctx, id)
			if err == nil && (ord.Status == domain.StatusDelivered || ord.Status == domain.StatusCancelled) {
				completed++
			}
		}
		return completed == totalOrders
	}, 15*time.Second, 20*time.Millisecond, "all 50 orders must complete")

	// Critical assertion: NO partner must ever have been assigned to two active orders simultaneously
	assert.Equal(t, int64(0), atomic.LoadInt64(&trackStore.doubleAssignCount),
		"no partner should ever be assigned to two active orders simultaneously")

	// Ensure all partners are freed after all deliveries finish
	availablePartners, err := trackStore.ListAvailablePartners(ctx)
	require.NoError(t, err)
	assert.Len(t, availablePartners, 5, "all 5 partners should be free and available again")
}

func TestDispatcher_Concurrency_100Orders_AllDelivered(t *testing.T) {
	ctx := context.Background()
	trackStore := newTrackingStore()

	// Seed 10 partners (matching Bangalore seed)
	var partners []domain.Partner
	for i := 0; i < 10; i++ {
		partners = append(partners, domain.Partner{
			ID:        uuid.New(),
			Name:      "Bangalore Partner",
			Lat:       12.9000 + float64(i)*0.01,
			Lng:       77.5800 + float64(i)*0.01,
			Available: true,
		})
	}
	trackStore.SeedPartners(partners)

	disp := NewDispatcher(trackStore, Config{
		MaxRetries:     10,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     15 * time.Millisecond,
	})

	workerPool := worker.NewPool(worker.Config{
		WorkerCount:      12,
		QueueCapacity:    200,
		ConfirmDuration:  2 * time.Millisecond,
		DeliveryDuration: 5 * time.Millisecond,
	}, trackStore, disp)

	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = workerPool.Shutdown(shCtx)
	}()

	const totalOrders = 100
	var orderIDs []uuid.UUID

	for i := 0; i < totalOrders; i++ {
		ord := &domain.Order{
			ID:         uuid.New(),
			CustomerID: "cust_100_dod",
			Lat:        12.9352,
			Lng:        77.6245,
			Status:     domain.StatusPlaced,
		}
		require.NoError(t, trackStore.CreateOrder(ctx, ord))
		orderIDs = append(orderIDs, ord.ID)
	}

	// Submit all 100 orders concurrently
	var wg sync.WaitGroup
	wg.Add(totalOrders)
	for _, id := range orderIDs {
		orderID := id
		go func() {
			defer wg.Done()
			for {
				err := workerPool.Submit(worker.OrderJob{OrderID: orderID})
				if err == nil {
					break
				}
				if err == worker.ErrQueueFull {
					time.Sleep(1 * time.Millisecond)
					continue
				}
				break
			}
		}()
	}
	wg.Wait()

	// Assert that all 100 orders successfully reach DELIVERED
	assert.Eventually(t, func() bool {
		delivered := 0
		for _, id := range orderIDs {
			ord, err := trackStore.GetOrder(ctx, id)
			if err == nil && ord.Status == domain.StatusDelivered {
				delivered++
			}
		}
		return delivered == totalOrders
	}, 15*time.Second, 20*time.Millisecond, "all 100 orders must reach DELIVERED")

	// Critical DoD assertion: zero double assignments
	assert.Equal(t, int64(0), atomic.LoadInt64(&trackStore.doubleAssignCount),
		"no partner should ever be assigned to two active orders simultaneously")

	// All 10 partners must be freed back to available
	available, err := trackStore.ListAvailablePartners(ctx)
	require.NoError(t, err)
	assert.Len(t, available, 10, "all 10 partners should be available after 100 deliveries")
}
