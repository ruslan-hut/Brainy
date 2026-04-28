package bot

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"Brainy/storage"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
	api           *tgbotapi.BotAPI
	chat          core.ChatService
	prefs         storage.PreferencesStorage
	users         storage.UsersStorage
	invites       storage.InvitesStorage
	awaiting      sync.Map // userID -> struct{} : user prompted for invite code, next message is the code
	awaitingTopic sync.Map // userID -> struct{} : /menu topic button pressed, next message is the topic
	wizard        *wizardManager
	botUsername   string
	stopChan      chan struct{}
}

func NewTgBot(conf *core.Config, log *slog.Logger) (*TgBot, error) {
	tgBot := &TgBot{
		conf:        conf,
		log:         log.With(sl.Module("tgbot")),
		botUsername: conf.Username,
		stopChan:    make(chan struct{}),
	}

	api, err := tgbotapi.NewBotAPI(conf.TelegramApiKey)
	if err != nil {
		return nil, fmt.Errorf("creating api instance: %v", err)
	}
	tgBot.api = api

	return tgBot, nil
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
func (t *TgBot) SetAccessControl(users storage.UsersStorage, invites storage.InvitesStorage, adminIds []int64) {
	t.users = users
	t.invites = invites
	t.seedAdmins(adminIds)
}

// requirePrivate refuses management commands in non-private chats and tells
// the user to DM the bot. Returns true if the chat is private.
func (t *TgBot) requirePrivate(chat *tgbotapi.Chat) bool {
	if chat.IsPrivate() {
		return true
	}
	t.plainResponse(chat.ID, "This command is only available in a direct chat with me.")
	return false
}

// adminTarget returns the chat ID to use for an admin command's response.
// If invoked in a non-private chat, deletes the original message and redirects
// to the admin's DM (their user ID == private chat ID in Telegram).
func (t *TgBot) adminTarget(chat *tgbotapi.Chat, msg *tgbotapi.Message) int64 {
	if chat.IsPrivate() {
		return chat.ID
	}
	t.deleteMessage(chat.ID, msg.MessageID)
	return msg.From.ID
}

// userLanguage returns the user's preferred language, or fallback if not set.
func (t *TgBot) userLanguage(userId int64, fallback string) string {
	if t.prefs == nil {
		return fallback
	}
	p, err := t.prefs.GetUserPreferences(userId)
	if err != nil || p == nil || p.PreferredLanguage == "" {
		return fallback
	}
	return p.PreferredLanguage
}

func (t *TgBot) Start() error {
	// Set up an update configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Start listening for updates
	updates := t.api.GetUpdatesChan(u)

	// Define a command handler
	for {
		select {
		case update := <-updates:
			if update.CallbackQuery != nil {
				data := update.CallbackQuery.Data
				switch {
				case t.wizard != nil && strings.HasPrefix(data, callbackPrefix+":"):
					t.wizard.handleCallback(update.CallbackQuery)
				case strings.HasPrefix(data, adminCallbackPrefix+":"):
					t.handleAdminCallback(update.CallbackQuery)
				case strings.HasPrefix(data, menuCallbackPrefix+":"):
					t.handleMenuCallback(update.CallbackQuery)
				}
				continue
			}
			if update.Message == nil {
				continue
			}

			incoming := update.Message
			chat := incoming.Chat
			question := incoming.Text

			if !incoming.IsCommand() && !chat.IsPrivate() && !t.isMentioned(incoming.Text) && !t.isReplyToBot(incoming) {
				continue
			}

			// Check for non-text messages (images, voice, stickers, etc.)
			if question == "" {
				t.log.With(
					slog.String("user", chat.UserName),
					slog.Int64("id", chat.ID),
				).Debug("non-text message received")
				t.sendRandomEmoji(chat.ID)
				continue
			}

			// Access control: invites only gate DMs. In groups, presence in
			// the chat implies access — group membership is the authorization.
			if t.users != nil && chat.IsPrivate() {
				if incoming.IsCommand() && incoming.Command() == "start" {
					arg := strings.TrimSpace(strings.TrimPrefix(question, "/start"))
					t.handleStart(chat.ID, incoming.From.ID, incoming.MessageID, chat.UserName, arg)
					continue
				}
				if !t.isAuthorized(incoming.From.ID) {
					if _, awaiting := t.awaiting.Load(incoming.From.ID); awaiting && !incoming.IsCommand() {
						t.handleInviteSubmission(chat.ID, incoming.From.ID, incoming.MessageID, chat.UserName, question)
						continue
					}
					t.plainResponse(chat.ID, "Access is by invite only. Send /start to begin.")
					continue
				}
			}

			// Capture topic submission triggered from /menu.
			if !incoming.IsCommand() {
				if _, ok := t.awaitingTopic.LoadAndDelete(incoming.From.ID); ok {
					topic := strings.TrimSpace(question)
					if topic == "" {
						t.plainResponse(chat.ID, "Topic was empty, nothing changed.")
						continue
					}
					t.chat.SetTopic(chat.ID, topic)
					t.plainResponse(chat.ID, "Topic set: "+topic)
					continue
				}
			}

			if incoming.IsCommand() {
				switch incoming.Command() {
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
					if t.isAdmin(incoming.From.ID) {
						text += "\nAdmin commands:\n"
						text += "/admin - show admin menu\n"
						text += "/gencode - generate an invite code\n"
						text += "/codes - list invite codes\n"
						text += "/showid - show current chat id\n"
					}
					t.plainResponse(chat.ID, text)
					continue
				case "admin":
					if !t.isAdmin(incoming.From.ID) {
						continue
					}
					target := t.adminTarget(chat, incoming)
					msg := tgbotapi.NewMessage(target, "Admin menu:")
					msg.ReplyMarkup = adminMenuKeyboard()
					if _, err := t.api.Send(msg); err != nil {
						t.log.With(slog.Int64("id", target)).Warn("admin menu", sl.Err(err))
					}
					continue
				case "gencode":
					if !t.isAdmin(incoming.From.ID) {
						continue
					}
					target := t.adminTarget(chat, incoming)
					code, err := t.generateAndSaveCode(incoming.From.ID)
					if err != nil {
						t.plainResponse(target, "Failed to generate code: "+err.Error())
						continue
					}
					t.plainResponse(target, "New invite code: "+code)
					continue
				case "codes":
					if !t.isAdmin(incoming.From.ID) {
						continue
					}
					target := t.adminTarget(chat, incoming)
					t.plainResponse(target, t.formatInviteList())
					continue
				case "showid":
					if !t.isAdmin(incoming.From.ID) {
						continue
					}
					sourceChatID := chat.ID
					target := t.adminTarget(chat, incoming)
					t.plainResponse(target, fmt.Sprintf("Chat ID: %d\nType: %s\nTitle: %s", sourceChatID, chat.Type, chat.Title))
					continue
				case "ask":
					stripped := strings.TrimSpace(strings.TrimPrefix(question, "/ask"))
					if stripped == "" {
						t.plainResponse(chat.ID, "Please provide a question. Example: /ask what is the capital of France?")
						continue
					}
					lang := t.userLanguage(incoming.From.ID, "")
					prompt := stripped
					if lang != "" {
						prompt = fmt.Sprintf("Answer in %s. %s", lang, stripped)
					}
					go t.sendOneShot(chat.ID, prompt)
					continue
				case "cat":
					word := strings.TrimSpace(strings.TrimPrefix(question, "/cat"))
					if word == "" {
						t.plainResponse(chat.ID, "Please provide a word. Example: /cat poma")
						continue
					}
					go t.sendTranslate(chat.ID, "Catalan", word, t.userLanguage(incoming.From.ID, "English"))
					continue
				case "cas":
					word := strings.TrimSpace(strings.TrimPrefix(question, "/cas"))
					if word == "" {
						t.plainResponse(chat.ID, "Please provide a word. Example: /cas manzana")
						continue
					}
					go t.sendTranslate(chat.ID, "Spanish", word, t.userLanguage(incoming.From.ID, "English"))
					continue
				case "hello":
					lang := t.userLanguage(incoming.From.ID, "English")
					go t.sendOneShot(chat.ID, fmt.Sprintf("Answer in %s: Say one random fact from science.", lang))
					continue
				case "topic":
					if !chat.IsPrivate() && !t.isAdmin(incoming.From.ID) {
						continue
					}
					topic := strings.TrimSpace(strings.TrimPrefix(question, "/topic"))
					if topic == "" {
						t.plainResponse(chat.ID, "Please provide a subject. Example: /topic astronomy")
						continue
					}
					t.chat.SetTopic(chat.ID, topic)
					t.plainResponse(chat.ID, "Let's talk about "+topic+".")
					continue
				case "imagine":
					imagePrompt := strings.TrimSpace(strings.TrimPrefix(question, "/imagine"))
					if imagePrompt == "" {
						t.plainResponse(chat.ID, "Please provide a description for the image. Example: /imagine a sunset over mountains")
						continue
					}
					go t.SendImageResponse(chat.ID, imagePrompt)
					continue
				case "menu":
					if !t.requirePrivate(chat) {
						continue
					}
					t.sendMenu(chat.ID)
					continue
				case "tuneup":
					if !chat.IsPrivate() && !t.isAdmin(incoming.From.ID) {
						continue
					}
					if !t.requirePrivate(chat) {
						continue
					}
					if t.wizard == nil {
						t.plainResponse(chat.ID, "Tuning is not available right now.")
						continue
					}
					t.wizard.start(chat.ID, incoming.From.ID)
					continue
				case "clear":
					if !chat.IsPrivate() && !t.isAdmin(incoming.From.ID) {
						continue
					}
					t.log.With(
						slog.String("user", chat.UserName),
						slog.Int64("id", chat.ID),
					).Info("context cleared")
					t.chat.ClearContext(chat.ID)
					t.plainResponse(chat.ID, "context cleared")
					continue
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
				slog.String("user", chat.UserName),
				slog.Int64("id", chat.ID),
				slog.String("text", logText),
			).Info("incoming message")

			go t.SendResponse(chat.ID, question)

		case <-t.stopChan:
			t.log.Info("stopping bot gracefully")
			return nil
		}
	}
}

func (t *TgBot) Stop() {
	close(t.stopChan)
}

func (t *TgBot) sendChatAction(chatId int64, action string) {
	msg := tgbotapi.NewChatAction(chatId, action)
	_, err := t.api.Request(msg)
	if err != nil {
		t.log.With(
			slog.String("action", action),
			slog.Int64("id", chatId),
		).Error("sending chat action", sl.Err(err))
	}
}

func (t *TgBot) sendRandomEmoji(chatId int64) {
	emoji := smileEmojis[rand.Intn(len(smileEmojis))]
	msg := tgbotapi.NewMessage(chatId, emoji)
	_, err := t.api.Send(msg)
	if err != nil {
		t.log.With(
			slog.Int64("id", chatId),
		).Error("sending emoji", sl.Err(err))
	}
}

func (t *TgBot) SendResponse(chatId int64, request string) {
	t.sendChatAction(chatId, "typing")

	editor := newStreamEditor(t, chatId)
	editor.start()

	resp, err := t.chat.AskStream(chatId, request, editor.update)
	editor.stop()

	if err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("composing reply", sl.Err(err))
		editor.deleteIfPosted()
		t.plainResponse(chatId, errorResponse)
		return
	}
	if resp.ImagePrompt != "" {
		editor.deleteIfPosted()
		t.withTyping(chatId, func() {
			t.generateAndSendImage(chatId, resp.ImagePrompt)
		})
		return
	}
	editor.finalize(resp.Text)
}

func (t *TgBot) sendOneShot(chatId int64, prompt string) {
	t.withTyping(chatId, func() {
		text, err := t.chat.OneShot(prompt)
		if err != nil {
			t.log.With(slog.Int64("id", chatId)).Error("one-shot reply", sl.Err(err))
			t.plainResponse(chatId, errorResponse)
			return
		}
		t.plainResponse(chatId, text)
	})
}

func (t *TgBot) sendTranslate(chatId int64, language, word, responseLanguage string) {
	t.withTyping(chatId, func() {
		text, err := t.chat.Translate(language, word, responseLanguage)
		if err != nil {
			t.log.With(slog.Int64("id", chatId)).Error("translate reply", sl.Err(err))
			t.plainResponse(chatId, errorResponse)
			return
		}
		t.plainResponse(chatId, text)
	})
}

// withTyping shows a typing indicator while fn runs.
func (t *TgBot) withTyping(chatId int64, fn func()) {
	stop := make(chan struct{})
	t.sendChatAction(chatId, "typing")
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.sendChatAction(chatId, "typing")
			case <-stop:
				return
			}
		}
	}()
	defer close(stop)
	fn()
}

