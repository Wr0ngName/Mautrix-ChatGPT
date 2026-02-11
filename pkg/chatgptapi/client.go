// Package chatgptapi provides a wrapper around the official OpenAI Go SDK.
package chatgptapi

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/rs/zerolog"
)

// Client wraps the official OpenAI SDK client.
type Client struct {
	sdk     openai.Client
	Log     zerolog.Logger
	Metrics *Metrics
}

// Ensure Client implements MessageClient interface.
var _ MessageClient = (*Client)(nil)

// NewClient creates a new OpenAI API client using the official SDK.
func NewClient(apiKey string, log zerolog.Logger) *Client {
	sdk := openai.NewClient(
		option.WithAPIKey(apiKey),
	)

	return &Client{
		sdk:     sdk,
		Log:     log,
		Metrics: NewMetrics(),
	}
}

// Validate checks if the API key is valid by making a minimal test request.
func (c *Client) Validate(ctx context.Context) error {
	// Use the Models API to validate the key
	_, err := c.sdk.Models.List(ctx)
	if err != nil {
		c.Log.Debug().Err(err).Msg("API key validation failed")
		return err
	}
	return nil
}

// CreateMessage creates a new message (non-streaming).
func (c *Client) CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error) {
	startTime := time.Now()

	// Convert our messages to SDK format
	sdkMessages := convertMessagesToSDK(req.Messages, req.System)

	// Build params
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: sdkMessages,
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	c.Log.Debug().
		Str("model", req.Model).
		Int("max_tokens", req.MaxTokens).
		Msg("Sending message to OpenAI API")

	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil {
		c.Metrics.RecordError(err)
		return nil, err
	}

	// Record metrics
	duration := time.Since(startTime)
	inputTokens := int(resp.Usage.PromptTokens)
	outputTokens := int(resp.Usage.CompletionTokens)
	c.Metrics.RecordRequest(req.Model, duration, inputTokens, outputTokens)

	// Convert response to our format
	return convertSDKResponse(resp), nil
}

// CreateMessageStream creates a new message with streaming.
func (c *Client) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
	// Convert our messages to SDK format
	sdkMessages := convertMessagesToSDK(req.Messages, req.System)

	// Build params
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: sdkMessages,
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	c.Log.Debug().
		Str("model", req.Model).
		Int("max_tokens", req.MaxTokens).
		Msg("Starting streaming message to OpenAI API")

	stream := c.sdk.Chat.Completions.NewStreaming(ctx, params)

	// Check if stream was created successfully
	if stream == nil {
		return nil, fmt.Errorf("failed to create message stream")
	}

	// Create output channel with buffer size of 100.
	eventCh := make(chan StreamEvent, 100)

	// Start goroutine to process stream with proper context cancellation handling
	go func() {
		defer close(eventCh)
		defer func() {
			if r := recover(); r != nil {
				c.Log.Error().Interface("panic", r).Msg("Panic in stream processing goroutine")
			}
		}()
		startTime := time.Now()
		var inputTokens, outputTokens int

		// Send initial message_start event
		select {
		case eventCh <- StreamEvent{
			Type: "message_start",
			Message: &CreateMessageResponse{
				ID:    fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Model: req.Model,
			},
		}:
		case <-ctx.Done():
			return
		}

		for stream.Next() {
			// Check for context cancellation to prevent goroutine leak
			select {
			case <-ctx.Done():
				c.Log.Debug().Msg("Stream cancelled by context")
				return
			default:
			}

			chunk := stream.Current()

			// Process choices
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					// Non-blocking send to prevent deadlock
					select {
					case eventCh <- StreamEvent{
						Type: "content_block_delta",
						Delta: &ContentDelta{
							Type: "text_delta",
							Text: choice.Delta.Content,
						},
					}:
					case <-ctx.Done():
						c.Log.Debug().Msg("Stream cancelled while sending event")
						return
					}
				}
			}

			// Track token usage from chunk
			if chunk.Usage.PromptTokens > 0 {
				inputTokens = int(chunk.Usage.PromptTokens)
			}
			if chunk.Usage.CompletionTokens > 0 {
				outputTokens = int(chunk.Usage.CompletionTokens)
			}
		}

		if err := stream.Err(); err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil {
				c.Log.Debug().Msg("Stream ended due to context cancellation")
				return
			}
			// io.EOF is expected at end of stream
			if err != io.EOF {
				c.Log.Error().Err(err).Msg("Stream error")
				c.Metrics.RecordError(err)
				// Non-blocking error send
				select {
				case eventCh <- StreamEvent{
					Type: "error",
					Error: &StreamError{
						Type:    "stream_error",
						Message: err.Error(),
					},
				}:
				case <-ctx.Done():
				}
				return
			}
		}

		// Record successful request
		duration := time.Since(startTime)
		c.Log.Debug().
			Str("model", req.Model).
			Dur("duration", duration).
			Int("input_tokens", inputTokens).
			Int("output_tokens", outputTokens).
			Msg("Stream completed successfully, recording metrics")
		c.Metrics.RecordRequest(req.Model, duration, inputTokens, outputTokens)

		// Send final message_delta with usage
		select {
		case eventCh <- StreamEvent{
			Type: "message_delta",
			Usage: &Usage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			},
		}:
		case <-ctx.Done():
		}

		// Send message_stop
		select {
		case eventCh <- StreamEvent{
			Type: "message_stop",
		}:
		case <-ctx.Done():
		}
	}()

	return eventCh, nil
}

