package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistant_DirectTextReply(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-api-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		resp := MessagesResponse{
			ID:         "msg_01",
			Type:       "message",
			Role:       "assistant",
			StopReason: "end_turn",
			Content: []ContentBlock{
				{Type: "text", Text: "Hello! How can I help you today?"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	memStore := store.NewMemoryStore()
	executor := NewToolExecutor(memStore)
	client := NewClient(Config{
		BaseURL: mockServer.URL,
		APIKey:  "test-api-key",
		Model:   "claude-3-5-sonnet",
	}, executor)

	reply, err := client.Chat(context.Background(), "cust_1", "Hello")
	require.NoError(t, err)
	assert.Equal(t, "Hello! How can I help you today?", reply)
}

func TestAssistant_ToolCallLoop_GetOrderStatus(t *testing.T) {
	memStore := store.NewMemoryStore()
	orderID := uuid.New()
	_ = memStore.CreateOrder(context.Background(), &domain.Order{
		ID:         orderID,
		CustomerID: "cust_alice",
		Status:     domain.StatusConfirmed,
		TotalPaise: 45000,
	})

	var requestCount int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		var req MessagesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if count == 1 {
			// First turn: assistant chooses to call get_order_status
			inputJSON, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
			resp := MessagesResponse{
				ID:         "msg_tool_1",
				Role:       "assistant",
				StopReason: "tool_use",
				Content: []ContentBlock{
					{
						Type:  "tool_use",
						ID:    "toolu_01",
						Name:  "get_order_status",
						Input: inputJSON,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second turn: assistant receives tool_result and generates text reply
		assert.Equal(t, int32(2), count)
		resp := MessagesResponse{
			ID:         "msg_final",
			Role:       "assistant",
			StopReason: "end_turn",
			Content: []ContentBlock{
				{Type: "text", Text: "Your order is currently CONFIRMED for 45000 paise."},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	executor := NewToolExecutor(memStore)
	client := NewClient(Config{
		BaseURL: mockServer.URL,
		APIKey:  "test-api-key",
	}, executor)

	reply, err := client.Chat(context.Background(), "cust_alice", "Where is my order?")
	require.NoError(t, err)
	assert.Equal(t, "Your order is currently CONFIRMED for 45000 paise.", reply)
	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
}

func TestAssistant_Security_OwnershipCheck(t *testing.T) {
	memStore := store.NewMemoryStore()

	// Bob's order
	bobsOrderID := uuid.New()
	_ = memStore.CreateOrder(context.Background(), &domain.Order{
		ID:         bobsOrderID,
		CustomerID: "cust_bob",
		Status:     domain.StatusPlaced,
	})

	executor := NewToolExecutor(memStore)

	t.Run("cannot get status of another customer's order", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{"order_id": bobsOrderID.String()})
		// Alice tries to access Bob's order
		result, err := executor.Execute(context.Background(), "cust_alice", "get_order_status", input)
		require.NoError(t, err)
		assert.Contains(t, result, "unauthorized: order does not belong to this customer")
	})

	t.Run("cannot cancel another customer's order", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{"order_id": bobsOrderID.String()})
		// Alice tries to cancel Bob's order
		result, err := executor.Execute(context.Background(), "cust_alice", "cancel_order", input)
		require.NoError(t, err)
		assert.Contains(t, result, "unauthorized: order does not belong to this customer")

		// Verify order remains PLACED
		ord, err := memStore.GetOrder(context.Background(), bobsOrderID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPlaced, ord.Status)
	})

	t.Run("list_recent_orders always forces authenticated customer", func(t *testing.T) {
		// Model passes bob's ID, but executor must strictly use alice's ID
		input, _ := json.Marshal(map[string]any{"customer_id": "cust_bob", "limit": 10})
		result, err := executor.Execute(context.Background(), "cust_alice", "list_recent_orders", input)
		require.NoError(t, err)
		assert.Equal(t, "[]", result, "Alice has no orders, so result should be empty despite query trying bob")
	})
}

func TestAssistant_IterationCap(t *testing.T) {
	var iterations int32

	// Mock server that loops tool_use forever
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&iterations, 1)

		inputJSON, _ := json.Marshal(map[string]string{"customer_id": "cust_loop"})
		resp := MessagesResponse{
			ID:         "msg_loop",
			Role:       "assistant",
			StopReason: "tool_use",
			Content: []ContentBlock{
				{
					Type:  "tool_use",
					ID:    "toolu_infinite",
					Name:  "list_recent_orders",
					Input: inputJSON,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	memStore := store.NewMemoryStore()
	executor := NewToolExecutor(memStore)
	client := NewClient(Config{
		BaseURL: mockServer.URL,
		APIKey:  "key",
	}, executor)

	_, err := client.Chat(context.Background(), "cust_loop", "Loop forever please")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded safety cap of 5 tool-use iterations")
	assert.Equal(t, int32(5), atomic.LoadInt32(&iterations))
}

func TestAssistant_HTTPHandler(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := MessagesResponse{
			Role:       "assistant",
			StopReason: "end_turn",
			Content: []ContentBlock{
				{Type: "text", Text: "I can help you cancel or track orders!"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	memStore := store.NewMemoryStore()
	executor := NewToolExecutor(memStore)
	client := NewClient(Config{
		BaseURL: mockServer.URL,
		APIKey:  "key",
	}, executor)
	handler := NewHandler(client)

	t.Run("valid request returns 200 with reply", func(t *testing.T) {
		body := `{"customer_id":"cust_web","message":"Hi"}`
		req := httptest.NewRequest(http.MethodPost, "/assistant", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		handler.HandleAssistant(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp AssistantResponse
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "I can help you cancel or track orders!", resp.Reply)
	})

	t.Run("missing customer_id returns 400", func(t *testing.T) {
		body := `{"customer_id":"","message":"Hi"}`
		req := httptest.NewRequest(http.MethodPost, "/assistant", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		handler.HandleAssistant(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing message returns 400", func(t *testing.T) {
		body := `{"customer_id":"cust_1","message":""}`
		req := httptest.NewRequest(http.MethodPost, "/assistant", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		handler.HandleAssistant(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
