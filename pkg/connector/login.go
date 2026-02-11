package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-chatgpt/pkg/chatgptapi"
)

// APIKeyLogin handles API key-based login.
type APIKeyLogin struct {
	User      *bridgev2.User
	Connector *ChatGPTConnector
}

var (
	_ bridgev2.LoginProcess          = (*APIKeyLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*APIKeyLogin)(nil)
)

// Start begins the API key login flow.
func (a *APIKeyLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "api_key",
		Instructions: "Enter your OpenAI API key. Get one from: https://platform.openai.com/api-keys",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "api_key",
					Name:        "API Key",
					Description: "Your OpenAI API key (sk-...)",
				},
			},
		},
	}, nil
}

// SubmitUserInput processes the submitted API key.
func (a *APIKeyLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	apiKey := input["api_key"]

	// Validate API key format
	if !isValidAPIKeyFormat(apiKey) {
		return nil, fmt.Errorf("invalid API key format")
	}

	// Test the API key
	client := chatgptapi.NewClient(apiKey, a.Connector.Log)
	if err := client.Validate(ctx); err != nil {
		if chatgptapi.IsAuthError(err) {
			return nil, fmt.Errorf("invalid API key: authentication failed")
		}
		return nil, fmt.Errorf("failed to validate API key: %w", err)
	}

	// Create user login with hashed API key (for privacy - no raw key material in ID)
	hash := sha256.Sum256([]byte(apiKey))
	loginID := networkid.UserLoginID(fmt.Sprintf("chatgpt_%s", hex.EncodeToString(hash[:10])))
	userLogin, err := a.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "OpenAI API User",
		Metadata: &UserLoginMetadata{
			APIKey: apiKey,
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	// Set up client with rate limiter
	chatgptClient := &ChatGPTClient{
		MessageClient: client,
		UserLogin:     userLogin,
		Connector:     a.Connector,
		conversations: make(map[networkid.PortalID]*chatgptapi.ConversationManager),
		rateLimiter:   NewRateLimiter(a.Connector.Config.GetRateLimitPerMinute()),
	}
	userLogin.Client = chatgptClient

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: "Successfully authenticated with OpenAI API",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// Cancel cancels the login process.
func (a *APIKeyLogin) Cancel() {}

// isValidAPIKeyFormat checks if an API key has a valid format.
func isValidAPIKeyFormat(apiKey string) bool {
	if apiKey == "" {
		return false
	}

	// OpenAI API keys start with "sk-"
	if !strings.HasPrefix(apiKey, "sk-") {
		return false
	}

	// Must be longer than just the prefix
	if len(apiKey) <= len("sk-") {
		return false
	}

	return true
}
