package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresStore_Integration(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("cannot connect to Postgres at %s: %v", dbURL, err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ping to Postgres failed: %v", err)
	}

	// Apply migrations
	migrationFiles := []string{
		"../../migrations/001_create_tables.sql",
		"../../migrations/002_seed_partners.sql",
	}
	for _, file := range migrationFiles {
		content, err := os.ReadFile(file)
		if err == nil {
			_, _ = pool.Exec(ctx, string(content))
		}
	}

	pgStore := NewPostgresStore(pool)

	// Test partner atomic claim in PostgreSQL
	partners, err := pgStore.ListAvailablePartners(ctx)
	require.NoError(t, err)
	if len(partners) == 0 {
		t.Skip("no available partners in database")
	}

	targetPartner := partners[0]

	// Concurrent claim test against Postgres
	const concurrency = 10
	var (
		wg      sync.WaitGroup
		success int
		mu      sync.Mutex
	)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			claimed, claimErr := pgStore.MarkPartnerBusy(ctx, targetPartner.ID)
			if claimErr == nil && claimed {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, success, "exactly one concurrent worker must claim the partner in Postgres")

	// Free partner
	err = pgStore.MarkPartnerFree(ctx, targetPartner.ID)
	require.NoError(t, err)

	// Test Order lifecycle in PostgreSQL
	orderID := uuid.New()
	ord := &domain.Order{
		ID:         orderID,
		CustomerID: "cust_pg_test",
		Items: []domain.OrderItem{
			{Name: "Burger", Qty: 1, PricePaise: 25000},
		},
		TotalPaise: 25000,
		Lat:        12.9352,
		Lng:        77.6245,
		Status:     domain.StatusPlaced,
	}

	err = pgStore.CreateOrder(ctx, ord)
	require.NoError(t, err)

	fetched, err := pgStore.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, orderID, fetched.ID)
	assert.Equal(t, domain.StatusPlaced, fetched.Status)

	// Transition to CONFIRMED
	err = pgStore.UpdateOrderStatus(ctx, orderID, domain.StatusConfirmed, nil)
	require.NoError(t, err)

	// Invalid transition attempt
	err = pgStore.UpdateOrderStatus(ctx, orderID, domain.StatusDelivered, nil)
	require.ErrorIs(t, err, ErrInvalidTransition)
}
