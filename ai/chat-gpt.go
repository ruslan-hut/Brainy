package ai

import (
	"Brainy/core"
	"Brainy/holder"
	"Brainy/lib/sl"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type ChatGPT struct {
	conf           *core.Config
	log            *slog.Logger
	contextManager *holder.ContextManager
}

func NewChat(conf *core.Config, log *slog.Logger) *ChatGPT {
	return &ChatGPT{
		conf:           conf,
		log:            log.With(sl.Module("chat-gpt")),
		contextManager: holder.NewContextManager(),
	}
}

func (c *ChatGPT) GetResponse(userId int64, question string) (string, error) {
	client := &http.Client{}

	prompt := c.composePrompt(userId, question)

	request := NewRequest(prompt)
	jsonBytes, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("error marshalling request: %v", err)
	}
	requestBody := strings.NewReader(string(jsonBytes))

	// Create a new request with the ChatGPT API URL
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", requestBody)
	if err != nil {
		return "", fmt.Errorf("making request: %v", err)
	}

	// Add the Authorization header with your ChatGPT API key
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.conf.OpenAIApiKey))
	req.Header.Set("Content-Type", "application/json")

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting response: %v", err)
	}

	// Read the response body
	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			c.log.Error("closing response body", sl.Err(err))
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %v", err)
	}
	c.log.With(
		slog.Int64("user", userId),
		slog.String("body", string(body)),
	).Debug("response body")

	// Parse the response JSON to get the generated text
	// Here you'll need to adjust the code to parse the JSON response from ChatGPT and extract the generated text
	var chatCompletion ChatCompletion
	err = json.Unmarshal(body, &chatCompletion)
	if err != nil {
		return "", fmt.Errorf("decoding response: %v", err)
	}
	c.log.With(
		slog.Int64("user", userId),
		slog.String("model", chatCompletion.Model),
		slog.Int("choices", len(chatCompletion.Choices)),
	).Info("chat completion")
	if len(chatCompletion.Choices) == 0 {
		return "", fmt.Errorf("chat completion: empty choices")
	}
	response := chatCompletion.Choices[0].Message.Content

	// add bot message to context
	msg := holder.Message{
		Text:   response,
		IsUser: false,
	}
	c.contextManager.UpdateUserContext(userId, msg)

	logText := response
	if len(logText) > 50 {
		logText = logText[:50] + "..."
	}
	c.log.With(
		slog.Int64("user", userId),
		slog.String("text", logText),
	).Info("outgoing message")

	return response, nil
}

// compose prompt for openai
func (c *ChatGPT) composePrompt(userId int64, question string) string {

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

	if strings.HasPrefix(question, "/hello") {
		return "Answer in Ukrainian: Say one random fact from science."
	}

	if strings.HasPrefix(question, "/clear") {
		c.contextManager.ClearUserContext(userId)
		return "Let's talk."
	}

	if strings.HasPrefix(question, "/topic") {
		topic := strings.TrimPrefix(question, "/topic ")
		c.contextManager.SetTopic(userId, topic)
		return "Let's talk about " + topic + "."
	}

	// add user message to context
	msg := holder.Message{
		Text:   question,
		IsUser: true,
	}
	c.contextManager.UpdateUserContext(userId, msg)

	t := c.getContext(userId)
	if t != "" {
		question = t + "\nMy next question is:\n" + question
	}

	return question
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

func (c *ChatGPT) getContext(userId int64) string {
	t := ""
	dialogContext := c.contextManager.GetUserContext(userId)
	if dialogContext != nil {
		c.log.With(
			slog.Int64("user", userId),
			slog.Int("tokens", dialogContext.Tokens),
		).Info("user context")
		if dialogContext.Topic != "" {
			t = "Subject: " + dialogContext.Topic
		}
		t += "\nPrevious messages of you as Assistant and me as User: "
		for _, message := range dialogContext.Messages {
			person := "Assistant"
			if message.IsUser {
				person = "User"
			}
			t += fmt.Sprintf("\n%s: %s", person, message.Text)
		}
	}
	return t
}
