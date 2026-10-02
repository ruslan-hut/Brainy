# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run Commands

```bash
# Run the bot (uses config.yml by default)
go run main.go

# Run with custom config
go run main.go -conf /path/to/config.yml

# Unit tests (skips MongoDB integration tests)
go test -short -race ./...

# Integration tests against a real MongoDB
MONGO_TEST_URI=mongodb://127.0.0.1:27017 go test -race ./storage/
```

Pushing to `master` deploys to production (`.github/workflows/deploy.yml` builds, copies `brainy.yml` with secrets substituted, restarts `brainy.service`).

## Configuration

`config.yml` (local) / `brainy.yml` (prod), loaded with `cleanenv` in `core/config.go`:
- `env`: local/dev/prod — affects log level
- `telegram_api_key`, `openai_api_key`, `username` (bot username, for mention detection in groups)
- `model`: chat model (default `gpt-6-luna`)
- `reasoning_effort`: for tool-less calls (`/ask`, translations, preferences analysis); empty omits it
- `image_model`, `image_size`, `image_style`: image generation (`gpt-image-*` only)
- `admin_user_ids`: seeded as admins on startup
- `mongo`: when `enabled`, all storages use MongoDB; otherwise (or on connect failure) in-memory

## Architecture

Telegram bot (`go-telegram-bot-api/v5`) backed by the OpenAI Chat Completions API (`openai-go/v3`).

- **main.go**: entry point; wires storages, `ai.ChatGPT`, `ai.PreferencesAnalyzer`, `bot.TgBot`; graceful shutdown on SIGINT/SIGTERM
- **core/**: config singleton and the `ChatService` interface the bot depends on
- **ai/chat-gpt.go**: chat (streaming, with a `generate_image` function tool), one-shot prompts, translations, image generation
- **ai/preferences_analyzer.go**: background job inferring user preferences via structured output; injected into the system prompt
- **bot/**: update loop and commands (`tgbot.go`), streaming output (`stream-editor.go`), message splitting (`message-split.go`), `/menu`, `/tuneup` wizard, admin/invite handling
- **holder/**: thin wrapper over `storage.ContextStorage` for dialog history
- **storage/**: MongoDB (driver v2) and in-memory implementations for dialog contexts (trimmed at 20000 tokens), preferences, users, invites
- **lib/tokens/**: tiktoken-based counting; unknown `gpt-*` models map to an encoding by prefix

### Key Flow

1. `TgBot` receives a message and calls `ChatService.AskStream`
2. `ChatGPT` builds messages: system prompt (preferences + topic), stored history, the question
3. If the model calls `generate_image`, the bot generates and sends an image instead of text; history is not updated
4. Otherwise the reply streams to Telegram, is stored in the dialog context, and the final text is sent as MarkdownV2 (plain-text fallback)

### OpenAI constraints

- Chat Completions allows function calling only with `reasoning_effort: none`, so the tool-using chat path pins it; don't make it configurable.
- `gpt-image-*` always returns base64; `response_format` is rejected.

### Telegram constraints

- Messages are limited to 4096 UTF-16 units; all outgoing text goes through `splitMessage`, which also keeps code fences balanced per chunk.
- Private chats stream via `sendMessageDraft` (called through `api.MakeRequest`, since the library predates it); groups stream by editing a posted message. A draft is never persisted — the final reply is always sent as a regular message.

## Bot Commands

- `/start [code]` — invite-code onboarding (private chats are invite-only; groups are open to members)
- `/ask <question>` — one-shot question (plain messages work in private chats)
- `/topic <subject>` — set conversation subject; without args shows the topic menu
- `/clear` — clear conversation context and topic (triggers preferences analysis)
- `/cat <word>`, `/cas <word>` — Catalan/Spanish dictionary article in the user's language
- `/hello` — random science fact in the user's language
- `/imagine <prompt>` — generate an image
- `/menu`, `/tuneup` (preferences wizard), `/help`
- Admin: `/admin`, `/gencode`, `/codes`, `/showid` — responses go to the admin's DM

### Group Chat Behavior

Bot responds in groups when:
- Message is a command
- Bot is mentioned (@username)
- Message is a reply to a bot's message
