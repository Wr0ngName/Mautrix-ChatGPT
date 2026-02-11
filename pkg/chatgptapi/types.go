// Package chatgptapi provides a client for the OpenAI API.
package chatgptapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
)

// Message represents a message in a conversation.
type Message struct {
	Role    string    `json:"role"`    // "user", "assistant", or "system"
	Content []Content `json:"content"` // Text, images, etc.
}

// Content represents content within a message.
type Content struct {
	Type     string    `json:"type"`               // "text" or "image_url"
	Text     string    `json:"text,omitempty"`     // Text content
	ImageURL *ImageURL `json:"image_url,omitempty"` // Image URL source
}

// ImageURL represents an image URL source for OpenAI vision.
type ImageURL struct {
	URL    string `json:"url"`              // URL or base64 data URI
	Detail string `json:"detail,omitempty"` // "auto", "low", or "high"
}

// CreateMessageRequest represents a request to create a message.
type CreateMessageRequest struct {
	Model       string                 `json:"model"`
	Messages    []Message              `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Temperature float64                `json:"temperature,omitempty"`
	System      string                 `json:"system,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// CreateMessageResponse represents a response from creating a message.
type CreateMessageResponse struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Role         string    `json:"role"`
	Content      []Content `json:"content"`
	Model        string    `json:"model"`
	StopReason   string    `json:"stop_reason,omitempty"`
	StopSequence string    `json:"stop_sequence,omitempty"`
	Usage        *Usage    `json:"usage,omitempty"`
}

// Usage represents token usage information.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamEvent represents an event in a streaming response.
type StreamEvent struct {
	Type    string                 `json:"type"` // "message_start", "content_block_delta", "message_stop", "error", etc.
	Index   int                    `json:"index,omitempty"`
	Delta   *ContentDelta          `json:"delta,omitempty"`
	Message *CreateMessageResponse `json:"message,omitempty"`
	Model   string                 `json:"model,omitempty"`
	Usage   *Usage                 `json:"usage,omitempty"`
	Error   *StreamError           `json:"error,omitempty"`
}

// StreamError represents an error in a streaming response.
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ContentDelta represents incremental content in a streaming response.
type ContentDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text"`
}

// APIError represents an error from the OpenAI API.
type APIError struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	RetryAfter int    `json:"-"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return e.Type + ": " + e.Message
}

// IsRateLimitError checks if an error is a rate limit error (429).
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}

	errStr := err.Error()
	return strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "429")
}

// IsAuthError checks if an error is an authentication error (401, 403).
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized ||
			apiErr.StatusCode == http.StatusForbidden
	}

	errStr := err.Error()
	return strings.Contains(errStr, "authentication") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "invalid_api_key")
}

// IsOverloadedError checks if an error is an overloaded/server error (5xx).
func IsOverloadedError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500
	}

	errStr := err.Error()
	return strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "server_error")
}

// IsInvalidRequestError checks if an error is an invalid request error (400).
func IsInvalidRequestError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusBadRequest
	}

	errStr := err.Error()
	return strings.Contains(errStr, "invalid_request") ||
		strings.Contains(errStr, "bad request")
}

// GetRetryAfter attempts to extract retry-after duration from an error.
func GetRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.Response != nil {
			if retryAfter := apiErr.Response.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, parseErr := time.ParseDuration(retryAfter + "s"); parseErr == nil {
					return seconds
				}
			}
		}
	}

	if IsRateLimitError(err) {
		return 30 * time.Second
	}

	return 0
}
