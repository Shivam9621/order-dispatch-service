package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicVersion         = "2023-06-01"
	maxIterations            = 5
)

// Client handles communication with the Anthropic Messages API and manages the agentic tool loop.
type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
	executor   *ToolExecutor
}

// Config configures the Anthropic assistant client.
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// NewClient initializes a new assistant client.
func NewClient(cfg Config, executor *ToolExecutor) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultAnthropicEndpoint
	}

	model := cfg.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: httpClient,
		executor:   executor,
	}
}

// Chat runs the agent loop:
// 1. Sends the user message plus tool definitions with a 10-second timeout context.
// 2. If stop_reason is "tool_use", executes the tools, appends tool_result blocks, and repeats.
// 3. Stops at the final text response or after a max of 5 iterations.
func (c *Client) Chat(ctx context.Context, customerID string, userMessage string) (string, error) {
	// Enforce 10-second context timeout per PLAN.md
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tools := GetToolDefinitions()

	var messages []Message
	messages = append(messages, Message{
		Role:    "user",
		Content: userMessage,
	})

	for iteration := 1; iteration <= maxIterations; iteration++ {
		reqBody := MessagesRequest{
			Model:     c.model,
			MaxTokens: 1024,
			Messages:  messages,
			Tools:     tools,
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return "", fmt.Errorf("marshaling messages request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, c.baseURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("creating http request: %w", err)
		}

		httpReq.Header.Set("x-api-key", c.apiKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("calling anthropic api: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("reading anthropic response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("anthropic api returned error (status %d): %s", resp.StatusCode, string(respBody))
		}

		var apiResp MessagesResponse
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			return "", fmt.Errorf("unmarshaling anthropic response: %w", err)
		}

		// Check if the response contains final text answer
		if apiResp.StopReason != "tool_use" {
			var textParts []string
			for _, b := range apiResp.Content {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					textParts = append(textParts, b.Text)
				}
			}
			if len(textParts) == 0 {
				return "No text response received from assistant.", nil
			}
			return strings.Join(textParts, "\n"), nil
		}

		// Assistant wants to call one or more tools
		// Append the assistant's response to message history
		messages = append(messages, Message{
			Role:    "assistant",
			Content: apiResp.Content,
		})

		var toolResultBlocks []ContentBlock
		for _, block := range apiResp.Content {
			if block.Type == "tool_use" {
				slog.Info("assistant invoking tool",
					"customer_id", customerID,
					"tool", block.Name,
					"tool_use_id", block.ID,
					"iteration", iteration,
				)

				result, err := c.executor.Execute(timeoutCtx, customerID, block.Name, block.Input)
				if err != nil {
					result = fmt.Sprintf(`{"error":"%v"}`, err)
				}

				toolResultBlocks = append(toolResultBlocks, ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   result,
				})
			}
		}

		// Append user message containing tool results
		messages = append(messages, Message{
			Role:    "user",
			Content: toolResultBlocks,
		})
	}

	return "", errors.New("assistant exceeded safety cap of 5 tool-use iterations")
}
