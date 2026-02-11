// Package connector provides the Matrix bridge connector for OpenAI API.
package connector

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-chatgpt/pkg/chatgptapi"
)

// ChatGPTConnector implements the bridgev2.NetworkConnector interface for OpenAI API.
type ChatGPTConnector struct {
	br     *bridgev2.Bridge
	Config Config
	Log    zerolog.Logger
}

var (
	_ bridgev2.NetworkConnector            = (*ChatGPTConnector)(nil)
	_ bridgev2.MaxFileSizeingNetwork       = (*ChatGPTConnector)(nil)
	_ bridgev2.IdentifierValidatingNetwork = (*ChatGPTConnector)(nil)
	_ bridgev2.ConfigValidatingNetwork     = (*ChatGPTConnector)(nil)
)

// NewConnector creates a new OpenAI API bridge connector.
func NewConnector() *ChatGPTConnector {
	return &ChatGPTConnector{}
}

// Init initializes the connector with the bridge.
func (c *ChatGPTConnector) Init(bridge *bridgev2.Bridge) {
	c.br = bridge
	c.Log = bridge.Log.With().Str("connector", "chatgpt").Logger()
}

// Start starts the connector.
func (c *ChatGPTConnector) Start(ctx context.Context) error {
	c.Log.Info().Msg("ChatGPT API connector starting")

	// Log loaded config
	c.Log.Info().
		Str("default_model", c.Config.GetDefaultModel()).
		Int("max_tokens", c.Config.GetMaxTokens()).
		Float64("temperature", c.Config.GetTemperature()).
		Str("system_prompt_preview", truncateString(c.Config.GetSystemPrompt(), 50)).
		Int("conversation_max_age_hours", c.Config.ConversationMaxAge).
		Int("rate_limit_per_minute", c.Config.GetRateLimitPerMinute()).
		Msg("Loaded connector config")

	// Register custom commands
	if proc, ok := c.br.Commands.(*commands.Processor); ok {
		c.RegisterCommands(proc)
		c.Log.Debug().Msg("Registered custom bridge commands")
	}

	return nil
}

// truncateString truncates a string to maxLen runes (not bytes).
// This ensures proper UTF-8 handling and won't split multi-byte characters.
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// GetName returns the name of the network.
func (c *ChatGPTConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "ChatGPT",
		NetworkURL:       "https://platform.openai.com",
		NetworkIcon:      "mxc://maunium.net/openai",
		NetworkID:        "chatgpt",
		BeeperBridgeType: "go.mau.fi/mautrix-chatgpt",
		DefaultPort:      29350,
	}
}

