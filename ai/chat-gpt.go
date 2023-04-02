package ai

import (
	"Brainy/core"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type ChatGPT struct {
	conf *core.Config
}

func NewChat(conf *core.Config) *ChatGPT {
	return &ChatGPT{conf: conf}
}

func (c *ChatGPT) GetResponse(question string) (string, error) {
	client := &http.Client{}

	prompt := c.composePrompt(question)

	request := NewRequest(prompt)
	jsonBytes, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("error marshalling request: %v", err)
	}
	requestBody := strings.NewReader(string(jsonBytes))

	// Create a new request with the ChatGPT API URL
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", requestBody)
	if err != nil {
		return "", fmt.Errorf("error making request: %v", err)
	}

	// Add the Authorization header with your ChatGPT API key
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.conf.OpenAIApiKey))
	req.Header.Set("Content-Type", "application/json")

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error getting response: %v", err)
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
		return "", fmt.Errorf("error reading response body: %v", err)
	}

	// Parse the response JSON to get the generated text
	// Here you'll need to adjust the code to parse the JSON response from ChatGPT and extract the generated text
	var chatCompletion ChatCompletion
	err = json.Unmarshal(body, &chatCompletion)
	if err != nil {
		return "", fmt.Errorf("error decoding response: %v", err)
	}
	if len(chatCompletion.Choices) == 0 {
		return "", fmt.Errorf("error: no response on prompt: %s", question)
	}
	response := chatCompletion.Choices[0].Message.Content

	return response, nil
}

// compose prompt for openai
func (c *ChatGPT) composePrompt(question string) string {

	if strings.HasPrefix(question, "/ask ") {
		// Send the text after the "/ask " command to the ChatGPT API
		return strings.TrimPrefix(question, "/ask ")
	}

	if strings.HasPrefix(question, "/cat ") {
		word := strings.TrimPrefix(question, "/cat ")
		p := LanguageTranslatePrompt("Catalan")
		return p + word
	}

	if strings.HasPrefix(question, "/cas ") {
		word := strings.TrimPrefix(question, "/cas ")
		p := LanguageTranslatePrompt("Spanish")
		return p + word
	}

	return "Say random phrase about misunderstandings, miscommunication"
}

func LanguageTranslatePrompt(language string) string {
	p := "Act as a " + language + "-English dictionary. Give response like an Dictionary article. Add the following information: "
	p = p + "[ transcription ] "
	p = p + "- gender, empty if not applicable "
	p = p + "- grammar form, empty if not applicable "
	p = p + "- translation "
	p = p + "- examples of use "
	p = p + "- for verbs add: conjugation in present, past and future. "
	p = p + "Here is the word to translate: "
	return p
}
