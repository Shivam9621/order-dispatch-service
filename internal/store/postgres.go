package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivam/order-dispatch/internal/domain"
)

// PostgresStore implements the Store interface using PostgreSQL via pgxpool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgresStore with the provided connection pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// CreateOrder persists a newly placed order into PostgreSQL.
func (s *PostgresStore) CreateOrder(ctx context.Context, order *domain.Order) error {
	itemsJSON, err := json.Marshal(order.Items)
	if err != nil {
		return fmt.Errorf("marshaling order items: %w", err)
	}

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

	query := `
		INSERT INTO orders (id, customer_id, items, total_paise, lat, lng, status, partner_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err = s.pool.Exec(ctx, query,
		order.ID,
		order.CustomerID,
		itemsJSON,
		order.TotalPaise,
		order.Lat,
		order.Lng,
		string(order.Status),
		order.PartnerID,
		order.CreatedAt,
		order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting order %s: %w", order.ID, err)
	}

	return nil
}

// GetOrder retrieves an order by its ID.
func (s *PostgresStore) GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	query := `
		SELECT id, customer_id, items, total_paise, lat, lng, status, partner_id, created_at, updated_at
		FROM orders
		WHERE id = $1
	`
	var (
		ord       domain.Order
		itemsJSON []byte
		statusStr string
	)

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&ord.ID,
		&ord.CustomerID,
		&itemsJSON,
		&ord.TotalPaise,
		&ord.Lat,
		&ord.Lng,
		&statusStr,
		&ord.PartnerID,
		&ord.CreatedAt,
		&ord.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying order %s: %w", id, err)
	}

	if err := json.Unmarshal(itemsJSON, &ord.Items); err != nil {
		return nil, fmt.Errorf("unmarshaling order items for %s: %w", id, err)
	}
	ord.Status = domain.OrderStatus(statusStr)

	return &ord, nil
}

// ListOrders retrieves orders, optionally filtered by customerID.
func (s *PostgresStore) ListOrders(ctx context.Context, customerID string, limit int) ([]domain.Order, error) {
	if limit <= 0 {
		limit = 50
	}

	var (
		rows pgx.Rows
		err  error
	)

	if customerID != "" {
		query := `
			SELECT id, customer_id, items, total_paise, lat, lng, status, partner_id, created_at, updated_at
			FROM orders
			WHERE customer_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		`
		rows, err = s.pool.Query(ctx, query, customerID, limit)
	} else {
		query := `
			SELECT id, customer_id, items, total_paise, lat, lng, status, partner_id, created_at, updated_at
			FROM orders
			ORDER BY created_at DESC
			LIMIT $1
		`
		rows, err = s.pool.Query(ctx, query, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("querying orders: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var (
			ord       domain.Order
			itemsJSON []byte
			statusStr string
		)
		if err := rows.Scan(
			&ord.ID,
			&ord.CustomerID,
			&itemsJSON,
			&ord.TotalPaise,
			&ord.Lat,
			&ord.Lng,
			&statusStr,
			&ord.PartnerID,
			&ord.CreatedAt,
			&ord.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning order row: %w", err)
		}

		if err := json.Unmarshal(itemsJSON, &ord.Items); err != nil {
			return nil, fmt.Errorf("unmarshaling order items: %w", err)
		}
		ord.Status = domain.OrderStatus(statusStr)
		orders = append(orders, ord)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating order rows: %w", err)
	}

	return orders, nil
}

// ListOrdersByStatus retrieves orders matching any of the provided statuses.
func (s *PostgresStore) ListOrdersByStatus(ctx context.Context, statuses ...domain.OrderStatus) ([]domain.Order, error) {
	if len(statuses) == 0 {
		return []domain.Order{}, nil
	}

	statusStrs := make([]string, len(statuses))
	for i, st := range statuses {
		statusStrs[i] = string(st)
	}

	query := `
		SELECT id, customer_id, items, total_paise, lat, lng, status, partner_id, created_at, updated_at
		FROM orders
		WHERE status = ANY($1)
		ORDER BY created_at ASC
	`
	rows, err := s.pool.Query(ctx, query, statusStrs)
	if err != nil {
		return nil, fmt.Errorf("querying orders by status: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var (
			ord       domain.Order
			itemsJSON []byte
			statusStr string
		)
		if err := rows.Scan(
			&ord.ID,
			&ord.CustomerID,
			&itemsJSON,
			&ord.TotalPaise,
			&ord.Lat,
			&ord.Lng,
			&statusStr,
			&ord.PartnerID,
			&ord.CreatedAt,
			&ord.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning order row: %w", err)
		}

		if err := json.Unmarshal(itemsJSON, &ord.Items); err != nil {
			return nil, fmt.Errorf("unmarshaling order items: %w", err)
		}
		ord.Status = domain.OrderStatus(statusStr)
		orders = append(orders, ord)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating orders by status: %w", err)
	}

	return orders, nil
}

// UpdateOrderStatus transitions an order to a new status within a transaction.
// Row-level lock (FOR UPDATE) is acquired to prevent concurrent conflicting state transitions.
func (s *PostgresStore) UpdateOrderStatus(ctx context.Context, id uuid.UUID, status domain.OrderStatus, partnerID *uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning update transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentStatusStr string
	err = tx.QueryRow(ctx, "SELECT status FROM orders WHERE id = $1 FOR UPDATE", id).Scan(&currentStatusStr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("fetching current status for order %s: %w", id, err)
	}

	currentStatus := domain.OrderStatus(currentStatusStr)
	if !currentStatus.CanTransitionTo(status) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, currentStatus, status)
	}

	now := time.Now().UTC()
	if partnerID != nil {
		_, err = tx.Exec(ctx,
			"UPDATE orders SET status = $1, partner_id = $2, updated_at = $3 WHERE id = $4",
			string(status), partnerID, now, id,
		)
	} else {
		_, err = tx.Exec(ctx,
			"UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3",
			string(status), now, id,
		)
	}
	if err != nil {
		return fmt.Errorf("updating order %s: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing order update for %s: %w", id, err)
	}

	return nil
}

// ListAvailablePartners retrieves all partners where available is true.
func (s *PostgresStore) ListAvailablePartners(ctx context.Context) ([]domain.Partner, error) {
	query := `
		SELECT id, name, lat, lng, available
		FROM partners
		WHERE available = true
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying available partners: %w", err)
	}
	defer rows.Close()

	var partners []domain.Partner
	for rows.Next() {
		var p domain.Partner
		if err := rows.Scan(&p.ID, &p.Name, &p.Lat, &p.Lng, &p.Available); err != nil {
			return nil, fmt.Errorf("scanning partner row: %w", err)
		}
		partners = append(partners, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating partners: %w", err)
	}

	return partners, nil
}

// MarkPartnerBusy atomically claims a partner:
// UPDATE partners SET available=false WHERE id=$1 AND available=true
// Returning whether a row was updated ensures at most one worker claims a partner.
func (s *PostgresStore) MarkPartnerBusy(ctx context.Context, id uuid.UUID) (bool, error) {
	query := `UPDATE partners SET available = false WHERE id = $1 AND available = true`
	tag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("marking partner busy %s: %w", id, err)
	}

	// RowsAffected will be 1 if we successfully claimed the partner, 0 if already claimed by someone else
	return tag.RowsAffected() > 0, nil
}

// MarkPartnerFree marks a partner as available again.
func (s *PostgresStore) MarkPartnerFree(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE partners SET available = true WHERE id = $1`
	tag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("marking partner free %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
