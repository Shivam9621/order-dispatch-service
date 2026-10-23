package domain

import (
	"time"

	"github.com/google/uuid"
)

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	StatusPlaced    OrderStatus = "PLACED"
	StatusConfirmed OrderStatus = "CONFIRMED"
	StatusAssigned  OrderStatus = "ASSIGNED"
	StatusDelivered OrderStatus = "DELIVERED"
	StatusCancelled OrderStatus = "CANCELLED"
)

// CanTransitionTo enforces valid lifecycle state transitions.
// Transitions permitted:
//   PLACED -> CONFIRMED, CANCELLED
//   CONFIRMED -> ASSIGNED, CANCELLED
//   ASSIGNED -> DELIVERED, CANCELLED
// DELIVERED and CANCELLED are terminal states.
func (s OrderStatus) CanTransitionTo(next OrderStatus) bool {
	switch s {
	case StatusPlaced:
		return next == StatusConfirmed || next == StatusCancelled
	case StatusConfirmed:
		return next == StatusAssigned || next == StatusCancelled
	case StatusAssigned:
		return next == StatusDelivered || next == StatusCancelled
	default:
		return false
	}
}

// OrderItem represents a single item inside an order.
type OrderItem struct {
	Name       string `json:"name"`
	Qty        int    `json:"qty"`
	PricePaise int64  `json:"price_paise"`
}

// Order represents an order placed by a customer.
type Order struct {
	ID         uuid.UUID   `json:"id"`
	CustomerID string      `json:"customer_id"`
	Items      []OrderItem `json:"items"`
	TotalPaise int64       `json:"total_paise"`
	Lat        float64     `json:"lat"`
	Lng        float64     `json:"lng"`
	Status     OrderStatus `json:"status"`
	PartnerID  *uuid.UUID  `json:"partner_id,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}
