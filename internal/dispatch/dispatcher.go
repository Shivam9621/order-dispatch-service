package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
)

// ErrNoPartnersAvailable indicates that all delivery partners are currently busy or unavailable.
var ErrNoPartnersAvailable = errors.New("no partners available")

// Config holds dispatch retry and backoff parameters.
type Config struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Dispatcher matches an order with the nearest available delivery partner.
type Dispatcher struct {
	store store.Store
	cfg   Config
}

// NewDispatcher creates a new Dispatcher.
func NewDispatcher(s store.Store, cfg Config) *Dispatcher {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 300 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}

	return &Dispatcher{
		store: s,
		cfg:   cfg,
	}
}

type partnerWithDistance struct {
	partner  domain.Partner
	distance float64
}

// Dispatch finds and atomically claims the nearest available partner for the given order.
// If all candidates are claimed by concurrent workers or no partners are available,
// it retries with exponential backoff up to MaxRetries before cancelling the order.
func (d *Dispatcher) Dispatch(ctx context.Context, order *domain.Order) (*domain.Partner, error) {
	for attempt := 0; attempt <= d.cfg.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("dispatch aborted by context: %w", ctx.Err())
		}

		// 1. Fetch all currently available partners
		available, err := d.store.ListAvailablePartners(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing available partners: %w", err)
		}

		if len(available) > 0 {
			// 2. Compute Haversine distance and sort by nearest first
			candidates := make([]partnerWithDistance, len(available))
			for i, p := range available {
				dist := Haversine(order.Lat, order.Lng, p.Lat, p.Lng)
				candidates[i] = partnerWithDistance{partner: p, distance: dist}
			}

			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].distance < candidates[j].distance
			})

			// 3. Atomically attempt to claim the nearest available partner.
			// If claim fails (another concurrent worker claimed them first), try the next nearest.
			for _, c := range candidates {
				claimed, err := d.store.MarkPartnerBusy(ctx, c.partner.ID)
				if err != nil {
					continue
				}
				if claimed {
					slog.Info("partner claimed for order",
						"order_id", order.ID,
						"partner_id", c.partner.ID,
						"distance_km", fmt.Sprintf("%.2f", c.distance),
						"attempt", attempt,
					)
					return &c.partner, nil
				}
			}
		}

		// If no partner was claimed and we reached the maximum retry attempts, mark the order cancelled
		if attempt == d.cfg.MaxRetries {
			slog.Warn("no partners available after retries; cancelling order",
				"order_id", order.ID,
				"attempts", attempt+1,
			)
			// Cancellation reason logged; order marked CANCELLED in store
			_ = d.store.UpdateOrderStatus(ctx, order.ID, domain.StatusCancelled, nil)
			return nil, fmt.Errorf("dispatching order %s: %w", order.ID, ErrNoPartnersAvailable)
		}

		// 4. Retry with exponential backoff
		backoff := d.cfg.InitialBackoff * time.Duration(1<<attempt)
		if backoff > d.cfg.MaxBackoff {
			backoff = d.cfg.MaxBackoff
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("dispatch backoff interrupted: %w", ctx.Err())
		case <-timer.C:
		}
	}

	return nil, ErrNoPartnersAvailable
}
