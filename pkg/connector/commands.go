package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/mautrix-chatgpt/pkg/chatgptapi"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// swapGhosts ensures the correct ghost is in the room for the new model.
func (c *ChatGPTConnector) swapGhosts(ctx context.Context, roomID id.RoomID, oldModel, newModel string) error {
	oldGhostID := c.MakeChatGPTGhostID(oldModel)
	newGhostID := c.MakeChatGPTGhostID(newModel)
	ghostChanged := oldGhostID != newGhostID

	// Get the new ghost and ensure it joins the room
	newGhost, err := c.GetOrUpdateGhost(ctx, newGhostID, newModel)
	if err != nil {
		return fmt.Errorf("failed to get new ghost: %w", err)
	}

	// Have the new ghost join
	if err := newGhost.Intent.EnsureJoined(ctx, roomID); err != nil {
		// Try invite + join
		if err := c.br.Bot.EnsureInvited(ctx, roomID, newGhost.Intent.GetMXID()); err != nil {
			return fmt.Errorf("failed to invite new ghost: %w", err)
		}
		if err := newGhost.Intent.EnsureJoined(ctx, roomID); err != nil {
			return fmt.Errorf("new ghost failed to join after invite: %w", err)
		}
	}

	// Only remove old ghost if ghost changed
	if ghostChanged {
		// Have the old ghost leave the room
		oldGhost, err := c.br.GetExistingGhostByID(ctx, oldGhostID)
		if err == nil && oldGhost != nil {
			// Use the underlying appservice IntentAPI's LeaveRoom method
			if asIntent, ok := oldGhost.Intent.(*matrix.ASIntent); ok {
				if _, err := asIntent.Matrix.LeaveRoom(ctx, roomID); err != nil {
					c.Log.Warn().Err(err).Msg("Failed to leave old ghost from room")
				}
			} else {
				// Fallback to SendState approach
				leaveContent := &event.Content{Parsed: &event.MemberEventContent{Membership: event.MembershipLeave}}
				if _, err := oldGhost.Intent.SendState(ctx, roomID, event.StateMember, oldGhost.Intent.GetMXID().String(), leaveContent, time.Time{}); err != nil {
					c.Log.Warn().Err(err).Msg("Failed to send leave state for old ghost")
				}
			}
		}

		c.Log.Info().
			Str("old_ghost", string(oldGhostID)).
			Str("new_ghost", string(newGhostID)).
			Msg("Swapped ghosts for model change")
	}

	return nil
}

