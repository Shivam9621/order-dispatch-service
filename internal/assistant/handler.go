package assistant

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Handler serves HTTP requests for the AI assistant.
type Handler struct {
	client *Client
}

// NewHandler creates a new Handler.
func NewHandler(client *Client) *Handler {
	return &Handler{client: client}
}

// HandleAssistant processes POST /assistant.
func (h *Handler) HandleAssistant(w http.ResponseWriter, r *http.Request) {
	var req AssistantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.CustomerID) == "" {
		writeError(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	reply, err := h.client.Chat(r.Context(), req.CustomerID, req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(AssistantResponse{Reply: reply})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
