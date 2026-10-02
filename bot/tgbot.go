package bot

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"Brainy/storage"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const streamEditInterval = 400 * time.Millisecond

var smileEmojis = []string{
	"😊", "😄", "😁", "🙂", "😉", "🤗", "😇", "🥰", "😎", "🤔",
	"👀", "🙈", "🤷", "👍", "✨", "🎉", "💫", "🌟", "🔥", "💯",
}

const errorResponse = "Sorry, I'm not feeling well today. Please try again later."

type TgBot struct {
	conf          *core.Config
	log           *slog.Logger
	api           *tg.Bot
	chat          core.ChatService
	prefs         storage.PreferencesStorage
	users         storage.UsersStorage
	invites       storage.InvitesStorage
	awaiting      sync.Map // userID -> struct{} : user prompted for invite code, next message is the code
	awaitingTopic sync.Map // userID -> struct{} : /menu topic button pressed, next message is the topic
	wizard        *wizardManager
	botUsername   string
	// replies tracks goroutines started by goSafe so Start can wait for them
	// before the caller closes storage.
	replies sync.WaitGroup
	// draftsUnsupported is set once sendMessageDraft fails, so later replies
	// stream by editing instead of retrying drafts.
	draftsUnsupported atomic.Bool
}

func NewTgBot(conf *core.Config, log *slog.Logger) (*TgBot, error) {
	t := &TgBot{
		conf:        conf,
		log:         log.With(sl.Module("tgbot")),
		botUsername: conf.Username,
	}

	// Updates are routed one at a time, as before; slow work (LLM calls)
	// is moved off the update loop with goSafe.
	api, err := tg.New(conf.TelegramApiKey,
		tg.WithDefaultHandler(t.handleUpdate),
		tg.WithNotAsyncHandlers(),
		tg.WithErrorsHandler(func(err error) {
			if !errors.Is(err, context.Canceled) {
				t.log.Error("telegram", sl.Err(err))
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("creating api instance: %w", err)
	}
	t.api = api

	return t, nil
}

// SetChat set chat service
func (t *TgBot) SetChat(chat core.ChatService) {
	t.chat = chat
}

// SetPreferences enables the /tuneup wizard backed by the given storage.
func (t *TgBot) SetPreferences(prefs storage.PreferencesStorage) {
	t.prefs = prefs
	t.wizard = newWizardManager(t.api, prefs, t.log)
}

// SetAccessControl enables role-based gating and the invite-code system.
// Admin IDs from config are seeded as admins on startup.
func (t *TgBot) SetAccessControl(ctx context.Context, users storage.UsersStorage, invites storage.InvitesStorage, adminIds []int64) {
	t.users = users
	t.invites = invites
	t.seedAdmins(ctx, adminIds)
}

// Start polls for updates until ctx is cancelled, then waits for in-flight
// replies to finish.
func (t *TgBot) Start(ctx context.Context) {
	t.api.Start(ctx)
	t.replies.Wait()
	t.log.Info("bot stopped")
}

func isPrivate(chat models.Chat) bool {
	return chat.Type == models.ChatTypePrivate
}

// parseCommand returns the command name (without the leading "/" and any
// "@botname" suffix) and its arguments when msg starts with a bot command.
func parseCommand(msg *models.Message) (cmd, args string, ok bool) {
	for _, e := range msg.Entities {
		if e.Type != models.MessageEntityTypeBotCommand || e.Offset != 0 {
			continue
		}
		// Commands are ASCII, so the UTF-16 entity length equals the byte length.
		if e.Length < 2 || e.Length > len(msg.Text) {
			return "", "", false
		}
		cmd, _, _ = strings.Cut(msg.Text[1:e.Length], "@")
		return cmd, strings.TrimSpace(msg.Text[e.Length:]), true
	}
	return "", "", false
}

// requirePrivate refuses management commands in non-private chats and tells
// the user to DM the bot. Returns true if the chat is private.
func (t *TgBot) requirePrivate(ctx context.Context, chat models.Chat) bool {
	if isPrivate(chat) {
		return true
	}
	t.plainResponse(ctx, chat.ID, "This command is only available in a direct chat with me.")
	return false
}

// adminTarget returns the chat ID to use for an admin command's response.
// If invoked in a non-private chat, deletes the original message and redirects
// to the admin's DM (their user ID == private chat ID in Telegram).
func (t *TgBot) adminTarget(ctx context.Context, msg *models.Message) int64 {
	if isPrivate(msg.Chat) {
		return msg.Chat.ID
	}
	t.deleteMessage(ctx, msg.Chat.ID, msg.ID)
	return msg.From.ID
}

// userLanguage returns the user's preferred language, or fallback if not set.
func (t *TgBot) userLanguage(ctx context.Context, userId int64, fallback string) string {
	if t.prefs == nil {
		return fallback
	}
	p, err := t.prefs.GetUserPreferences(ctx, userId)
	if err != nil || p == nil || p.PreferredLanguage == "" {
		return fallback
	}
	return p.PreferredLanguage
}

// handleUpdate processes a single update. A panic in any handler is logged
// and the bot keeps serving other updates.
func (t *TgBot) handleUpdate(ctx context.Context, _ *tg.Bot, update *models.Update) {
	defer t.recoverPanic()

	if cb := update.CallbackQuery; cb != nil {
		switch {
		case t.wizard != nil && strings.HasPrefix(cb.Data, callbackPrefix+":"):
			t.wizard.handleCallback(ctx, cb)
		case strings.HasPrefix(cb.Data, adminCallbackPrefix+":"):
			t.handleAdminCallback(ctx, cb)
		case strings.HasPrefix(cb.Data, menuCallbackPrefix+":"):
			t.handleMenuCallback(ctx, cb)
		}
		return
	}
	incoming := update.Message
	if incoming == nil || incoming.From == nil {
		return
	}

	chat := incoming.Chat
	question := incoming.Text
	cmd, args, isCommand := parseCommand(incoming)

	if !isCommand && !isPrivate(chat) && !t.isMentioned(incoming.Text) && !t.isReplyToBot(incoming) {
		return
	}

	// Check for non-text messages (images, voice, stickers, etc.)
	if question == "" {
		t.log.With(
			slog.String("user", chat.Username),
			slog.Int64("id", chat.ID),
		).Debug("non-text message received")
		t.sendRandomEmoji(ctx, chat.ID)
		return
	}

	// Access control: invites only gate DMs. In groups, presence in
	// the chat implies access — group membership is the authorization.
	if t.users != nil && isPrivate(chat) {
		if isCommand && cmd == "start" {
			t.handleStart(ctx, chat.ID, incoming.From.ID, incoming.ID, chat.Username, args)
			return
		}
		if !t.isAuthorized(ctx, incoming.From.ID) {
			if _, awaiting := t.awaiting.Load(incoming.From.ID); awaiting && !isCommand {
				t.handleInviteSubmission(ctx, chat.ID, incoming.From.ID, incoming.ID, chat.Username, question)
				return
			}
			t.plainResponse(ctx, chat.ID, "Access is by invite only. Send /start to begin.")
			return
		}
	}

	// Capture topic submission triggered from /menu.
	if !isCommand {
		if _, ok := t.awaitingTopic.LoadAndDelete(incoming.From.ID); ok {
			topic := strings.TrimSpace(question)
			if topic == "" {
				t.plainResponse(ctx, chat.ID, "Topic was empty, nothing changed.")
				return
			}
			t.chat.SetTopic(ctx, chat.ID, topic)
			t.plainResponse(ctx, chat.ID, "Topic set: "+topic)
			return
		}
	}

	if isCommand {
		switch cmd {
		case "help":
			text := "You can use the following commands:\n"
			text += "/help - show this help\n"
			text += "/hello - bot says random fact\n"
			text += "/topic - set a subject of conversation\n"
			text += "/ask - ask something or just reply on previous bot message\n"
			text += "/cat - Catalan-English dictionary lookup\n"
			text += "/cas - Spanish-English dictionary lookup\n"
			text += "/imagine - generate an image from description\n"
			text += "/clear - clear bot memory to begin new topic\n"
			text += "/tuneup - configure tone, length, language, etc.\n"
			text += "/menu - quick actions (set topic, clear context)\n"
			if t.isAdmin(ctx, incoming.From.ID) {
				text += "\nAdmin commands:\n"
				text += "/admin - show admin menu\n"
				text += "/gencode - generate an invite code\n"
				text += "/codes - list invite codes\n"
				text += "/showid - show current chat id\n"
			}
			t.plainResponse(ctx, chat.ID, text)
			return
		case "admin":
			if !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			target := t.adminTarget(ctx, incoming)
			if _, err := t.api.SendMessage(ctx, &tg.SendMessageParams{
				ChatID:      target,
				Text:        "Admin menu:",
				ReplyMarkup: adminMenuKeyboard(),
			}); err != nil {
				t.log.With(slog.Int64("id", target)).Warn("admin menu", sl.Err(err))
			}
			return
		case "gencode":
			if !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			target := t.adminTarget(ctx, incoming)
			code, err := t.generateAndSaveCode(ctx, incoming.From.ID)
			if err != nil {
				t.plainResponse(ctx, target, "Failed to generate code: "+err.Error())
				return
			}
			t.plainResponse(ctx, target, "New invite code: "+code)
			return
		case "codes":
			if !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			target := t.adminTarget(ctx, incoming)
			t.plainResponse(ctx, target, t.formatInviteList(ctx))
			return
		case "showid":
			if !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			target := t.adminTarget(ctx, incoming)
			t.plainResponse(ctx, target, fmt.Sprintf("Chat ID: %d\nType: %s\nTitle: %s", chat.ID, chat.Type, chat.Title))
			return
		case "ask":
			if args == "" {
				t.plainResponse(ctx, chat.ID, "Please provide a question. Example: /ask what is the capital of France?")
				return
			}
			prompt := args
			if lang := t.userLanguage(ctx, incoming.From.ID, ""); lang != "" {
				prompt = fmt.Sprintf("Answer in %s. %s", lang, args)
			}
			t.goSafe(func() { t.sendOneShot(ctx, chat.ID, prompt) })
			return
		case "cat":
			if args == "" {
				t.plainResponse(ctx, chat.ID, "Please provide a word. Example: /cat poma")
				return
			}
			lang := t.userLanguage(ctx, incoming.From.ID, "English")
			t.goSafe(func() { t.sendTranslate(ctx, chat.ID, "Catalan", args, lang) })
			return
		case "cas":
			if args == "" {
				t.plainResponse(ctx, chat.ID, "Please provide a word. Example: /cas manzana")
				return
			}
			lang := t.userLanguage(ctx, incoming.From.ID, "English")
			t.goSafe(func() { t.sendTranslate(ctx, chat.ID, "Spanish", args, lang) })
			return
		case "hello":
			lang := t.userLanguage(ctx, incoming.From.ID, "English")
			t.goSafe(func() {
				t.sendOneShot(ctx, chat.ID, fmt.Sprintf("Answer in %s: Say one random fact from science.", lang))
			})
			return
		case "topic":
			if !isPrivate(chat) && !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			if args != "" {
				t.chat.SetTopic(ctx, chat.ID, args)
				t.plainResponse(ctx, chat.ID, "Let's talk about "+args+".")
				return
			}
			current := t.chat.GetTopic(ctx, chat.ID)
			if current == "" {
				t.plainResponse(ctx, chat.ID, "No topic set. Provide a subject. Example: /topic astronomy")
				return
			}
			t.sendTopicMenu(ctx, chat.ID, current)
			return
		case "imagine":
			if args == "" {
				t.plainResponse(ctx, chat.ID, "Please provide a description for the image. Example: /imagine a sunset over mountains")
				return
			}
			t.goSafe(func() { t.SendImageResponse(ctx, chat.ID, args) })
			return
		case "menu":
			if !t.requirePrivate(ctx, chat) {
				return
			}
			t.sendMenu(ctx, chat.ID)
			return
		case "tuneup":
			if !isPrivate(chat) && !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			if !t.requirePrivate(ctx, chat) {
				return
			}
			if t.wizard == nil {
				t.plainResponse(ctx, chat.ID, "Tuning is not available right now.")
				return
			}
			t.wizard.start(ctx, chat.ID, incoming.From.ID)
			return
		case "clear":
			if !isPrivate(chat) && !t.isAdmin(ctx, incoming.From.ID) {
				return
			}
			t.log.With(
				slog.String("user", chat.Username),
				slog.Int64("id", chat.ID),
			).Info("context cleared")
			t.chat.ClearContext(ctx, chat.ID)
			t.plainResponse(ctx, chat.ID, "context cleared")
			return
		}
	}
	if t.isMentioned(incoming.Text) {
		question = strings.ReplaceAll(question, "@"+t.botUsername, "")
	}

	logText := question
	if len(logText) > 50 {
		logText = logText[:50] + "..."
	}
	t.log.With(
		slog.String("user", chat.Username),
		slog.Int64("id", chat.ID),
		slog.String("text", logText),
	).Info("incoming message")

	t.goSafe(func() { t.SendResponse(ctx, chat.ID, isPrivate(chat), question) })
}

// goSafe runs fn in a tracked goroutine, logging instead of crashing on panic.
func (t *TgBot) goSafe(fn func()) {
	t.replies.Add(1)
	go func() {
		defer t.replies.Done()
		defer t.recoverPanic()
		fn()
	}()
}

func (t *TgBot) recoverPanic() {
	if r := recover(); r != nil {
		t.log.Error("recovered from panic", slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
	}
}

func (t *TgBot) sendChatAction(ctx context.Context, chatId int64) {
	if _, err := t.api.SendChatAction(ctx, &tg.SendChatActionParams{
		ChatID: chatId,
		Action: models.ChatActionTyping,
	}); err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("sending chat action", sl.Err(err))
	}
}

func (t *TgBot) sendRandomEmoji(ctx context.Context, chatId int64) {
	emoji := smileEmojis[rand.Intn(len(smileEmojis))]
	if _, err := t.api.SendMessage(ctx, &tg.SendMessageParams{ChatID: chatId, Text: emoji}); err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("sending emoji", sl.Err(err))
	}
}

func (t *TgBot) SendResponse(ctx context.Context, chatId int64, private bool, request string) {
	t.sendChatAction(ctx, chatId)

	editor := newStreamEditor(t, chatId, private)
	editor.start(ctx)

	resp, err := t.chat.AskStream(ctx, chatId, request, editor.update)
	editor.stop()

	if err != nil {
		if ctx.Err() != nil {
			t.log.With(slog.Int64("id", chatId)).Info("reply cancelled by shutdown")
			return
		}
		t.log.With(slog.Int64("id", chatId)).Error("composing reply", sl.Err(err))
		editor.deleteIfPosted(ctx)
		t.plainResponse(ctx, chatId, errorResponse)
		return
	}
	if resp.ImagePrompt != "" {
		editor.deleteIfPosted(ctx)
		t.withTyping(ctx, chatId, func() {
			t.generateAndSendImage(ctx, chatId, resp.ImagePrompt)
		})
		return
	}
	editor.finalize(ctx, resp.Text)
}

func (t *TgBot) sendOneShot(ctx context.Context, chatId int64, prompt string) {
	t.withTyping(ctx, chatId, func() {
		text, err := t.chat.OneShot(ctx, prompt)
		if err != nil {
			t.log.With(slog.Int64("id", chatId)).Error("one-shot reply", sl.Err(err))
			t.plainResponse(ctx, chatId, errorResponse)
			return
		}
		t.plainResponse(ctx, chatId, text)
	})
}

func (t *TgBot) sendTranslate(ctx context.Context, chatId int64, language, word, responseLanguage string) {
	t.withTyping(ctx, chatId, func() {
		text, err := t.chat.Translate(ctx, language, word, responseLanguage)
		if err != nil {
			t.log.With(slog.Int64("id", chatId)).Error("translate reply", sl.Err(err))
			t.plainResponse(ctx, chatId, errorResponse)
			return
		}
		t.plainResponse(ctx, chatId, text)
	})
}

// withTyping shows a typing indicator while fn runs.
func (t *TgBot) withTyping(ctx context.Context, chatId int64, fn func()) {
	stop := make(chan struct{})
	t.sendChatAction(ctx, chatId)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.sendChatAction(ctx, chatId)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	defer close(stop)
	fn()
}

// SendImageResponse generates and sends an image
func (t *TgBot) SendImageResponse(ctx context.Context, chatId int64, prompt string) {
	t.withTyping(ctx, chatId, func() {
		t.generateAndSendImage(ctx, chatId, prompt)
	})
}

func (t *TgBot) generateAndSendImage(ctx context.Context, chatId int64, prompt string) {
	data, err := t.chat.GenerateImage(ctx, chatId, prompt)
	if err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("generating image", sl.Err(err))
		t.plainResponse(ctx, chatId, "Sorry, I couldn't generate the image. Please try again with a different description.")
		return
	}
	if _, err := t.api.SendPhoto(ctx, &tg.SendPhotoParams{
		ChatID: chatId,
		Photo:  &models.InputFileUpload{Filename: "image.png", Data: bytes.NewReader(data)},
	}); err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("sending image", sl.Err(err))
		t.plainResponse(ctx, chatId, "Sorry, I couldn't send the image.")
	}
}

