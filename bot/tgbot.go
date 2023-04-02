package bot

import (
	"Brainy/core"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"log"
	"strconv"
)

const MaxMessageLength = 1000
const errorResponse = "Sorry, I'm not feeling well today. Please try again later."

type TgBot struct {
	conf *core.Config
	api  *tgbotapi.BotAPI
	chat core.ChatService
}

func NewTgBot(conf *core.Config) (*TgBot, error) {
	tgBot := &TgBot{conf: conf}
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

		if !update.Message.IsCommand() {
			continue
		}

		// Reject the message if it exceeds the maximum length
		if len(update.Message.Text) > MaxMessageLength {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Your message is too long. Please keep it under "+strconv.Itoa(MaxMessageLength)+" characters.")
			_, err := t.api.Send(msg)
			if err != nil {
				log.Printf("error sending message: %v", err)
			}
			log.Printf("[%s] Input rejected", update.Message.From.UserName)
			continue
		}
		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		// Get the response from the chat service
		response, err := t.chat.GetResponse(update.Message.Text)
		if err != nil {
			log.Printf("error getting response: %v", err)
			response = errorResponse
		}

		// Send the response back to the user
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, response)
		_, err = t.api.Send(msg)
		if err != nil {
			log.Printf("error sending message: %v", err)
		}
	}
}
