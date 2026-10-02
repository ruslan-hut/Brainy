# Brainy

A Telegram chatbot backed by OpenAI. It keeps a per-chat conversation history, streams replies as they are generated, draws images on request, and adapts its tone to each user. It works in private chats (invite-only) and in groups.

## Features

- **Conversations with memory** — each chat keeps its own history (trimmed to the most recent ~20,000 tokens), with an optional topic that steers the conversation.
- **Streaming replies** — in private chats replies stream as a native Telegram draft; in groups the bot posts a message and updates it as text arrives. Long answers are split across several messages.
- **Image generation** — `/imagine <description>`, or just ask for a picture in conversation and the model decides to draw one.
- **Personalisation** — the bot infers each user's language, tone and preferred detail level from their messages, or the user sets them with the `/tuneup` wizard.
- **Dictionary lookups** — Catalan and Spanish word articles with translation, grammar and examples, written in the user's language.
- **Invite-only access** — private chats require an invite code; admins generate and list codes from the bot. In groups, membership is the access check.
- **Storage** — MongoDB for persistence, or in memory for local runs.

## Requirements

- Go 1.25+
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- An OpenAI API key
- MongoDB (optional; without it everything is kept in memory and lost on restart)

## Quick start

1. Clone the repository.
2. Edit `config.yml` — at minimum set `telegram_api_key`, `openai_api_key` and `username` (the bot's username without `@`). Add your Telegram user ID to `admin_user_ids` so you can generate invite codes.
3. Run:

   ```bash
   go run main.go                       # uses ./config.yml
   go run main.go -conf /path/to/config.yml
   ```

4. Open a chat with the bot, send `/start`, then `/gencode` (as admin) to create invite codes for other users.

## Configuration

| Key | Default | Description |
|---|---|---|
| `env` | `local` | `local` / `dev` log at debug level, `prod` at info |
| `telegram_api_key` | — | Bot token from BotFather |
| `openai_api_key` | — | OpenAI API key |
| `username` | — | Bot username, used to detect mentions in groups |
| `model` | `gpt-6-luna` | Chat model |
| `reasoning_effort` | `low` | Reasoning effort for `/ask`, `/hello`, dictionary lookups and preference analysis; leave empty to use the model's default. Regular chat always uses `none`, which tool calling requires |
| `image_model` | `gpt-image-2` | Image model (`gpt-image-*`) |
| `image_size` | `1024x1024` | Generated image size |
| `image_style` | cartoon style | Text appended to every image prompt |
| `admin_user_ids` | — | Telegram user IDs registered as admins on startup |
| `mongo.enabled` | `false` | Use MongoDB; if the connection fails the bot falls back to memory |
| `mongo.host`, `port`, `user`, `password`, `database` | — | MongoDB connection; the user authenticates against `database` |

## Commands

| Command | Description |
|---|---|
| `/start [code]` | Register with an invite code (private chats) |
| `/help` | List commands |
| `/ask <question>` | One-off question without conversation history. In private chats you can just write |
| `/topic [subject]` | Set the conversation subject; without a subject, show the current one with change/clear buttons |
| `/clear` | Forget the conversation and topic |
| `/imagine <description>` | Generate an image |
| `/cat <word>`, `/cas <word>` | Catalan / Spanish dictionary article |
| `/hello` | A random science fact |
| `/menu` | Quick actions: set topic, clear context (private chats) |
| `/tuneup` | Set language, tone, length, humour and technical level (private chats) |

Admin commands — replies always go to the admin's private chat:

| Command | Description |
|---|---|
| `/admin` | Admin menu |
| `/gencode` | Generate an invite code |
| `/codes` | List recent invite codes and who used them |
| `/showid` | Show the current chat's ID and type |

### Groups

In a group the bot answers commands, messages that mention it (`@username`), and replies to its own messages. `/topic` and `/clear` are limited to admins there, since they affect the whole group's conversation.

### Personalisation

Preferences are analysed from the user's messages once a day for users with new activity, and again whenever they run `/clear`. Preferences set with `/tuneup` are never overwritten by the analysis.

## Development

```bash
go test -short -race ./...                                          # unit tests
MONGO_TEST_URI=mongodb://127.0.0.1:27017 go test -race ./storage/   # MongoDB integration tests
```

The integration tests create and drop a temporary database. A throwaway server is enough:

```bash
docker run -d --rm -p 27017:27017 mongo:7
```

## Deployment

Pushing to `master` runs `.github/workflows/deploy.yml`, which:

1. fills `brainy.yml` from repository secrets and copies it to `/etc/conf/` on the server;
2. builds the binary and copies it to `/usr/local/bin/brainy`;
3. restarts `brainy.service` (the systemd unit lives on the server, not in this repo).

Required secrets: `TELEGRAM_API_KEY`, `OPENAI_API_KEY`, `BOT_USERNAME`, `MONGO_HOST`, `MONGO_PORT`, `MONGO_USER`, `MONGO_PASSWORD`, `MONGO_DATABASE`, `SERVER_IP`, `SERVER_USER`, `SSH_PRIVATE_KEY`.

On `SIGTERM` the bot stops taking new messages and gives replies already in progress up to 60 seconds to finish.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
