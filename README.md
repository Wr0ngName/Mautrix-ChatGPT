# mautrix-chatgpt

A Matrix bridge for OpenAI ChatGPT, built on the [mautrix](https://github.com/mautrix) bridgev2 framework in Go. Chat with GPT models directly from any Matrix client.

## Features

- **Multiple models** -- GPT-4o, GPT-4 Turbo, GPT-3.5 Turbo, o1, o1-mini, and any new models from OpenAI
- **Vision** -- send images and get descriptions/analysis via the OpenAI Vision API
- **Streaming responses** -- responses stream in real-time with typing indicators
- **Conversation context** -- per-room history with automatic compaction when approaching token limits
- **Per-room configuration** -- model, system prompt, and temperature are configurable per room
- **Rate limiting** -- configurable per-user rate limits to prevent API quota exhaustion
- **Encryption** -- optional end-to-bridge encryption support

## Requirements

- A Matrix homeserver (Synapse, Dendrite, Conduit, etc.)
- An OpenAI API key from [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
- Docker (recommended) or Go 1.24+

## Quick start (Docker)

```bash
git clone https://github.com/mautrix/chatgpt.git
cd chatgpt
docker compose up -d
```

On first run, the container generates a default config at `data/config.yaml` and exits. Edit it with your homeserver details, then start again:

```bash
docker compose up -d
```

On second run (without a registration file), it generates `data/registration.yaml` and exits. Register the appservice with your homeserver (see [Registering appservices](https://docs.mau.fi/bridges/general/registering-appservices.html)), then start a final time:

```bash
docker compose up -d
```

The bridge is now running. Start a DM with `@chatgptbot:yourdomain` and send `login` to authenticate with your OpenAI API key.

## Quick start (standalone)

```bash
go build -o mautrix-chatgpt ./cmd/mautrix-chatgpt

# Generate example config
./mautrix-chatgpt -c config.yaml -e

# Edit config.yaml, then generate registration
./mautrix-chatgpt -g -c config.yaml -r registration.yaml

# Register appservice with your homeserver, then run
./mautrix-chatgpt -c config.yaml
```

## Configuration

See [`example-config.yaml`](example-config.yaml) for all options. Key network settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `network.default_model` | `gpt-4o` | Default model for new conversations |
| `network.max_tokens` | `4096` | Maximum tokens per response |
| `network.temperature` | `1.0` | Response randomness (0.0--2.0) |
| `network.system_prompt` | `You are a helpful AI assistant.` | Default system prompt |
| `network.conversation_max_age_hours` | `24` | Hours before conversation context expires (0 = unlimited) |
| `network.rate_limit_per_minute` | `60` | Max requests per user per minute |

## Bridge commands

All commands use the prefix `!chatgpt` (configurable via `bridge.command_prefix`).

| Command | Description |
|---------|-------------|
| `login` | Log in with your OpenAI API key |
| `logout` | Log out and clear credentials |
| `join [model]` | Add ChatGPT to the current room |
| `model [name]` | View or change the model for this room |
| `models` | List available models from OpenAI |
| `system [prompt]` | View or set the system prompt |
| `temperature [0-2]` | View or set the temperature |
| `clear` | Clear conversation history |
| `stats` | Show conversation statistics and token usage |
| `help` | Show all available commands |

## Vision

Send an image to a room with ChatGPT and it will analyze it using the Vision API. Add a caption to ask a specific question about the image, or send it without a caption for a general description. Supported formats: JPEG, PNG, GIF, WebP.

## Building the Docker image

```bash
docker build \
  --build-arg COMMIT_HASH=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -Iseconds) \
  --build-arg VERSION=1.0.0 \
  -t mautrix-chatgpt:latest .
```

## Database

SQLite is used by default (`sqlite:mautrix-chatgpt.db`). PostgreSQL is also supported -- uncomment the postgres service in `docker-compose.yaml` and update `appservice.database` in your config:

```yaml
appservice:
    database: postgres://mautrix:changeme@postgres/mautrix_chatgpt?sslmode=disable
```

## License

AGPL-3.0
