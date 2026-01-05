# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run Commands

```bash
# Run the bot (uses config.yml by default)
go run main.go

# Run with custom config
go run main.go -conf /path/to/config.yml

# Install dependencies
go get ./...
```

## Configuration

The bot requires a `config.yml` file with:
- `env`: Environment (local/dev/prod) - affects log level
- `telegram_api_key`: Telegram bot token from BotFather
- `openai_api_key`: OpenAI API key
- `username`: Bot's Telegram username (for mention detection in groups)
- `mongo`: Optional MongoDB configuration (currently not fully implemented)

## Architecture

This is a Telegram bot that integrates with OpenAI's GPT API to provide conversational AI responses.

### Package Structure

- **main.go**: Entry point, sets up logger based on env, initializes bot and AI chat service
- **core/**: Configuration loading (singleton pattern via `sync.Once`) and `ChatService` interface
- **bot/**: Telegram bot implementation using `go-telegram-bot-api`
- **ai/**: OpenAI API integration - sends chat completion requests to `gpt-4.1-mini` model
- **holder/**: In-memory conversation context management with token-based limiting (20000 tokens max)
- **lib/sl/**: Structured logging helpers for `slog`

### Key Flow

1. `TgBot` receives messages and dispatches to `ChatGPT.GetResponse()`
2. `ChatGPT` builds prompts using `ContextManager` to include conversation history
3. Context is per-user/chat, automatically trimmed when exceeding token limit
4. Responses are sanitized for Telegram MarkdownV2 before sending

### Bot Commands

Commands are processed in `ai/chat-gpt.go:composePrompt()`:
- `/ask <question>` - Ask a question (not needed in private chats)
- `/topic <subject>` - Set conversation subject
- `/clear` - Clear conversation context and topic
- `/cat <word>` - Catalan-English translation
- `/cas <word>` - Spanish-English translation
- `/hello` - Random science fact in Ukrainian
- `/help` - Show help (handled in `bot/tgbot.go`)

### Group Chat Behavior

Bot responds in groups when:
- Message is a command
- Bot is mentioned (@username)
- Message is a reply to a bot's message
