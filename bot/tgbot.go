package bot

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

const streamEditInterval = 900 * time.Millisecond

var smileEmojis = []string{
	"😊", "😄", "😁", "🙂", "😉", "🤗", "😇", "🥰", "😎", "🤔",
	"👀", "🙈", "🤷", "👍", "✨", "🎉", "💫", "🌟", "🔥", "💯",
}

const errorResponse = "Sorry, I'm not feeling well today. Please try again later."

type TgBot struct {
	conf        *core.Config
	log         *slog.Logger
	api         *tgbotapi.BotAPI
	chat        core.ChatService
	botUsername string
	stopChan    chan struct{}
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

func (t *TgBot) Start() error {
	// Set up an update configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Start listening for updates
	updates, err := t.api.GetUpdatesChan(u)
	if err != nil {
		return fmt.Errorf("getting updates channel: %v", err)
	}

	// Define a command handler
	for {
		select {
		case update := <-updates:
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
					t.plainResponse(chat.ID, text)
					continue
				case "ask":
					stripped := strings.TrimSpace(strings.TrimPrefix(question, "/ask"))
					if stripped == "" {
						t.plainResponse(chat.ID, "Please provide a question. Example: /ask what is the capital of France?")
						continue
					}
					go t.sendOneShot(chat.ID, stripped)
					continue
				case "cat":
					word := strings.TrimSpace(strings.TrimPrefix(question, "/cat"))
					if word == "" {
						t.plainResponse(chat.ID, "Please provide a word. Example: /cat poma")
						continue
					}
					go t.sendTranslate(chat.ID, "Catalan", word)
					continue
				case "cas":
					word := strings.TrimSpace(strings.TrimPrefix(question, "/cas"))
					if word == "" {
						t.plainResponse(chat.ID, "Please provide a word. Example: /cas manzana")
						continue
					}
					go t.sendTranslate(chat.ID, "Spanish", word)
					continue
				case "hello":
					go t.sendOneShot(chat.ID, "Answer in Ukrainian: Say one random fact from science.")
					continue
				case "topic":
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
				case "clear":
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
	_, err := t.api.Send(msg)
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

func (t *TgBot) sendTranslate(chatId int64, language, word string) {
	t.withTyping(chatId, func() {
		text, err := t.chat.Translate(language, word)
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
	msg := tgbotapi.NewPhotoUpload(chatId, tgbotapi.FileBytes{Name: "image.png", Bytes: data})
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
		safeMsg := tgbotapi.NewMessage(chatId, text)
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
