package assistant

import "encoding/json"

// Anthropic API request and response structures.

type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Content    string          `json:"content,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // can be string or []ContentBlock
}

type MessagesRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []Message    `json:"messages"`
	Tools     []ToolSchema `json:"tools,omitempty"`
}

type MessagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

// AssistantRequest is the payload for POST /assistant.
type AssistantRequest struct {
	CustomerID string `json:"customer_id"`
	Message    string `json:"message"`
}

// AssistantResponse is the reply returned to the user.
type AssistantResponse struct {
	Reply string `json:"reply"`
}
