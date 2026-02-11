package connector

import (
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/id"
)

// GhostQueryHandler implements appservice.QueryHandler to tell the homeserver
// which ghost users exist. Without this, the homeserver won't allow inviting
// ghost users to rooms because it thinks they don't exist.
type GhostQueryHandler struct {
	Matrix *matrix.Connector
	Log    zerolog.Logger
}

// QueryAlias handles alias existence queries. We don't use room aliases.
func (q *GhostQueryHandler) QueryAlias(alias id.RoomAlias) bool {
	q.Log.Debug().Str("alias", string(alias)).Msg("QueryAlias called")
	return false
}

// QueryUser handles user existence queries from the homeserver.
// Returns true if the user ID belongs to a valid ChatGPT ghost user.
func (q *GhostQueryHandler) QueryUser(userID id.UserID) bool {
	q.Log.Debug().Str("user_id", string(userID)).Msg("QueryUser called")

	// Check if this user ID is in our ghost namespace
	ghostID, isGhost := q.Matrix.ParseGhostMXID(userID)
	q.Log.Debug().
		Str("user_id", string(userID)).
		Str("ghost_id", string(ghostID)).
		Bool("is_ghost", isGhost).
		Msg("ParseGhostMXID result")

	if !isGhost {
		q.Log.Debug().Str("user_id", string(userID)).Msg("QueryUser: not a ghost, returning false")
		return false
	}

	// Ghost IDs are sanitized model IDs (e.g., "chatgpt_gpt_4o")
	// We accept any ghost ID that starts with our prefix or is one of our special IDs
	ghostIDStr := string(ghostID)
	if ghostIDStr != "" {
		// Accept "error" for error notices, and any model-based ghost ID
		q.Log.Info().Str("user_id", string(userID)).Str("ghost_id", ghostIDStr).Msg("QueryUser: valid ghost, returning true")
		return true
	}

	q.Log.Debug().Str("user_id", string(userID)).Str("ghost_id", ghostIDStr).Msg("QueryUser: empty ghost ID, returning false")
	return false
}
