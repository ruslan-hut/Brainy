package bot

import (
	"Brainy/core"
	"Brainy/holder"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"log"
	"strings"
)

const MaxMessageLength = 1000
const errorResponse = "Sorry, I'm not feeling well today. Please try again later."

type TgBot struct {
	conf           *core.Config
	api            *tgbotapi.BotAPI
	chat           core.ChatService
	botUsername    string
	contextManager *holder.ContextManager
}

func NewTgBot(conf *core.Config) (*TgBot, error) {
	tgBot := &TgBot{
		conf:           conf,
		contextManager: holder.NewContextManager(),
		botUsername:    conf.Username,
	}

	api, err := tgbotapi.NewBotAPI(conf.TelegramApiKey)
	if err != nil {
		return nil, err
	}
	tgBot.api = api

	return tgBot, nil
}

// SetChat set chat service
func (t *TgBot) SetChat(chat core.ChatService) {
	t.chat = chat
}

func (t *TgBot) Start() {
	// Set up an update configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Start listening for updates
	updates, err := t.api.GetUpdatesChan(u)
	if err != nil {
		log.Fatal(err)
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

		// Reject the message if it exceeds the maximum length
		//if len(incoming.Text) > MaxMessageLength {
		//	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Your message is too long. Please keep it under "+strconv.Itoa(MaxMessageLength)+" characters.")
		//	_, err := t.api.Send(msg)
		//	if err != nil {
		//		log.Printf("error sending message: %v", err)
		//	}
		//	log.Printf("[%s] Input rejected", update.Message.From.UserName)
		//	continue
		//}
		logText := question
		if len(logText) > 50 {
			logText = logText[:50] + "..."
		}
		log.Printf("[%s] %s", incoming.From.UserName, logText)

		go t.SendResponse(chat.ID, question)

	}
}

func (t *TgBot) SendResponse(chatId int64, request string) {
	// Get the response from the chat service
	response, err := t.chat.GetResponse(chatId, request)
	if err != nil {
		log.Printf("error getting response: %v", err)
		response = errorResponse
	}

	// Send the response back to the user
	msg := tgbotapi.NewMessage(chatId, response)
	_, err = t.api.Send(msg)
	if err != nil {
		log.Printf("error sending message: %v", err)
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
