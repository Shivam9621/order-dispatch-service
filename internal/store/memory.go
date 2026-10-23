package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
)

// MemoryStore provides a thread-safe in-memory implementation of the Store interface.
// It is useful for unit testing, handler testing without a live database, and rapid verification.
type MemoryStore struct {
	// mu protects concurrent access to orders and partners maps to prevent data races.
	mu       sync.RWMutex
	orders   map[uuid.UUID]*domain.Order
	partners map[uuid.UUID]*domain.Partner
}

// NewMemoryStore initializes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders:   make(map[uuid.UUID]*domain.Order),
		partners: make(map[uuid.UUID]*domain.Partner),
	}
}

// SeedPartners populates initial partners into the memory store.
func (m *MemoryStore) SeedPartners(partners []domain.Partner) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range partners {
		partnerCopy := p
		m.partners[p.ID] = &partnerCopy
	}
}

// CreateOrder stores a deep copy of an order under write lock.
func (m *MemoryStore) CreateOrder(ctx context.Context, order *domain.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if order.ID == uuid.Nil {
		order.ID = uuid.New()
	}
	now := time.Now().UTC()
	if order.CreatedAt.IsZero() {
		order.CreatedAt = now
	}
	if order.UpdatedAt.IsZero() {
		order.UpdatedAt = now
	}

	orderCopy := *order
	orderCopy.Items = make([]domain.OrderItem, len(order.Items))
	copy(orderCopy.Items, order.Items)

	m.orders[order.ID] = &orderCopy
	return nil
}

// GetOrder returns a copy of the order under read lock.
func (m *MemoryStore) GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ord, exists := m.orders[id]
	if !exists {
		return nil, ErrNotFound
	}

	orderCopy := *ord
	orderCopy.Items = make([]domain.OrderItem, len(ord.Items))
	copy(orderCopy.Items, ord.Items)
	return &orderCopy, nil
}

// ListOrders retrieves orders filtered by customerID sorted newest first.
func (m *MemoryStore) ListOrders(ctx context.Context, customerID string, limit int) ([]domain.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	var results []domain.Order
	for _, ord := range m.orders {
		if customerID == "" || ord.CustomerID == customerID {
			orderCopy := *ord
			orderCopy.Items = make([]domain.OrderItem, len(ord.Items))
			copy(orderCopy.Items, ord.Items)
			results = append(results, orderCopy)
		}
	}

	// Sort newest first
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// ListOrdersByStatus retrieves all orders matching any of the specified statuses.
func (m *MemoryStore) ListOrdersByStatus(ctx context.Context, statuses ...domain.OrderStatus) ([]domain.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statusMap := make(map[domain.OrderStatus]bool, len(statuses))
	for _, s := range statuses {
		statusMap[s] = true
	}

	var results []domain.Order
	for _, ord := range m.orders {
		if statusMap[ord.Status] {
			orderCopy := *ord
			orderCopy.Items = make([]domain.OrderItem, len(ord.Items))
			copy(orderCopy.Items, ord.Items)
			results = append(results, orderCopy)
		}
	}

	return results, nil
}

// UpdateOrderStatus transitions an order to a new status if valid.
// Uses a write lock to ensure atomic status checking and transition.
func (m *MemoryStore) UpdateOrderStatus(ctx context.Context, id uuid.UUID, status domain.OrderStatus, partnerID *uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ord, exists := m.orders[id]
	if !exists {
		return ErrNotFound
	}

	if !ord.Status.CanTransitionTo(status) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, ord.Status, status)
	}

	ord.Status = status
	ord.UpdatedAt = time.Now().UTC()
	if partnerID != nil {
		ord.PartnerID = partnerID
	}

	return nil
}

// ListAvailablePartners returns all partners whose available flag is true.
func (m *MemoryStore) ListAvailablePartners(ctx context.Context) ([]domain.Partner, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var available []domain.Partner
	for _, p := range m.partners {
		if p.Available {
			available = append(available, *p)
		}
	}
	return available, nil
}

// MarkPartnerBusy atomically checks if a partner is available and marks them busy.
// A write lock guarantees mutual exclusion so only one caller succeeds in claiming the partner.
func (m *MemoryStore) MarkPartnerBusy(ctx context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.partners[id]
	if !exists {
		return false, ErrNotFound
	}

	// If already claimed by another concurrent worker, claim fails
	if !p.Available {
		return false, nil
	}

	p.Available = false
	return true, nil
}

// MarkPartnerFree resets a partner's availability back to true.
func (m *MemoryStore) MarkPartnerFree(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.partners[id]
	if !exists {
		return ErrNotFound
	}

	p.Available = true
	return nil
}