// RegisterCommands registers custom commands for the ChatGPT bridge.
func (c *ChatGPTConnector) RegisterCommands(proc *commands.Processor) {
	proc.AddHandlers(
		&commands.FullHandler{
			Func:    c.cmdJoin,
			Name:    "join",
			Aliases: []string{"add", "invite"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Add ChatGPT to the current room (creates a bridge portal)",
				Args:        "[model]",
			},
			RequiresLogin:  true,
			RequiresPortal: false, // Can be used in any room
		},
		&commands.FullHandler{
			Func:    c.cmdModel,
			Name:    "model",
			Aliases: []string{"set-model", "switch-model"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or change the OpenAI model for this conversation",
				Args:        "[model-name]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdModels,
			Name:    "models",
			Aliases: []string{"list-models"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "List available OpenAI models",
			},
			RequiresLogin: true,
		},
		&commands.FullHandler{
			Func:    c.cmdClear,
			Name:    "clear",
			Aliases: []string{"reset", "clear-context"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Clear the conversation history/context for this room",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdStats,
			Name:    "stats",
			Aliases: []string{"info", "status"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Show conversation statistics for this room",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdSystem,
			Name:    "system",
			Aliases: []string{"set-system", "system-prompt"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or set the system prompt for this conversation",
				Args:        "[prompt]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdTemperature,
			Name:    "temperature",
			Aliases: []string{"temp", "set-temp"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "View or set the temperature (0-2) for this conversation",
				Args:        "[value]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func:    c.cmdMention,
			Name:    "mention",
			Aliases: []string{"mentions", "mention-only"},
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionGeneral,
				Description: "Toggle mention-only mode (ChatGPT only responds when @mentioned)",
				Args:        "[on|off]",
			},
			RequiresLogin:  true,
			RequiresPortal: true,
		},
		&commands.FullHandler{
			Func: c.cmdRemoveGhost,
			Name: "remove-ghost",
			Help: commands.HelpMeta{
				Section:     commands.HelpSectionAdmin,
				Description: "Remove a bridge ghost from the current room (admin only)",
				Args:        "<@user:server>",
			},
			RequiresAdmin: true,
		},
	)
}

// getAPIKeyFromLogin extracts the API key from a user login.
func (c *ChatGPTConnector) getAPIKeyFromLogin(ce *commands.Event) string {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		return ""
	}
	meta, ok := login.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		return ""
	}
	return meta.APIKey
}

// cmdModel views or changes the OpenAI model for a conversation.
func (c *ChatGPTConnector) cmdModel(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current model
	if len(ce.Args) == 0 {
		currentModel := meta.Model
		if currentModel == "" {
			currentModel = c.Config.GetDefaultModel()
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**Current model:** `%s`\n\n", currentModel))
		sb.WriteString("Use `model <model-id>` to change. Run `models` to see available options.")
		ce.Reply(sb.String())
		return
	}

	// Set new model - model ID is passed directly to API
	ctx, cancel := context.WithTimeout(ce.Ctx, 15*time.Second)
	defer cancel()

	newModel := strings.Join(ce.Args, "-")

	// Validate model ID format
	if err := ValidateModelID(newModel); err != nil {
		ce.Reply("Invalid model ID: %v", err)
		return
	}

	// Validate model exists via API
	apiKey := c.getAPIKeyFromLogin(ce)
	if apiKey == "" {
		ce.Reply("Failed to get API credentials.")
		return
	}
	if err := chatgptapi.ValidateModel(ctx, apiKey, newModel); err != nil {
		ce.Reply("Invalid model: %v\n\nRun `models` to see available options.", err)
		return
	}

	// Get old model for ghost swap
	oldModel := meta.Model
	if oldModel == "" {
		oldModel = c.Config.GetDefaultModel()
	}

	// Update portal metadata
	meta.Model = newModel
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.Model = oldModel // Rollback in-memory state on save failure
		ce.Reply("Failed to save model change: %v", err)
		return
	}

	// Swap ghosts if model changed
	if ce.Portal.MXID != "" {
		if err := c.swapGhosts(ctx, ce.Portal.MXID, oldModel, newModel); err != nil {
			c.Log.Warn().Err(err).Msg("Failed to swap ghosts for model change")
		}
	}

	ce.Reply("Model changed to `%s`", newModel)
}

// cmdModels lists available OpenAI models by querying the API.
func (c *ChatGPTConnector) cmdModels(ce *commands.Event) {
	// Get API key
	apiKey := c.getAPIKeyFromLogin(ce)
	if apiKey == "" {
		ce.Reply("Failed to get API credentials.")
		return
	}

	// Fetch models from API
	ctx, cancel := context.WithTimeout(ce.Ctx, 15*time.Second)
	defer cancel()

	models, err := chatgptapi.FetchModels(ctx, apiKey)
	if err != nil {
		ce.Reply("Failed to fetch models from API: %v", err)
		return
	}

	if len(models) == 0 {
		ce.Reply("No models available.")
		return
	}

	var sb strings.Builder
	sb.WriteString("**Available OpenAI Models:**\n\n")

	defaultModel := c.Config.GetDefaultModel()

	// List all models
	for _, model := range models {
		isDefault := ""
		if model.ID == defaultModel {
			isDefault = " *(default)*"
		}
		sb.WriteString(fmt.Sprintf("• `%s`%s\n", model.ID, isDefault))
	}

	sb.WriteString("\nUse `model <model-id>` to switch models.")

	ce.Reply(sb.String())
}

// cmdClear clears the conversation history.
func (c *ChatGPTConnector) cmdClear(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ChatGPTClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	// Get stats before clearing
	msgCount, tokens, _ := client.GetConversationStats(ce.Portal.PortalKey.ID)

	// Clear the conversation
	client.ClearConversation(ce.Portal.PortalKey.ID)

	ce.Reply("Conversation cleared. Removed %d messages (~%d tokens).", msgCount, tokens)
}

// cmdStats shows conversation statistics.
func (c *ChatGPTConnector) cmdStats(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ChatGPTClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	meta, _ := ce.Portal.Metadata.(*PortalMetadata)

	var sb strings.Builder
	sb.WriteString("**Conversation Statistics:**\n\n")

	// Model info
	model := c.Config.GetDefaultModel()
	if meta != nil && meta.Model != "" {
		model = meta.Model
	}
	sb.WriteString(fmt.Sprintf("**Model:** `%s`\n", model))

	// Get local conversation stats
	msgCount, estimatedTokens, lastUsed := client.GetConversationStats(ce.Portal.PortalKey.ID)

	// Conversation stats
	sb.WriteString(fmt.Sprintf("**Messages in context:** %d\n", msgCount))
	sb.WriteString(fmt.Sprintf("**Estimated tokens:** ~%d\n", estimatedTokens))

	// Show compaction and context usage from conversation manager
	_, estTokens, maxTokens, apiCompactionCount, _ := client.GetConversationFullStats(ce.Portal.PortalKey.ID)
	if apiCompactionCount > 0 {
		sb.WriteString(fmt.Sprintf("**Context compactions:** %d\n", apiCompactionCount))
	}
	if maxTokens > 0 && estTokens > 0 {
		usagePercent := (estTokens * 100) / maxTokens
		sb.WriteString(fmt.Sprintf("**Context usage:** ~%d%% of %dk limit\n", usagePercent, maxTokens/1000))
	}

	if !lastUsed.IsZero() {
		sb.WriteString(fmt.Sprintf("**Last active:** %s ago\n", time.Since(lastUsed).Round(time.Second)))
	}

	// System prompt info
	if meta != nil && meta.SystemPrompt != "" {
		promptPreview := meta.SystemPrompt
		if len(promptPreview) > 100 {
			promptPreview = promptPreview[:97] + "..."
		}
		sb.WriteString(fmt.Sprintf("**Custom system prompt:** %s\n", promptPreview))
	}

	// Temperature info
	if meta != nil && meta.Temperature != nil {
		sb.WriteString(fmt.Sprintf("**Temperature:** %.2f\n", *meta.Temperature))
	} else {
		sb.WriteString(fmt.Sprintf("**Temperature:** %.2f (default)\n", c.Config.GetTemperature()))
	}

	// API metrics
	if metrics := client.GetMetrics(); metrics != nil {
		totalReqs := metrics.TotalRequests.Load()
		failedReqs := metrics.FailedRequests.Load()
		localInputTokens := metrics.TotalInputTokens.Load()
		localOutputTokens := metrics.TotalOutputTokens.Load()

		sb.WriteString(fmt.Sprintf("\n**API Stats (this session):**\n"))
		sb.WriteString(fmt.Sprintf("• Requests: %d (%d failed)\n", totalReqs, failedReqs))
		sb.WriteString(fmt.Sprintf("• Total tokens: %d (in: %d, out: %d)\n",
			localInputTokens+localOutputTokens, localInputTokens, localOutputTokens))
		if avgDuration := metrics.GetAverageRequestDuration(); avgDuration > 0 {
			sb.WriteString(fmt.Sprintf("• Avg response time: %s\n", avgDuration.Round(time.Millisecond)))
		}
	}

	ce.Reply(sb.String())
}

// cmdSystem views or sets the system prompt.
func (c *ChatGPTConnector) cmdSystem(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current system prompt
	if len(ce.Args) == 0 {
		currentPrompt := meta.SystemPrompt
		if currentPrompt == "" {
			currentPrompt = c.Config.GetSystemPrompt()
			if currentPrompt == "" {
				ce.Reply("No system prompt is set. Use `system <prompt>` to set one.")
			} else {
				ce.Reply("**Current system prompt (default):**\n\n%s", currentPrompt)
			}
		} else {
			ce.Reply("**Current system prompt:**\n\n%s\n\nUse `system clear` to reset to default.", currentPrompt)
		}
		return
	}

	// Check for clear command
	if strings.ToLower(ce.Args[0]) == "clear" {
		oldPrompt := meta.SystemPrompt
		meta.SystemPrompt = ""
		if err := ce.Portal.Save(ce.Ctx); err != nil {
			meta.SystemPrompt = oldPrompt // Rollback in-memory state on save failure
			ce.Reply("Failed to clear system prompt: %v", err)
			return
		}
		ce.Reply("System prompt cleared. Using default.")
		return
	}

	// Set new system prompt - save old value for rollback on failure
	newPrompt := strings.Join(ce.Args, " ")
	oldPrompt := meta.SystemPrompt
	meta.SystemPrompt = newPrompt
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.SystemPrompt = oldPrompt // Rollback in-memory state on save failure
		ce.Reply("Failed to save system prompt: %v", err)
		return
	}

	ce.Reply("System prompt updated.")
}

// cmdMention toggles mention-only mode.
func (c *ChatGPTConnector) cmdMention(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current status
	if len(ce.Args) == 0 {
		if meta.MentionOnly {
			ce.Reply("**Mention-only mode:** ON\n\nChatGPT only responds when @mentioned.\n\nUse `mention off` to respond to all messages.")
		} else {
			ce.Reply("**Mention-only mode:** OFF\n\nChatGPT responds to all messages.\n\nUse `mention on` to only respond when @mentioned.")
		}
		return
	}

	// Parse argument
	arg := strings.ToLower(ce.Args[0])
	var newValue bool
	switch arg {
	case "on", "true", "yes", "1", "enable", "enabled":
		newValue = true
	case "off", "false", "no", "0", "disable", "disabled":
		newValue = false
	case "toggle":
		newValue = !meta.MentionOnly
	default:
		ce.Reply("Invalid argument. Use `mention on`, `mention off`, or `mention toggle`.")
		return
	}

	oldValue := meta.MentionOnly
	meta.MentionOnly = newValue
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.MentionOnly = oldValue
		ce.Reply("Failed to save setting: %v", err)
		return
	}

	if newValue {
		ce.Reply("Mention-only mode **enabled**. ChatGPT will only respond when @mentioned.")
	} else {
		ce.Reply("Mention-only mode **disabled**. ChatGPT will respond to all messages.")
	}
}