// GetClientType returns the client type identifier.
func (c *Client) GetClientType() string {
	return ClientTypeAPI
}

// GetMetrics returns the metrics collector.
func (c *Client) GetMetrics() *Metrics {
	return c.Metrics
}

// CompactConversation calls ChatGPT to generate a summary of the conversation for compaction.
// Returns the summary text that can be used to replace the conversation history.
func (c *Client) CompactConversation(ctx context.Context, model string, conversationText string) (string, error) {
	compactionPrompt := `You are being asked to create a concise summary of the conversation so far.
This summary will replace the full conversation history to manage context limits.

Create a summary that:
1. Preserves all important context, decisions, and key information discussed
2. Maintains the essential flow and topics of the conversation
3. Notes any specific user preferences or requirements mentioned
4. Keeps track of any ongoing tasks or open questions
5. Is written from a neutral perspective (not as "I" the assistant)

Format your summary as a clear, structured recap that another instance of ChatGPT
could use to continue the conversation seamlessly. Be thorough but concise.

Respond ONLY with the summary, no preamble or explanation.

Here is the conversation to summarize:

` + conversationText

	resp, err := c.CreateMessage(ctx, &CreateMessageRequest{
		Model:     model,
		MaxTokens: 4096, // Enough for a detailed summary
		Messages: []Message{
			{
				Role: "user",
				Content: []Content{
					{Type: "text", Text: compactionPrompt},
				},
			},
		},
		Temperature: 0.3, // Low temperature for consistent summaries
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate compaction summary: %w", err)
	}

	// Extract summary from response
	for _, content := range resp.Content {
		if content.Type == "text" && content.Text != "" {
			return content.Text, nil
		}
	}

	return "", fmt.Errorf("no summary in compaction response")
}

// convertMessagesToSDK converts our message format to SDK format.
func convertMessagesToSDK(messages []Message, systemPrompt string) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)

	// Add system message if provided
	if systemPrompt != "" {
		result = append(result, openai.SystemMessage(systemPrompt))
	}

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Extract text content
			text := extractTextContent(msg.Content)
			result = append(result, openai.SystemMessage(text))

		case "assistant":
			text := extractTextContent(msg.Content)
			result = append(result, openai.AssistantMessage(text))

		case "user":
			// Check if message has images
			hasImages := false
			for _, c := range msg.Content {
				if c.Type == "image_url" {
					hasImages = true
					break
				}
			}

			if !hasImages {
				// Simple text message
				text := extractTextContent(msg.Content)
				result = append(result, openai.UserMessage(text))
			} else {
				// Multi-part message with images
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Content))
				for _, c := range msg.Content {
					switch c.Type {
					case "text":
						parts = append(parts, openai.TextContentPart(c.Text))
					case "image_url":
						if c.ImageURL != nil {
							parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
								URL:    c.ImageURL.URL,
								Detail: c.ImageURL.Detail,
							}))
						}
					}
				}
				result = append(result, openai.UserMessage(parts))
			}
		}
	}

	return result
}

// extractTextContent extracts text from content array.
func extractTextContent(content []Content) string {
	for _, c := range content {
		if c.Type == "text" {
			return c.Text
		}
	}
	return ""
}

// convertSDKResponse converts SDK response to our format.
func convertSDKResponse(resp *openai.ChatCompletion) *CreateMessageResponse {
	var content []Content

	// Extract content from choices
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		content = append(content, Content{
			Type: "text",
			Text: choice.Message.Content,
		})
	}

	return &CreateMessageResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Content: content,
		Model: resp.Model,
		Usage: &Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
		},
	}
}
