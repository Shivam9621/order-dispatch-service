package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
)

// ErrQueueFull is returned when the worker pool or job queue is at maximum capacity.
var ErrQueueFull = errors.New("queue is full")

// OrderEnqueuer submits orders to the asynchronous processing pool.
type OrderEnqueuer interface {
	Enqueue(ctx context.Context, orderID uuid.UUID) error
}

// OrderHandler handles HTTP requests for order operations.
type OrderHandler struct {
	store    store.Store
	enqueuer OrderEnqueuer
}

// NewOrderHandler creates a new OrderHandler.
func NewOrderHandler(s store.Store, enqueuer OrderEnqueuer) *OrderHandler {
	return &OrderHandler{
		store:    s,
		enqueuer: enqueuer,
	}
}

type CreateOrderItem struct {
	Name       string `json:"name"`
	Qty        int    `json:"qty"`
	PricePaise int64  `json:"price_paise"`
}

type CreateOrderRequest struct {
	CustomerID string            `json:"customer_id"`
	Items      []CreateOrderItem `json:"items"`
	Lat        float64           `json:"lat"`
	Lng        float64           `json:"lng"`
}

// CreateOrder handles POST /orders.
func (h *OrderHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var req CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.CustomerID) == "" {
		writeError(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "at least one item is required")
		return
	}

	var totalPaise int64
	domainItems := make([]domain.OrderItem, 0, len(req.Items))
	for _, item := range req.Items {
		if strings.TrimSpace(item.Name) == "" {
			writeError(w, http.StatusBadRequest, "item name is required")
			return
		}
		if item.Qty <= 0 {
			writeError(w, http.StatusBadRequest, "item qty must be greater than zero")
			return
		}
		if item.PricePaise <= 0 {
			writeError(w, http.StatusBadRequest, "item price_paise must be greater than zero")
			return
		}
		totalPaise += int64(item.Qty) * item.PricePaise
		domainItems = append(domainItems, domain.OrderItem{
			Name:       item.Name,
			Qty:        item.Qty,
			PricePaise: item.PricePaise,
		})
	}

	if req.Lat < -90 || req.Lat > 90 || req.Lng < -180 || req.Lng > 180 {
		writeError(w, http.StatusBadRequest, "invalid coordinates")
		return
	}

	order := &domain.Order{
		ID:         uuid.New(),
		CustomerID: req.CustomerID,
		Items:      domainItems,
		TotalPaise: totalPaise,
		Lat:        req.Lat,
		Lng:        req.Lng,
		Status:     domain.StatusPlaced,
	}

	if err := h.store.CreateOrder(r.Context(), order); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	if h.enqueuer != nil {
		if err := h.enqueuer.Enqueue(r.Context(), order.ID); err != nil {
			if errors.Is(err, ErrQueueFull) {
				writeError(w, http.StatusServiceUnavailable, "queue is full")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to enqueue order for processing")
			return
		}
	}

	writeJSON(w, http.StatusCreated, order)
}

// GetOrder handles GET /orders/{id}.
func (h *OrderHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := h.store.GetOrder(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retrieve order")
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// ListOrders handles GET /orders?customer_id=...&limit=...
func (h *OrderHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	limitStr := r.URL.Query().Get("limit")

	limit := 50
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit parameter")
			return
		}
		limit = l
	}

	orders, err := h.store.ListOrders(r.Context(), customerID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orders")
		return
	}
	if orders == nil {
		orders = []domain.Order{}
	}

	writeJSON(w, http.StatusOK, orders)
}

// CancelOrder handles POST /orders/{id}/cancel.
// Only allowed from PLACED, CONFIRMED, or ASSIGNED.
func (h *OrderHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := h.store.GetOrder(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retrieve order")
		return
	}

	if order.Status != domain.StatusPlaced &&
		order.Status != domain.StatusConfirmed &&
		order.Status != domain.StatusAssigned {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("order cannot be cancelled from status %s", order.Status))
		return
	}

	// Free the delivery partner if one was assigned
	if order.PartnerID != nil {
		_ = h.store.MarkPartnerFree(r.Context(), *order.PartnerID)
	}

	if err := h.store.UpdateOrderStatus(r.Context(), id, domain.StatusCancelled, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel order")
		return
	}

	updated, err := h.store.GetOrder(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload cancelled order")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}
