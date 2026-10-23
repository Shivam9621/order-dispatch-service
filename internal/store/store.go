package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
)

var (
	// ErrNotFound is returned when the requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalidTransition is returned when an order transition is not allowed.
	ErrInvalidTransition = errors.New("invalid status transition")
)

// Store defines the data access contract for orders and delivery partners.
type Store interface {
	// CreateOrder persists a newly placed order.
	CreateOrder(ctx context.Context, order *domain.Order) error

	// GetOrder fetches an order by its unique ID.
	GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error)

	// ListOrders retrieves orders, optionally filtered by customerID, up to limit.
	ListOrders(ctx context.Context, customerID string, limit int) ([]domain.Order, error)

	// ListOrdersByStatus retrieves orders matching any of the provided statuses (used for crash recovery).
	ListOrdersByStatus(ctx context.Context, statuses ...domain.OrderStatus) ([]domain.Order, error)

	// UpdateOrderStatus updates an order's status and optionally assigns a delivery partner.
	UpdateOrderStatus(ctx context.Context, id uuid.UUID, status domain.OrderStatus, partnerID *uuid.UUID) error

	// ListAvailablePartners retrieves all partners currently marked as available.
	ListAvailablePartners(ctx context.Context) ([]domain.Partner, error)

	// MarkPartnerBusy atomically attempts to claim an available partner.
	// Returns true if the partner was claimed successfully, or false if already taken.
	MarkPartnerBusy(ctx context.Context, id uuid.UUID) (bool, error)

	// MarkPartnerFree resets a partner's availability back to true.
	MarkPartnerFree(ctx context.Context, id uuid.UUID) error
}