// cmdJoin adds ChatGPT to the current room by creating a bridge portal.
func (c *ChatGPTConnector) cmdJoin(ce *commands.Event) {
	c.Log.Debug().
		Bool("portal_exists", ce.Portal != nil).
		Strs("args", ce.Args).
		Msg("Join command: starting")

	// If already a portal, update model and ghost, then re-configure relay
	if ce.Portal != nil {
		c.Log.Debug().
			Str("portal_id", string(ce.Portal.PortalKey.ID)).
			Str("portal_mxid", string(ce.Portal.MXID)).
			Msg("Join command: portal already exists, updating model and relay")

		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("You are not logged in.")
			return
		}

		ctx, cancel := context.WithTimeout(ce.Ctx, 15*time.Second)
		defer cancel()

		// Get existing metadata to check current model
		portalMeta, _ := ce.Portal.Metadata.(*PortalMetadata)
		oldModel := ""
		if portalMeta != nil {
			oldModel = portalMeta.Model
		}
		if oldModel == "" {
			oldModel = c.Config.GetDefaultModel()
		}

		// Get model from args - model ID is passed directly
		model := c.Config.GetDefaultModel()
		if len(ce.Args) > 0 {
			model = strings.Join(ce.Args, "-")
			// Validate model ID format
			if err := ValidateModelID(model); err != nil {
				ce.Reply("Invalid model ID: %v", err)
				return
			}
		}

		c.Log.Debug().
			Str("old_model", oldModel).
			Str("new_model", model).
			Msg("Join command: updating model in existing portal")

		// Update portal metadata with new model
		if portalMeta == nil {
			portalMeta = &PortalMetadata{}
		}
		portalMeta.ConversationName = fmt.Sprintf("ChatGPT (%s)", model)
		portalMeta.Model = model
		ce.Portal.Metadata = portalMeta

		if err := ce.Portal.Save(ctx); err != nil {
			ce.Reply("Failed to save portal: %v", err)
			return
		}

		// Swap ghosts if model changed
		if err := c.swapGhosts(ctx, ce.RoomID, oldModel, model); err != nil {
			ce.Reply("Failed to update ChatGPT ghost: %v", err)
			return
		}

		// Re-set relay if enabled
		if c.br.Config.Relay.Enabled {
			if err := ce.Portal.SetRelay(ctx, login); err != nil {
				ce.Reply("Failed to set relay: %v", err)
				return
			}
		}

		if oldModel != model {
			ce.Reply("✓ **ChatGPT (%s)** has joined the room! (replaced %s)\n\nUse `model` to change models, `mention on` for mention-only mode, or `clear` to reset conversation.", model, oldModel)
		} else {
			ce.Reply("✓ **ChatGPT (%s)** is in the room! Relay updated.\n\nUse `model` to change models, `mention on` for mention-only mode, or `clear` to reset conversation.", model)
		}
		return
	}
	c.Log.Debug().Msg("Join command: no existing portal, creating new one")

	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You are not logged in.")
		return
	}

	client, ok := login.Client.(*ChatGPTClient)
	if !ok || client == nil {
		ce.Reply("Failed to get client.")
		return
	}

	// Determine model to use - model ID is passed directly
	model := c.Config.GetDefaultModel()
	c.Log.Debug().
		Strs("args", ce.Args).
		Str("default_model", model).
		Msg("Join command: parsing model from args")

	if len(ce.Args) > 0 {
		model = strings.Join(ce.Args, "-")
		// Validate model ID format
		if err := ValidateModelID(model); err != nil {
			ce.Reply("Invalid model ID: %v", err)
			return
		}
		c.Log.Debug().
			Str("model", model).
			Msg("Join command: using provided model")
	}

	// Get the room ID from the event
	roomID := ce.RoomID
	if roomID == "" {
		ce.Reply("Could not determine room ID.")
		return
	}

	c.Log.Info().
		Str("room_id", string(roomID)).
		Str("model", model).
		Str("user", string(ce.User.MXID)).
		Msg("Join command: adding ChatGPT to room")

	// Create a unique conversation/portal ID based on the room
	conversationID := fmt.Sprintf("room_%s", roomID)
	portalKey := MakeChatGPTPortalKey(conversationID)

	// Get or create the portal
	ctx := ce.Ctx
	portal, err := c.br.GetPortalByKey(ctx, portalKey)
	if err != nil {
		ce.Reply("Failed to get portal: %v", err)
		return
	}

	// Check if this portal already has a different room associated
	if portal.MXID != "" && portal.MXID != roomID {
		ce.Reply("This portal is associated with a different room. Please use a new conversation.")
		return
	}

	// Get the ghost for this model (with proper metadata)
	ghostID := c.MakeChatGPTGhostID(model)
	c.Log.Debug().
		Str("model", model).
		Str("ghost_id", string(ghostID)).
		Msg("Join command: resolved ghost ID from model")

	ghost, err := c.GetOrUpdateGhost(ctx, ghostID, model)
	if err != nil {
		ce.Reply("Failed to get ChatGPT ghost: %v", err)
		return
	}

	c.Log.Debug().
		Str("ghost_mxid", ghost.Intent.GetMXID().String()).
		Msg("Join command: got ghost intent")

	// Set up portal metadata
	chatName := fmt.Sprintf("ChatGPT (%s)", model)

	// Get existing metadata or create new
	portalMeta, _ := portal.Metadata.(*PortalMetadata)
	if portalMeta == nil {
		portalMeta = &PortalMetadata{}
	}
	portalMeta.ConversationName = chatName
	portalMeta.Model = model

	// Update the portal to use this room
	needsSave := false
	if portal.MXID == "" {
		// Link the existing Matrix room to this portal
		portal.MXID = roomID
		needsSave = true
	}

	// Always update metadata (model may have changed)
	portal.Metadata = portalMeta
	needsSave = true

	if needsSave {
		if err := portal.Save(ctx); err != nil {
			ce.Reply("Failed to save portal: %v", err)
			return
		}
		c.Log.Debug().
			Str("model", model).
			Str("portal_id", string(portal.PortalKey.ID)).
			Msg("Join command: saved portal metadata with model")
	}

	// Have the ghost join the room
	c.Log.Debug().
		Str("ghost_mxid", ghost.Intent.GetMXID().String()).
		Str("room_id", string(roomID)).
		Msg("Join command: attempting to have ghost join room")

	err = ghost.Intent.EnsureJoined(ctx, roomID)
	if err != nil {
		c.Log.Warn().Err(err).
			Str("ghost_mxid", ghost.Intent.GetMXID().String()).
			Str("room_id", string(roomID)).
			Msg("Failed to join room with ghost, trying invite first")

		// Try to invite and then join
		botIntent := c.br.Bot
		c.Log.Debug().
			Str("ghost_mxid", ghost.Intent.GetMXID().String()).
			Msg("Join command: attempting to invite ghost via bot")

		err = botIntent.EnsureInvited(ctx, roomID, ghost.Intent.GetMXID())
		if err != nil {
			c.Log.Error().Err(err).
				Str("ghost_mxid", ghost.Intent.GetMXID().String()).
				Msg("Join command: failed to invite ghost")
			ce.Reply("Failed to invite ChatGPT to this room: %v\n\nMake sure the bot has permission to invite users.", err)
			return
		}

		c.Log.Debug().Msg("Join command: invite succeeded, attempting join")
		err = ghost.Intent.EnsureJoined(ctx, roomID)
		if err != nil {
			c.Log.Error().Err(err).Msg("Join command: ghost failed to join after invite")
			ce.Reply("ChatGPT was invited but failed to join: %v", err)
			return
		}
	}
	c.Log.Debug().Msg("Join command: ghost successfully joined room")

	// Auto-set relay so other users in the room can also talk to ChatGPT
	if c.br.Config.Relay.Enabled {
		if err := portal.SetRelay(ctx, login); err != nil {
			c.Log.Warn().Err(err).Msg("Failed to set relay for portal")
		} else {
			c.Log.Debug().
				Str("relay_login", string(login.ID)).
				Msg("Auto-configured relay for portal")
		}
	}

	if c.br.Config.Relay.Enabled {
		ce.Reply("✓ **ChatGPT (%s)** has joined the room!\n\nAll users in this room can now chat with ChatGPT (messages relayed through your account).\n\nUse `model` to change models, `system` to set a custom prompt, `mention on` for mention-only mode, or `clear` to reset conversation.", model)
	} else {
		ce.Reply("✓ **ChatGPT (%s)** has joined the room!\n\n⚠️ **Note:** Relay mode is disabled. Only you can talk to ChatGPT. Enable `relay.enabled: true` in bridge config for multi-user support.\n\nUse `model` to change models, `system` to set a custom prompt, or `clear` to reset the conversation.", model)
	}

	c.Log.Info().
		Str("room_id", string(roomID)).
		Str("model", model).
		Str("ghost_id", string(ghostID)).
		Bool("relay_enabled", c.br.Config.Relay.Enabled).
		Msg("Successfully added ChatGPT to room")
}

