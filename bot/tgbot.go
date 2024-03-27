package bot

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"log/slog"
	"strings"
	"time"
)

const errorResponse = "Sorry, I'm not feeling well today. Please try again later."

type TgBot struct {
	conf        *core.Config
	log         *slog.Logger
	api         *tgbotapi.BotAPI
	chat        core.ChatService
	botUsername string
}

func NewTgBot(conf *core.Config, log *slog.Logger) (*TgBot, error) {
	tgBot := &TgBot{
		conf:        conf,
		log:         log.With(sl.Module("tgbot")),
		botUsername: conf.Username,
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
	for update := range updates {
		if update.Message == nil {
			continue
		}

		incoming := update.Message
		chat := incoming.Chat
		question := incoming.Text

		if !incoming.IsCommand() && !chat.IsPrivate() && !t.isMentioned(incoming.Text) && !t.isReplyToBot(incoming) {
			continue
		}
		if incoming.IsCommand() {
			if incoming.Command() == "help" {
				text := "You can use the following commands:\n"
				text += "/help - show this help\n"
				text += "/hello - bot says random fact\n"
				text += "/topic - set a subject of conversation\n"
				text += "/ask - ask something or just reply on previous bot message\n"
				text += "/clear - clear bot memory to begin new topic\n"
				t.plainResponse(chat.ID, text)
				continue
			}
			if incoming.Command() == "ask" {
				question = strings.TrimPrefix(question, "/ask")
			}
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

	}

	return nil
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

func (t *TgBot) plainResponse(chatId int64, text string) {
	// Send the response back to the user
	msg := tgbotapi.NewMessage(chatId, text)
	_, err := t.api.Send(msg)
	if err != nil {
		t.log.With(
			slog.Int64("id", chatId),
		).Error("sending message", sl.Err(err))
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
