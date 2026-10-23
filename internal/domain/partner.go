package domain

import "github.com/google/uuid"

// Partner represents a delivery partner.
type Partner struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Available bool      `json:"available"`
}
