package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOrderStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name     string
		current  OrderStatus
		next     OrderStatus
		expected bool
	}{
		// Transitions from PLACED
		{name: "PLACED to CONFIRMED is valid", current: StatusPlaced, next: StatusConfirmed, expected: true},
		{name: "PLACED to CANCELLED is valid", current: StatusPlaced, next: StatusCancelled, expected: true},
		{name: "PLACED to ASSIGNED is invalid", current: StatusPlaced, next: StatusAssigned, expected: false},
		{name: "PLACED to DELIVERED is invalid", current: StatusPlaced, next: StatusDelivered, expected: false},
		{name: "PLACED to PLACED is invalid", current: StatusPlaced, next: StatusPlaced, expected: false},

		// Transitions from CONFIRMED
		{name: "CONFIRMED to ASSIGNED is valid", current: StatusConfirmed, next: StatusAssigned, expected: true},
		{name: "CONFIRMED to CANCELLED is valid", current: StatusConfirmed, next: StatusCancelled, expected: true},
		{name: "CONFIRMED to DELIVERED is invalid", current: StatusConfirmed, next: StatusDelivered, expected: false},
		{name: "CONFIRMED to PLACED is invalid", current: StatusConfirmed, next: StatusPlaced, expected: false},
		{name: "CONFIRMED to CONFIRMED is invalid", current: StatusConfirmed, next: StatusConfirmed, expected: false},

		// Transitions from ASSIGNED
		{name: "ASSIGNED to DELIVERED is valid", current: StatusAssigned, next: StatusDelivered, expected: true},
		{name: "ASSIGNED to CANCELLED is valid", current: StatusAssigned, next: StatusCancelled, expected: true},
		{name: "ASSIGNED to PLACED is invalid", current: StatusAssigned, next: StatusPlaced, expected: false},
		{name: "ASSIGNED to CONFIRMED is invalid", current: StatusAssigned, next: StatusConfirmed, expected: false},
		{name: "ASSIGNED to ASSIGNED is invalid", current: StatusAssigned, next: StatusAssigned, expected: false},

		// Transitions from DELIVERED (terminal)
		{name: "DELIVERED to CANCELLED is invalid", current: StatusDelivered, next: StatusCancelled, expected: false},
		{name: "DELIVERED to PLACED is invalid", current: StatusDelivered, next: StatusPlaced, expected: false},
		{name: "DELIVERED to CONFIRMED is invalid", current: StatusDelivered, next: StatusConfirmed, expected: false},
		{name: "DELIVERED to ASSIGNED is invalid", current: StatusDelivered, next: StatusAssigned, expected: false},

		// Transitions from CANCELLED (terminal)
		{name: "CANCELLED to PLACED is invalid", current: StatusCancelled, next: StatusPlaced, expected: false},
		{name: "CANCELLED to CONFIRMED is invalid", current: StatusCancelled, next: StatusConfirmed, expected: false},
		{name: "CANCELLED to ASSIGNED is invalid", current: StatusCancelled, next: StatusAssigned, expected: false},
		{name: "CANCELLED to DELIVERED is invalid", current: StatusCancelled, next: StatusDelivered, expected: false},

		// Unknown / Invalid status
		{name: "UNKNOWN status is invalid", current: OrderStatus("UNKNOWN"), next: StatusPlaced, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.current.CanTransitionTo(tt.next)
			assert.Equal(t, tt.expected, result)
		})
	}
}
