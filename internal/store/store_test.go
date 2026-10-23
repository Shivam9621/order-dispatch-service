package store

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_OrderLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	orderID := uuid.New()
	order := &domain.Order{
		ID:         orderID,
		CustomerID: "cust_123",
		Items: []domain.OrderItem{
			{Name: "Pizza", Qty: 2, PricePaise: 50000},
		},
		TotalPaise: 100000,
		Lat:        12.9716,
		Lng:        77.5946,
		Status:     domain.StatusPlaced,
	}

	// 1. Create order
	err := s.CreateOrder(ctx, order)
	require.NoError(t, err)

	// 2. Get order
	fetched, err := s.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, orderID, fetched.ID)
	assert.Equal(t, "cust_123", fetched.CustomerID)
	assert.Equal(t, domain.StatusPlaced, fetched.Status)
	assert.Len(t, fetched.Items, 1)

	// 3. Valid transition: PLACED -> CONFIRMED
	err = s.UpdateOrderStatus(ctx, orderID, domain.StatusConfirmed, nil)
	require.NoError(t, err)

	fetched, err = s.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, fetched.Status)

	// 4. Invalid transition: CONFIRMED -> DELIVERED (skipping ASSIGNED)
	err = s.UpdateOrderStatus(ctx, orderID, domain.StatusDelivered, nil)
	require.ErrorIs(t, err, ErrInvalidTransition)

	// 5. Valid transition: CONFIRMED -> ASSIGNED with partner
	partnerID := uuid.New()
	err = s.UpdateOrderStatus(ctx, orderID, domain.StatusAssigned, &partnerID)
	require.NoError(t, err)

	fetched, err = s.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusAssigned, fetched.Status)
	assert.NotNil(t, fetched.PartnerID)
	assert.Equal(t, partnerID, *fetched.PartnerID)

	// 6. Valid transition: ASSIGNED -> DELIVERED
	err = s.UpdateOrderStatus(ctx, orderID, domain.StatusDelivered, nil)
	require.NoError(t, err)

	fetched, err = s.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDelivered, fetched.Status)

	// 7. Cannot transition out of terminal state DELIVERED
	err = s.UpdateOrderStatus(ctx, orderID, domain.StatusCancelled, nil)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func TestStore_ListOrders(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	for i := 0; i < 5; i++ {
		custID := "cust_A"
		if i >= 3 {
			custID = "cust_B"
		}
		err := s.CreateOrder(ctx, &domain.Order{
			CustomerID: custID,
			TotalPaise: int64((i + 1) * 1000),
			Status:     domain.StatusPlaced,
		})
		require.NoError(t, err)
	}

	ordersA, err := s.ListOrders(ctx, "cust_A", 10)
	require.NoError(t, err)
	assert.Len(t, ordersA, 3)

	ordersB, err := s.ListOrders(ctx, "cust_B", 1)
	require.NoError(t, err)
	assert.Len(t, ordersB, 1)

	allOrders, err := s.ListOrders(ctx, "", 10)
	require.NoError(t, err)
	assert.Len(t, allOrders, 5)
}

func TestStore_AtomicPartnerClaim(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	partnerID := uuid.New()
	s.SeedPartners([]domain.Partner{
		{
			ID:        partnerID,
			Name:      "Bangalore Partner 1",
			Lat:       12.9716,
			Lng:       77.5946,
			Available: true,
		},
	})

	// Concurrent claims against the same partner:
	// exactly one goroutine must succeed and the others must fail
	const concurrentAttempts = 20
	var (
		wg           sync.WaitGroup
		successCount int
		mu           sync.Mutex
	)

	wg.Add(concurrentAttempts)
	for i := 0; i < concurrentAttempts; i++ {
		go func() {
			defer wg.Done()
			claimed, err := s.MarkPartnerBusy(ctx, partnerID)
			if err == nil && claimed {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, successCount, "exactly one concurrent claim must succeed")

	// Verify partner is no longer available in ListAvailablePartners
	available, err := s.ListAvailablePartners(ctx)
	require.NoError(t, err)
	assert.Empty(t, available)

	// Free partner and verify it can be claimed again
	err = s.MarkPartnerFree(ctx, partnerID)
	require.NoError(t, err)

	availableAfterFree, err := s.ListAvailablePartners(ctx)
	require.NoError(t, err)
	assert.Len(t, availableAfterFree, 1)

	claimedAgain, err := s.MarkPartnerBusy(ctx, partnerID)
	require.NoError(t, err)
	assert.True(t, claimedAgain)
}
