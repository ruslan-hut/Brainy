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
				if incoming.Command() == "help" {
					text := "You can use the following commands:\n"
					text += "/help - show this help\n"
					text += "/hello - bot says random fact\n"
					text += "/topic - set a subject of conversation\n"
					text += "/ask - ask something or just reply on previous bot message\n"
					text += "/imagine - generate an image from description\n"
					text += "/clear - clear bot memory to begin new topic\n"
					t.plainResponse(chat.ID, text)
					continue
				}
				if incoming.Command() == "ask" {
					question = strings.TrimPrefix(question, "/ask")
				}
				if incoming.Command() == "imagine" {
					imagePrompt := strings.TrimSpace(strings.TrimPrefix(question, "/imagine"))
					if imagePrompt == "" {
						t.plainResponse(chat.ID, "Please provide a description for the image. Example: /imagine a sunset over mountains")
						continue
					}
					go t.SendImageResponse(chat.ID, imagePrompt)
					continue
				}
				if incoming.Command() == "clear" {
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

func (t *TgBot) composeReply(chatId int64, request string) string {
	// Get the response from the chat service
	response, err := t.chat.GetResponse(chatId, request)
	if err != nil {
		t.log.With(
			slog.Int64("id", chatId),
		).Error("composing reply", sl.Err(err))
		response = errorResponse
	}
	return response
}

func (t *TgBot) SendResponse(chatId int64, request string) {
	// First, detect if user wants to generate an image
	wantsImage, imagePrompt := t.chat.DetectImageIntent(request)
	if wantsImage && imagePrompt != "" {
		t.log.With(
			slog.Int64("id", chatId),
			slog.String("prompt", imagePrompt),
		).Info("detected image generation intent")
		t.SendImageResponse(chatId, imagePrompt)
		return
	}

	stopTicker := make(chan bool)
	replyReady := make(chan string)

	t.sendChatAction(chatId, "typing")

	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.sendChatAction(chatId, "typing")
			case <-stopTicker:
				return
			}
		}
	}()

	go func() {
		reply := t.composeReply(chatId, request)
		replyReady <- reply
	}()

	reply := <-replyReady
	stopTicker <- true

	t.plainResponse(chatId, reply)
}

// SendImageResponse generates and sends an image
func (t *TgBot) SendImageResponse(chatId int64, prompt string) {
	stopTicker := make(chan bool)
	imageReady := make(chan string)
	errorChan := make(chan error)

	t.sendChatAction(chatId, "upload_photo")

	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.sendChatAction(chatId, "upload_photo")
			case <-stopTicker:
				return
			}
		}
	}()

	go func() {
		imageURL, err := t.chat.GenerateImage(chatId, prompt)
		if err != nil {
			errorChan <- err
			return
		}
		imageReady <- imageURL
	}()

	select {
	case imageURL := <-imageReady:
		stopTicker <- true
		t.sendImage(chatId, imageURL, prompt)
	case err := <-errorChan:
		stopTicker <- true
		t.log.With(
			slog.Int64("id", chatId),
		).Error("generating image", sl.Err(err))
		t.plainResponse(chatId, "Sorry, I couldn't generate the image. Please try again with a different description.")
	}
}

func (t *TgBot) sendImage(chatId int64, imageURL string, caption string) {
	// Truncate caption if too long (Telegram limit is 1024)
	if len(caption) > 200 {
		caption = caption[:197] + "..."
	}

	msg := tgbotapi.NewPhotoShare(chatId, imageURL)
	msg.Caption = caption
	_, err := t.api.Send(msg)
	if err != nil {
		t.log.With(
			slog.Int64("id", chatId),
			slog.String("url", imageURL),
		).Error("sending image", sl.Err(err))
		// Fallback: send the URL as text
		t.plainResponse(chatId, "Generated image: "+imageURL)
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