// GetDBMetaTypes returns the database meta types for the connector.
func (c *ChatGPTConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Ghost:     func() any { return &GhostMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		Portal:    func() any { return &PortalMetadata{} },
		Reaction:  nil,
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

// GetCapabilities returns the capabilities of the connector.
func (c *ChatGPTConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: false,
		AggressiveUpdateInfo: false,
	}
}

// GetBridgeInfoVersion returns version numbers for bridge info and room capabilities.
func (c *ChatGPTConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

// GetConfig returns the connector configuration.
func (c *ChatGPTConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return ExampleConfig, &c.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

// upgradeConfig copies config values from the user's config file to the base config.
// This ensures user values are preserved when the config file is updated.
func upgradeConfig(helper configupgrade.Helper) {
	helper.Copy(configupgrade.Str, "default_model")
	helper.Copy(configupgrade.Int, "max_tokens")
	helper.Copy(configupgrade.Float, "temperature")
	helper.Copy(configupgrade.Str, "system_prompt")
	helper.Copy(configupgrade.Int, "conversation_max_age_hours")
	helper.Copy(configupgrade.Int, "rate_limit_per_minute")
}

// ValidateConfig validates the loaded configuration.
func (c *ChatGPTConnector) ValidateConfig() error {
	return c.Config.Validate()
}

// SetMaxFileSize sets the maximum file size for uploads.
func (c *ChatGPTConnector) SetMaxFileSize(maxSize int64) {
	// OpenAI API supports images up to 20MB
}

// GetLoginFlows returns the available login flows.
func (c *ChatGPTConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			Name:        "API Key",
			Description: "Log in with your own OpenAI API key from platform.openai.com",
			ID:          "api_key",
		},
	}
}

// CreateLogin creates a new login handler.
func (c *ChatGPTConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	switch flowID {
	case "api_key":
		return &APIKeyLogin{
			User:      user,
			Connector: c,
		}, nil
	default:
		return nil, fmt.Errorf("unknown login flow: %s", flowID)
	}
}

// LoadUserLogin loads an existing user login.
func (c *ChatGPTConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	metadata, ok := login.Metadata.(*UserLoginMetadata)
	if !ok || metadata == nil {
		c.Log.Error().
			Bool("type_assertion_ok", ok).
			Interface("metadata_type", fmt.Sprintf("%T", login.Metadata)).
			Msg("Failed to cast user login metadata to expected type")
		return fmt.Errorf("invalid user login metadata")
	}

	log := c.Log.With().Str("user", string(login.UserMXID)).Logger()

	if metadata.APIKey == "" {
		return fmt.Errorf("no API key found in login metadata")
	}

	log.Info().Msg("Using direct API backend")
	messageClient := chatgptapi.NewClient(metadata.APIKey, log)

	chatgptClient := &ChatGPTClient{
		MessageClient: messageClient,
		UserLogin:     login,
		Connector:     c,
		conversations: make(map[networkid.PortalID]*chatgptapi.ConversationManager),
		rateLimiter:   NewRateLimiter(c.Config.GetRateLimitPerMinute()),
	}

	login.Client = chatgptClient

	return nil
}

// GhostMetadata contains ChatGPT-specific ghost user metadata.
type GhostMetadata struct {
	Model string `json:"model"` // Which model this "ghost" represents
}

// MessageMetadata contains ChatGPT-specific message metadata.
type MessageMetadata struct {
	ChatGPTMessageID string `json:"chatgpt_message_id"`
	TokensUsed       int    `json:"tokens_used"`
}

// PortalMetadata contains ChatGPT-specific portal/room metadata.
type PortalMetadata struct {
	ConversationName string   `json:"conversation_name"`
	Model            string   `json:"model"`                   // Selected model for this room
	SystemPrompt     string   `json:"system_prompt,omitempty"` // Custom system prompt
	Temperature      *float64 `json:"temperature,omitempty"`   // Custom temperature
	MentionOnly      bool     `json:"mention_only,omitempty"`  // Only respond when mentioned
}

// GetTemperature returns the temperature for this portal, or the default if not set.
func (p *PortalMetadata) GetTemperature(defaultTemp float64) float64 {
	if p.Temperature == nil {
		return defaultTemp
	}
	temp := *p.Temperature
	if temp < 0 || temp > 2 {
		return defaultTemp
	}
	return temp
}

// UserLoginMetadata contains ChatGPT-specific user login metadata.
type UserLoginMetadata struct {
	APIKey string `json:"api_key"`
	Email  string `json:"email,omitempty"`
}

// ValidateUserID validates that a user ID is a valid ChatGPT ghost ID.
// This is called by the framework during ghost DM invite handling.
// Accepts any sanitized model ID (lowercase alphanumeric with underscores).
func (c *ChatGPTConnector) ValidateUserID(id networkid.UserID) bool {
	idStr := string(id)
	// Special system ghost IDs
	if idStr == "error" || idStr == "system" {
		return true
	}
	// Accept any ID that looks like a sanitized model ID
	if len(idStr) == 0 || len(idStr) > 100 {
		return false
	}
	for _, r := range idStr {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

// MakeChatGPTGhostID creates a network user ID from a model name.
func (c *ChatGPTConnector) MakeChatGPTGhostID(model string) networkid.UserID {
	return networkid.UserID(chatgptapi.GetGhostID(model))
}

// MakeChatGPTPortalKey creates a portal key from a conversation identifier.
// Receiver is intentionally not set so that FindPreferredLogin works properly
// and users can switch between logins using set-preferred-login command.
func MakeChatGPTPortalKey(conversationID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID: networkid.PortalID(conversationID),
	}
}

// MakeChatGPTMessageID creates a message ID from a ChatGPT message ID.
func MakeChatGPTMessageID(messageID string) networkid.MessageID {
	return networkid.MessageID(messageID)
}
