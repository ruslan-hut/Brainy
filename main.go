package main

import (
	"AslamistBot/core"
	"encoding/json"
	"fmt"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"io"
	"log"
	"net/http"
	"strings"
)

type ChatCompletion struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
		Index        int    `json:"index"`
	} `json:"choices"`
}

func main() {

	conf, err := core.GetConfig()
	if err != nil {
		log.Fatal(err)
	}

	bot, err := tgbotapi.NewBotAPI(conf.TelegramApiKey)
	if err != nil {
		log.Fatal(err)
	}

	// Set up an update configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Start listening for updates
	updates, err := bot.GetUpdatesChan(u)

	// Define a command handler
	for update := range updates {
		if update.Message == nil {
			continue
		}

		if strings.HasPrefix(update.Message.Text, "/ask ") {
			// Send the text after the "/ask " command to the ChatGPT API
			question := strings.TrimPrefix(update.Message.Text, "/ask ")
			response := getChatGPTResponse(question, conf.OpenAIApiKey)

			// Send the response back to the user
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, response)
			_, err := bot.Send(msg)
			if err != nil {
				log.Printf("error sending message: %v", err)
			}
		}
		if strings.HasPrefix(update.Message.Text, "/cat ") {
			//p := "Act as a Catalan-English dictionary. Give response in a following pattern: "
			//p = p + "[ transcription ] "
			//p = p + "- gender, empty if not applicable "
			//p = p + "- grammar form, empty if not applicable "
			//p = p + "- translation "
			//p = p + "- examples of use "
			//p = p + "- for verbs add: conjugation in present, past and future "
			//p = p + ". Here is the word to translate: "

			p := "Act as a Catalan-English dictionary. Give response like an Oxford Dictionary article. Add the following information: "
			p = p + "[ transcription ] "
			p = p + "- gender, empty if not applicable "
			p = p + "- grammar form, empty if not applicable "
			p = p + "- translation "
			p = p + "- examples of use "
			p = p + "- for verbs add: conjugation in present, past and future. "
			p = p + "Here is the word to translate: "

			question := strings.TrimPrefix(update.Message.Text, "/cat ")
			question = p + question
			response := getChatGPTResponse(question, conf.OpenAIApiKey)

			msg := tgbotapi.NewMessage(update.Message.Chat.ID, response)
			_, err := bot.Send(msg)
			if err != nil {
				log.Printf("error sending message: %v", err)
			}
		}
		if strings.HasPrefix(update.Message.Text, "/cas ") {
			p := "Act as a Catalan-English dictionary. Give response like an Oxford Dictionary article. Add the following information: "
			p = p + "[ transcription ] "
			p = p + "- gender, empty if not applicable "
			p = p + "- grammar form, empty if not applicable "
			p = p + "- translation "
			p = p + "- examples of use "
			p = p + "- for verbs add: conjugation in present, past and future. "
			p = p + "Here is the word to translate: "

			question := strings.TrimPrefix(update.Message.Text, "/cas ")
			question = p + question
			response := getChatGPTResponse(question, conf.OpenAIApiKey)

			msg := tgbotapi.NewMessage(update.Message.Chat.ID, response)
			_, err := bot.Send(msg)
			if err != nil {
				log.Printf("error sending message: %v", err)
			}
		}
	}
}

func getChatGPTResponse(question, key string) string {
	client := &http.Client{}

	//requestBody := strings.NewReader(`{"prompt":"` + question + `","max_tokens":50,"n":1,"stop":null}`)
	requestBody := strings.NewReader(`{"model": "gpt-3.5-turbo","messages": [{"role": "user", "content": "` + question + `"}],"temperature": 0.7}`)

	// Create a new request with the ChatGPT API URL
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", requestBody)
	if err != nil {
		return fmt.Sprintf("error making request: %v", err)
	}

	// Add the Authorization header with your ChatGPT API key
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	req.Header.Set("Content-Type", "application/json")

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("error getting response: %v", err)
	}

	// Read the response body
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Println("error closing body: ", err)
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("error reading response body: %v", err)
	}

	// Parse the response JSON to get the generated text
	// Here you'll need to adjust the code to parse the JSON response from ChatGPT and extract the generated text
	var chatCompletion ChatCompletion
	err = json.Unmarshal(body, &chatCompletion)
	if err != nil {
		return fmt.Sprintf("error decoding response: %v", err)
	}
	if len(chatCompletion.Choices) == 0 {
		return "error: no response on prompt: " + question
	}
	response := chatCompletion.Choices[0].Message.Content

	return response
}