func (t *TgBot) plainResponse(ctx context.Context, chatId int64, text string) {
	for _, chunk := range splitMessage(text, maxMessageLen) {
		t.sendFormatted(ctx, chatId, chunk)
	}
}

// sendFormatted sends a single message-sized chunk as MarkdownV2, falling back
// to plain text if Telegram rejects the markup.
func (t *TgBot) sendFormatted(ctx context.Context, chatId int64, text string) {
	// ChatGPT uses ** for bold text, so we need to replace it
	text = strings.ReplaceAll(text, "**", "*")
	text = strings.ReplaceAll(text, "![", "[")

	_, err := t.api.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:    chatId,
		Text:      sanitize(text),
		ParseMode: models.ParseModeMarkdown,
	})
	if err != nil {
		t.log.With(slog.Int64("id", chatId)).Warn("sending message", sl.Err(err))
		_, err = t.api.SendMessage(ctx, &tg.SendMessageParams{ChatID: chatId, Text: stripMarkdown(text)})
		if err != nil {
			t.log.With(slog.Int64("id", chatId)).Error("sending safe message", sl.Err(err))
		}
	}
}

// detect if we are mentioned in the message
func (t *TgBot) isMentioned(text string) bool {
	if t.botUsername != "" {
		return strings.Contains(text, "@"+t.botUsername)
	}
	return false
}