// SendImageResponse generates and sends an image
func (t *TgBot) SendImageResponse(chatId int64, prompt string) {
	t.withTyping(chatId, func() {
		t.generateAndSendImage(chatId, prompt)
	})
}

func (t *TgBot) generateAndSendImage(chatId int64, prompt string) {
	data, err := t.chat.GenerateImage(chatId, prompt)
	if err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("generating image", sl.Err(err))
		t.plainResponse(chatId, "Sorry, I couldn't generate the image. Please try again with a different description.")
		return
	}
	msg := tgbotapi.NewPhoto(chatId, tgbotapi.FileBytes{Name: "image.png", Bytes: data})
	if _, err := t.api.Send(msg); err != nil {
		t.log.With(slog.Int64("id", chatId)).Error("sending image", sl.Err(err))
		t.plainResponse(chatId, "Sorry, I couldn't send the image.")
	}
}

func (t *TgBot) plainResponse(chatId int64, text string) {

	// ChatGPT uses ** for bold text, so we need to replace it
	text = strings.ReplaceAll(text, "**", "*")
	text = strings.ReplaceAll(text, "![", "[")

	// Send the response back to the user
	sanitized := sanitize(text)

	msg := tgbotapi.NewMessage(chatId, sanitized)
	msg.ParseMode = "MarkdownV2"
	_, err := t.api.Send(msg)
	if err != nil {
		t.log.With(
			slog.Int64("id", chatId),
		).Warn("sending message", sl.Err(err))
		safeMsg := tgbotapi.NewMessage(chatId, stripMarkdown(text))
		_, err = t.api.Send(safeMsg)
		if err != nil {
			t.log.With(
				slog.Int64("id", chatId),
			).Error("sending safe message", sl.Err(err))
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
func (t *TgBot) isReplyToBot(message *tgbotapi.Message) bool {
	if message.ReplyToMessage != nil {
		return message.ReplyToMessage.From.UserName == t.botUsername
	}
	return false
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
