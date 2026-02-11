// Package chatgptapi provides a wrapper around the official OpenAI Go SDK.
package chatgptapi

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// ModelInfo contains information about an OpenAI model.
type ModelInfo struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// ModelCache caches model information from the API.
type ModelCache struct {
	models    []ModelInfo
	byID      map[string]*ModelInfo
	mu        sync.RWMutex
	fetchMu   sync.Mutex // Prevents thundering herd on cache refresh
	lastFetch time.Time
	ttl       time.Duration
}

// NewModelCache creates a new model cache with the given TTL.
func NewModelCache(ttl time.Duration) *ModelCache {
	return &ModelCache{
		byID: make(map[string]*ModelInfo),
		ttl:  ttl,
	}
}

// globalModelCache is a package-level cache for model info.
var globalModelCache = NewModelCache(15 * time.Minute)

// FetchModels fetches the list of available models from the API.
func FetchModels(ctx context.Context, apiKey string) ([]ModelInfo, error) {
	client := openai.NewClient(option.WithAPIKey(apiKey))

	resp, err := client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	var allModels []ModelInfo
	for _, m := range resp.Data {
		info := ModelInfo{
			ID:          m.ID,
			DisplayName: m.ID,
			CreatedAt:   time.Unix(m.Created, 0),
		}
		allModels = append(allModels, info)
	}

	// Check for empty response - this could indicate API issues or invalid key
	if len(allModels) == 0 {
		return nil, fmt.Errorf("no models returned from API - check API key permissions")
	}

	// Update global cache
	globalModelCache.Update(allModels)

	return allModels, nil
}

// GetModel fetches information about a specific model from the API.
func GetModel(ctx context.Context, apiKey string, modelID string) (*ModelInfo, error) {
	client := openai.NewClient(option.WithAPIKey(apiKey))

	m, err := client.Models.Get(ctx, modelID)
	if err != nil {
		return nil, err
	}

	return &ModelInfo{
		ID:          m.ID,
		DisplayName: m.ID,
		CreatedAt:   time.Unix(m.Created, 0),
	}, nil
}

// ValidateModel checks if a model ID is valid by querying the API.
func ValidateModel(ctx context.Context, apiKey string, modelID string) error {
	_, err := GetModel(ctx, apiKey, modelID)
	return err
}

// Update updates the cache with new model data.
// Makes a deep copy of the input to ensure thread safety.
func (c *ModelCache) Update(models []ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Deep copy to avoid storing references to caller's data
	c.models = make([]ModelInfo, len(models))
	copy(c.models, models)
	c.byID = make(map[string]*ModelInfo, len(c.models))
	for i := range c.models {
		c.byID[c.models[i].ID] = &c.models[i]
	}
	c.lastFetch = time.Now()
}

// GetAll returns all cached models.
func (c *ModelCache) GetAll() []ModelInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]ModelInfo, len(c.models))
	copy(result, c.models)
	return result
}

// Get returns a cached model by ID.
func (c *ModelCache) Get(id string) *ModelInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if info, ok := c.byID[id]; ok {
		// Return a copy
		result := *info
		return &result
	}
	return nil
}

// IsStale returns true if the cache is stale.
func (c *ModelCache) IsStale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return time.Since(c.lastFetch) > c.ttl
}

// IsEmpty returns true if the cache is empty.
func (c *ModelCache) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.models) == 0
}

// GetCachedModels returns cached models, fetching from API if cache is empty or stale.
// Uses double-checked locking to prevent thundering herd on cache refresh.
func GetCachedModels(ctx context.Context, apiKey string) ([]ModelInfo, error) {
	// First check without fetch lock (fast path)
	if !globalModelCache.IsEmpty() && !globalModelCache.IsStale() {
		return globalModelCache.GetAll(), nil
	}

	// Acquire fetch lock to prevent multiple concurrent fetches
	globalModelCache.fetchMu.Lock()
	defer globalModelCache.fetchMu.Unlock()

	// Double-check after acquiring lock (another goroutine may have refreshed)
	if !globalModelCache.IsEmpty() && !globalModelCache.IsStale() {
		return globalModelCache.GetAll(), nil
	}

	return FetchModels(ctx, apiKey)
}

// GetCachedModelInfo returns cached model info by ID.
func GetCachedModelInfo(id string) *ModelInfo {
	return globalModelCache.Get(id)
}

// GetModelDisplayName returns the display name for a model.
func GetModelDisplayName(modelID string) string {
	if info := globalModelCache.Get(modelID); info != nil {
		return info.DisplayName
	}
	return modelID
}

// GetModelInfo returns cached model info, or creates a basic info struct from the model ID.
func GetModelInfo(modelID string) *ModelInfo {
	// Check cache first
	if info := globalModelCache.Get(modelID); info != nil {
		return info
	}

	// Return basic info from model ID
	return &ModelInfo{
		ID:          modelID,
		DisplayName: modelID,
	}
}

// GhostInfo contains information for Matrix ghost users.
type GhostInfo struct {
	Name      string
	AvatarURL string
}

// ghostIDRegex matches characters that are not valid in Matrix localparts.
var ghostIDRegex = regexp.MustCompile(`[^a-z0-9_]`)

// SanitizeModelIDForGhost converts a model ID to a valid Matrix localpart.
func SanitizeModelIDForGhost(modelID string) string {
	sanitized := strings.ToLower(modelID)
	sanitized = ghostIDRegex.ReplaceAllString(sanitized, "_")
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		sanitized = "chatgpt"
	}
	return sanitized
}

// GetGhostID returns a ghost user ID for a model.
func GetGhostID(modelID string) string {
	return SanitizeModelIDForGhost(modelID)
}

// GetGhostInfo returns ghost information for a model.
func GetGhostInfo(modelID string) *GhostInfo {
	if modelID == "" {
		modelID = "chatgpt"
	}

	if modelID == "error" {
		return &GhostInfo{
			Name:      "Error",
			AvatarURL: "",
		}
	}

	return &GhostInfo{
		Name:      GetModelDisplayName(modelID),
		AvatarURL: "",
	}
}

// DefaultMaxContextTokens is the default max context window size used for conversation management.
// Most modern OpenAI models support at least 128k tokens.
const DefaultMaxContextTokens = 128000

// GetModelMaxTokens returns the max context tokens for a model.
// Since OpenAI models vary and we don't hardcode model-specific values,
// this returns a reasonable default that works for modern models.
func GetModelMaxTokens(modelID string) int {
	return DefaultMaxContextTokens
}

// GetModelFamily returns a sanitized model identifier suitable for use in IDs.
// This simply sanitizes the model ID - no family grouping is performed.
func GetModelFamily(modelID string) string {
	return SanitizeModelIDForGhost(modelID)
}
