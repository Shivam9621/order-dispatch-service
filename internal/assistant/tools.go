package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shivam/order-dispatch/internal/domain"
	"github.com/shivam/order-dispatch/internal/store"
)

// GetToolDefinitions returns the Anthropic-compatible tool schemas.
func GetToolDefinitions() []ToolSchema {
	return []ToolSchema{
		{
			Name:        "get_order_status",
			Description: "Get current status, total amount, and items for an order by its ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"order_id": map[string]any{
						"type":        "string",
						"description": "The UUID of the order to check.",
					},
				},
				"required": []string{"order_id"},
			},
		},
		{
			Name:        "list_recent_orders",
			Description: "List recent orders placed by the customer.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"customer_id": map[string]any{
						"type":        "string",
						"description": "The ID of the customer.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max number of orders to return (default 10).",
					},
				},
				"required": []string{"customer_id"},
			},
		},
		{
			Name:        "cancel_order",
			Description: "Cancel an order if it is in PLACED, CONFIRMED, or ASSIGNED status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"order_id": map[string]any{
						"type":        "string",
						"description": "The UUID of the order to cancel.",
					},
				},
				"required": []string{"order_id"},
			},
		},
	}
}

// ToolExecutor executes tool calls while enforcing strict customer ownership security.
type ToolExecutor struct {
	store store.Store
}

func NewToolExecutor(s store.Store) *ToolExecutor {
	return &ToolExecutor{store: s}
}

// Execute parses tool input, runs the appropriate store operation, and returns the result string.
// Security: Verifies that any targeted order strictly belongs to authenticated customerID.
func (te *ToolExecutor) Execute(ctx context.Context, customerID string, toolName string, rawInput json.RawMessage) (string, error) {
	switch toolName {
	case "get_order_status":
		var input struct {
			OrderID string `json:"order_id"`
		}
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return `{"error":"invalid tool arguments"}`, nil
		}

		orderID, err := uuid.Parse(strings.TrimSpace(input.OrderID))
		if err != nil {
			return `{"error":"invalid order_id UUID format"}`, nil
		}

		order, err := te.store.GetOrder(ctx, orderID)
		if err != nil {
			return `{"error":"order not found"}`, nil
		}

		// Security: verify order ownership
		if order.CustomerID != customerID {
			return `{"error":"unauthorized: order does not belong to this customer"}`, nil
		}

		data, _ := json.Marshal(map[string]any{
			"order_id":    order.ID,
			"status":      order.Status,
			"total_paise": order.TotalPaise,
			"items":       order.Items,
			"created_at":  order.CreatedAt,
			"updated_at":  order.UpdatedAt,
		})
		return string(data), nil

	case "list_recent_orders":
		var input struct {
			CustomerID string `json:"customer_id"`
			Limit      int    `json:"limit"`
		}
		_ = json.Unmarshal(rawInput, &input)

		// Security: ignore whatever customer_id the model passed and force the authenticated customerID
		limit := input.Limit
		if limit <= 0 {
			limit = 10
		}

		orders, err := te.store.ListOrders(ctx, customerID, limit)
		if err != nil {
			return fmt.Sprintf(`{"error":"failed to list orders: %v"}`, err), nil
		}

		type summary struct {
			ID         uuid.UUID          `json:"id"`
			Status     domain.OrderStatus `json:"status"`
			TotalPaise int64              `json:"total_paise"`
			CreatedAt  string             `json:"created_at"`
		}
		summaries := make([]summary, 0, len(orders))
		for _, o := range orders {
			summaries = append(summaries, summary{
				ID:         o.ID,
				Status:     o.Status,
				TotalPaise: o.TotalPaise,
				CreatedAt:  o.CreatedAt.Format("2006-01-02 15:04:05 UTC"),
			})
		}

		data, _ := json.Marshal(summaries)
		return string(data), nil

	case "cancel_order":
		var input struct {
			OrderID string `json:"order_id"`
		}
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return `{"error":"invalid tool arguments"}`, nil
		}

		orderID, err := uuid.Parse(strings.TrimSpace(input.OrderID))
		if err != nil {
			return `{"error":"invalid order_id UUID format"}`, nil
		}

		order, err := te.store.GetOrder(ctx, orderID)
		if err != nil {
			return `{"error":"order not found"}`, nil
		}

		// Security: verify order ownership
		if order.CustomerID != customerID {
			return `{"error":"unauthorized: order does not belong to this customer"}`, nil
		}

		if order.Status != domain.StatusPlaced &&
			order.Status != domain.StatusConfirmed &&
			order.Status != domain.StatusAssigned {
			return fmt.Sprintf(`{"error":"order cannot be cancelled from status %s"}`, order.Status), nil
		}

		// Free partner if assigned
		if order.PartnerID != nil {
			_ = te.store.MarkPartnerFree(ctx, *order.PartnerID)
		}

		if err := te.store.UpdateOrderStatus(ctx, orderID, domain.StatusCancelled, nil); err != nil {
			return fmt.Sprintf(`{"error":"failed to cancel order: %v"}`, err), nil
		}

		return fmt.Sprintf(`{"success":true,"message":"order %s has been successfully cancelled"}`, orderID), nil

	default:
		return fmt.Sprintf(`{"error":"unknown tool %s"}`, toolName), nil
	}
}