// cmdTemperature views or sets the temperature.
func (c *ChatGPTConnector) cmdTemperature(ce *commands.Event) {
	if ce.Portal == nil {
		ce.Reply("This command must be run in a ChatGPT conversation room.")
		return
	}

	meta, ok := ce.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		ce.Reply("Failed to get room metadata.")
		return
	}

	// If no argument, show current temperature
	if len(ce.Args) == 0 {
		if meta.Temperature != nil {
			ce.Reply("**Current temperature:** %.2f\n\nUse `temperature <0-2>` to change, or `temperature reset` to use default.", *meta.Temperature)
		} else {
			ce.Reply("**Current temperature:** %.2f (default)\n\nUse `temperature <0-2>` to change.", c.Config.GetTemperature())
		}
		return
	}

	// Check for reset command
	if strings.ToLower(ce.Args[0]) == "reset" || strings.ToLower(ce.Args[0]) == "clear" {
		oldTemp := meta.Temperature
		meta.Temperature = nil
		if err := ce.Portal.Save(ce.Ctx); err != nil {
			meta.Temperature = oldTemp // Rollback in-memory state on save failure
			ce.Reply("Failed to reset temperature: %v", err)
			return
		}
		ce.Reply("Temperature reset to default (%.2f).", c.Config.GetTemperature())
		return
	}

	// Parse temperature value
	var temp float64
	if _, err := fmt.Sscanf(ce.Args[0], "%f", &temp); err != nil {
		ce.Reply("Invalid temperature value. Use a number between 0 and 2.")
		return
	}

	if temp < 0 || temp > 2 {
		ce.Reply("Temperature must be between 0 and 2.")
		return
	}

	oldTemp := meta.Temperature
	meta.Temperature = &temp
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		meta.Temperature = oldTemp // Rollback in-memory state on save failure
		ce.Reply("Failed to save temperature: %v", err)
		return
	}

	ce.Reply("Temperature set to %.2f.", temp)
}