// detect if message is a reply to a message from the bot
func (t *TgBot) isReplyToBot(message *models.Message) bool {
	reply := message.ReplyToMessage
	return reply != nil && reply.From != nil && reply.From.Username == t.botUsername
}

// stripMarkdown removes inline markdown emphasis markers (* and _) from text.
// Used as a fallback when MarkdownV2 parsing fails, so the user sees clean
// prose instead of raw markup. Code spans (backticks) are preserved.
func stripMarkdown(input string) string {
	r := strings.NewReplacer("**", "", "*", "", "__", "", "_", "")
	return r.Replace(input)
}

func sanitize(input string) string {
	var result strings.Builder
	// Reserved chars for MarkdownV2 (excluding backtick which we handle specially)
	reservedChars := "\\_{}#+-.!|()"
	runes := []rune(input)
	i := 0

	for i < len(runes) {
		// Check for triple backtick code block
		if i+2 < len(runes) && runes[i] == '`' && runes[i+1] == '`' && runes[i+2] == '`' {
			result.WriteString("```")
			i += 3

			// Find the closing ```
			for i < len(runes) {
				if i+2 < len(runes) && runes[i] == '`' && runes[i+1] == '`' && runes[i+2] == '`' {
					result.WriteString("```")
					i += 3
					break
				}
				result.WriteRune(runes[i])
				i++
			}
			continue
		}

		// Check for single backtick inline code
		if runes[i] == '`' {
			result.WriteRune('`')
			i++

			// Find the closing `
			for i < len(runes) && runes[i] != '`' {
				result.WriteRune(runes[i])
				i++
			}
			if i < len(runes) {
				result.WriteRune('`')
				i++
			}
			continue
		}

		// Normal character - escape if reserved
		if strings.ContainsRune(reservedChars, runes[i]) {
			result.WriteRune('\\')
		}
		result.WriteRune(runes[i])
		i++
	}

	return result.String()
}
