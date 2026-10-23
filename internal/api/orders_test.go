package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEnqueuer struct {
	err error
}

func (m *mockEnqueuer) Enqueue(ctx context.Context, orderID uuid.UUID) error {
	return m.err
}

func TestOrdersAPI_CreateOrder(t *testing.T) {
	memStore := store.NewMemoryStore()
	router := NewRouter(RouterConfig{
		Store: memStore,
	})

	t.Run("successful order creation computes total and returns 201", func(t *testing.T) {
		body := `{
			"customer_id": "cust_101",
			"items": [
				{"name": "Margherita Pizza", "qty": 2, "price_paise": 35000},
				{"name": "Garlic Bread", "qty": 1, "price_paise": 15000}
			],
			"lat": 12.9716,
			"lng": 77.5946
		}`

		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		var resp domain.Order
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)

		assert.NotEqual(t, uuid.Nil, resp.ID)
		assert.Equal(t, "cust_101", resp.CustomerID)
		assert.Equal(t, domain.StatusPlaced, resp.Status)
		assert.Equal(t, int64(85000), resp.TotalPaise) // (2*35000) + (1*15000) = 85000
		assert.Len(t, resp.Items, 2)
	})

	t.Run("missing customer_id returns 400", func(t *testing.T) {
		body := `{"customer_id":"","items":[{"name":"Pancake","qty":1,"price_paise":100}],"lat":12.9,"lng":77.5}`
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		var errResp map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&errResp)
		assert.Contains(t, errResp["error"], "customer_id is required")
	})

	t.Run("empty items returns 400", func(t *testing.T) {
		body := `{"customer_id":"cust_1","items":[],"lat":12.9,"lng":77.5}`
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid item qty returns 400", func(t *testing.T) {
		body := `{"customer_id":"cust_1","items":[{"name":"Pasta","qty":0,"price_paise":100}],"lat":12.9,"lng":77.5}`
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("queue full returns 503", func(t *testing.T) {
		fullQueueRouter := NewRouter(RouterConfig{
			Store:    store.NewMemoryStore(),
			Enqueuer: &mockEnqueuer{err: ErrQueueFull},
		})

		body := `{"customer_id":"cust_1","items":[{"name":"Salad","qty":1,"price_paise":100}],"lat":12.9,"lng":77.5}`
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		fullQueueRouter.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

		var errResp map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&errResp)
		assert.Equal(t, "queue is full", errResp["error"])
	})
}

func TestOrdersAPI_GetOrder(t *testing.T) {
	memStore := store.NewMemoryStore()
	router := NewRouter(RouterConfig{
		Store: memStore,
	})

	orderID := uuid.New()
	err := memStore.CreateOrder(context.Background(), &domain.Order{
		ID:         orderID,
		CustomerID: "cust_test",
		TotalPaise: 20000,
		Status:     domain.StatusPlaced,
	})
	require.NoError(t, err)

	t.Run("existing order returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders/"+orderID.String(), nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp domain.Order
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, orderID, resp.ID)
		assert.Equal(t, "cust_test", resp.CustomerID)
	})

	t.Run("non-existent order returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders/"+uuid.New().String(), nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("invalid uuid returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders/invalid-uuid", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestOrdersAPI_ListOrders(t *testing.T) {
	memStore := store.NewMemoryStore()
	router := NewRouter(RouterConfig{
		Store: memStore,
	})

	for i := 0; i < 4; i++ {
		cust := "cust_1"
		if i == 3 {
			cust = "cust_2"
		}
		_ = memStore.CreateOrder(context.Background(), &domain.Order{
			CustomerID: cust,
			TotalPaise: int64((i + 1) * 1000),
			Status:     domain.StatusPlaced,
		})
	}

	t.Run("filter by customer_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders?customer_id=cust_1", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp []domain.Order
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Len(t, resp, 3)
	})

	t.Run("list with limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders?limit=2", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp []domain.Order
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Len(t, resp, 2)
	})

	t.Run("invalid limit returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/orders?limit=-5", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestOrdersAPI_CancelOrder(t *testing.T) {
	memStore := store.NewMemoryStore()
	router := NewRouter(RouterConfig{
		Store: memStore,
	})

	partnerID := uuid.New()
	memStore.SeedPartners([]domain.Partner{
		{ID: partnerID, Name: "Test Partner", Available: false},
	})

	assignedOrderID := uuid.New()
	_ = memStore.CreateOrder(context.Background(), &domain.Order{
		ID:         assignedOrderID,
		CustomerID: "cust_cancel_test",
		Status:     domain.StatusPlaced,
	})
	_ = memStore.UpdateOrderStatus(context.Background(), assignedOrderID, domain.StatusConfirmed, nil)
	_ = memStore.UpdateOrderStatus(context.Background(), assignedOrderID, domain.StatusAssigned, &partnerID)

	t.Run("cancel ASSIGNED order frees partner and returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/orders/"+assignedOrderID.String()+"/cancel", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp domain.Order
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, resp.Status)

		// Partner should now be freed and available
		partners, err := memStore.ListAvailablePartners(context.Background())
		require.NoError(t, err)
		assert.Len(t, partners, 1)
		assert.Equal(t, partnerID, partners[0].ID)
	})

	t.Run("cancel already CANCELLED order returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/orders/"+assignedOrderID.String()+"/cancel", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		var errResp map[string]string
		_ = json.NewDecoder(rr.Body).Decode(&errResp)
		assert.Contains(t, errResp["error"], "cannot be cancelled from status CANCELLED")
	})

	t.Run("cancel DELIVERED order returns 400", func(t *testing.T) {
		deliveredID := uuid.New()
		_ = memStore.CreateOrder(context.Background(), &domain.Order{
			ID:     deliveredID,
			Status: domain.StatusPlaced,
		})
		_ = memStore.UpdateOrderStatus(context.Background(), deliveredID, domain.StatusConfirmed, nil)
		_ = memStore.UpdateOrderStatus(context.Background(), deliveredID, domain.StatusAssigned, nil)
		_ = memStore.UpdateOrderStatus(context.Background(), deliveredID, domain.StatusDelivered, nil)

		req := httptest.NewRequest(http.MethodPost, "/orders/"+deliveredID.String()+"/cancel", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