// cmdRemoveGhost removes a bridge ghost from the current room.
func (c *ChatGPTConnector) cmdRemoveGhost(ce *commands.Event) {
	if len(ce.Args) == 0 {
		ce.Reply("Usage: `remove-ghost <@user:server>`\n\nExample: `remove-ghost @chatgpt_gpt_4o:example.com`")
		return
	}

	// Parse the Matrix user ID
	userIDStr := ce.Args[0]
	if !strings.HasPrefix(userIDStr, "@") {
		ce.Reply("Invalid Matrix user ID. Must start with @")
		return
	}

	userID := id.UserID(userIDStr)

	// Verify it's a ghost controlled by this bridge
	if _, isGhost := c.br.Matrix.ParseGhostMXID(userID); !isGhost {
		ce.Reply("User `%s` is not a ghost controlled by this bridge.", userID)
		return
	}

	// Get the room ID
	roomID := ce.RoomID
	if ce.Portal != nil && ce.Portal.MXID != "" {
		roomID = ce.Portal.MXID
	}

	if roomID == "" {
		ce.Reply("Could not determine room ID.")
		return
	}

	// Get the ghost intent and make it leave
	matrixConn, ok := c.br.Matrix.(*matrix.Connector)
	if !ok {
		ce.Reply("Failed to access Matrix connector.")
		return
	}

	// Create an intent for the ghost user
	ghostIntent := matrixConn.AS.Intent(userID)

	ctx, cancel := context.WithTimeout(ce.Ctx, 30*time.Second)
	defer cancel()

	// Make the ghost leave the room
	if _, err := ghostIntent.LeaveRoom(ctx, roomID); err != nil {
		ce.Reply("Failed to remove ghost from room: %v", err)
		return
	}

	c.Log.Info().
		Str("ghost", string(userID)).
		Str("room", string(roomID)).
		Str("admin", string(ce.User.MXID)).
		Msg("Admin removed ghost from room")

	ce.Reply("Successfully removed `%s` from this room.", userID)
}
