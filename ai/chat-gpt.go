package ai

import (
	"Brainy/core"
	"Brainy/holder"
	"Brainy/lib/sl"
	"Brainy/storage"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

type ChatGPT struct {
	conf           *core.Config
	log            *slog.Logger
	contextManager *holder.ContextManager
	client         openai.Client
	prefsAnalyzer  *PreferencesAnalyzer
}

func NewChat(conf *core.Config, log *slog.Logger, store storage.ContextStorage) *ChatGPT {
	return &ChatGPT{
		conf:           conf,
		log:            log.With(sl.Module("chat-gpt")),
		contextManager: holder.NewContextManager(store),
		client:         openai.NewClient(option.WithAPIKey(conf.OpenAIApiKey)),
	}
}

func (c *ChatGPT) Close() error {
	return c.contextManager.Close()
}

func (c *ChatGPT) ClearContext(userId int64) {
	c.contextManager.ClearUserContext(userId)
}

// SetPreferencesAnalyzer sets the preferences analyzer for prompt injection
func (c *ChatGPT) SetPreferencesAnalyzer(pa *PreferencesAnalyzer) {
	c.prefsAnalyzer = pa
}

// GenerateImage generates an image using the configured image model.
// Returns a URL (dall-e-2 / dall-e-3 only). gpt-image-* models are b64-only
// and not supported by this entry point yet.
func (c *ChatGPT) GenerateImage(userId int64, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	styled := prompt + c.conf.ImageStyle

	resp, err := c.client.Images.Generate(ctx, openai.ImageGenerateParams{
		Prompt:         styled,
		Model:          openai.ImageModel(c.conf.ImageModel),
		Size:           openai.ImageGenerateParamsSize(c.conf.ImageSize),
		ResponseFormat: openai.ImageGenerateParamsResponseFormatURL,
		N:              openai.Int(1),
	})
	if err != nil {
		c.log.With(slog.Int64("user", userId)).Error("image generation", sl.Err(err))
		return "", fmt.Errorf("image generation: %w", err)
	}
	if len(resp.Data) == 0 || resp.Data[0].URL == "" {
		return "", fmt.Errorf("image generation: empty response")
	}

	c.log.With(
		slog.Int64("user", userId),
		slog.String("prompt", prompt),
	).Info("image generated")

	return resp.Data[0].URL, nil
}

// DetectImageIntent uses GPT to detect if the user wants to generate an image.
func (c *ChatGPT) DetectImageIntent(question string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := `Analyze the following user message and determine if they want to generate/create an image.
Respond in JSON format only: {"wants_image": true/false, "image_prompt": "optimized prompt for DALL-E if wants_image is true, otherwise empty string"}

Rules for detection:
- "wants_image" should be true if user explicitly asks to create, generate, draw, paint, make, design, or visualize an image/picture/photo/illustration
- "wants_image" should be true if user asks for visual content like "show me", "I want to see", "create a picture of"
- "wants_image" should be false for questions about images, image editing, or general conversation
- If wants_image is true, create an optimized detailed prompt for DALL-E image generation based on user's request

User message: ` + question

	completion, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.conf.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil || len(completion.Choices) == 0 {
		return false, ""
	}

	type intentResponse struct {
		WantsImage  bool   `json:"wants_image"`
		ImagePrompt string `json:"image_prompt"`
	}
	var ir intentResponse
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &ir); err != nil {
		c.log.With(slog.String("response", completion.Choices[0].Message.Content)).Debug("failed to parse intent response")
		return false, ""
	}
	return ir.WantsImage, ir.ImagePrompt
}

func (c *ChatGPT) GetResponse(userId int64, question string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, ok := c.composeMessages(userId, question)
	if !ok {
		// Slash command was fully handled locally (e.g. /clear, /topic).
		return c.localResponse(userId, question), nil
	}

	completion, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.conf.Model),
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("chat completion: empty choices")
	}

	response := completion.Choices[0].Message.Content

	c.contextManager.UpdateUserContext(userId, holder.Message{Text: response, IsUser: false})

	logText := response
	if len(logText) > 50 {
		logText = logText[:50] + "..."
	}
	c.log.With(
		slog.Int64("user", userId),
		slog.Int64("prompt_tokens", completion.Usage.PromptTokens),
		slog.Int64("completion_tokens", completion.Usage.CompletionTokens),
		slog.String("text", logText),
	).Info("outgoing message")

	return response, nil
}

// composeMessages builds the message array sent to the model.
// Returns ok=false when the command was handled locally and no API call is needed.
func (c *ChatGPT) composeMessages(userId int64, question string) ([]openai.ChatCompletionMessageParamUnion, bool) {
	// Single-shot commands (no history, no context update).
	if shot, isShot := c.singleShotCommand(question); isShot {
		return []openai.ChatCompletionMessageParamUnion{openai.UserMessage(shot)}, true
	}

	// Locally-handled commands.
	if strings.HasPrefix(question, "/clear") || strings.HasPrefix(question, "/topic") {
		return nil, false
	}

	// Track user message time for preferences analysis.
	if c.prefsAnalyzer != nil {
		c.prefsAnalyzer.UpdateLastMessageTime(userId)
	}

	c.contextManager.UpdateUserContext(userId, holder.Message{Text: question, IsUser: true})

	var msgs []openai.ChatCompletionMessageParamUnion

	if sys := c.systemPrompt(userId); sys != "" {
		msgs = append(msgs, openai.SystemMessage(sys))
	}

	dialog := c.contextManager.GetUserContext(userId)
	if dialog != nil {
		c.log.With(
			slog.Int64("user", userId),
			slog.Int("tokens", dialog.Tokens),
		).Info("user context")
		for _, m := range dialog.Messages {
			if m.IsUser {
				msgs = append(msgs, openai.UserMessage(m.Text))
			} else {
				msgs = append(msgs, openai.AssistantMessage(m.Text))
			}
		}
	} else {
		msgs = append(msgs, openai.UserMessage(question))
	}

	return msgs, true
}

func (c *ChatGPT) singleShotCommand(question string) (string, bool) {
	switch {
	case strings.HasPrefix(question, "/ask "):
		return strings.TrimPrefix(question, "/ask "), true
	case strings.HasPrefix(question, "/cat "):
		return LanguageTranslatePrompt("Catalan") + strings.TrimPrefix(question, "/cat "), true
	case strings.HasPrefix(question, "/cas "):
		return LanguageTranslatePrompt("Spanish") + strings.TrimPrefix(question, "/cas "), true
	case strings.HasPrefix(question, "/hello"):
		return "Answer in Ukrainian: Say one random fact from science.", true
	}
	return "", false
}

func (c *ChatGPT) localResponse(userId int64, question string) string {
	if strings.HasPrefix(question, "/clear") {
		if c.prefsAnalyzer != nil {
			c.prefsAnalyzer.TriggerAnalysisAsync(userId)
		}
		c.contextManager.ClearUserContext(userId)
		return "Let's talk."
	}
	if strings.HasPrefix(question, "/topic") {
		topic := strings.TrimPrefix(question, "/topic ")
		c.contextManager.SetTopic(userId, topic)
		return "Let's talk about " + topic + "."
	}
	return ""
}

func (c *ChatGPT) systemPrompt(userId int64) string {
	var parts []string

	if c.prefsAnalyzer != nil {
		if prefs := c.prefsAnalyzer.GetUserPreferences(userId); prefs != nil {
			parts = append(parts, c.buildPreferencesPrompt(prefs))
		}
	}

	dialog := c.contextManager.GetUserContext(userId)
	if dialog != nil && dialog.Topic != "" {
		parts = append(parts, "Current subject: "+dialog.Topic)
	}

	return strings.Join(parts, "\n\n")
}

func LanguageTranslatePrompt(language string) string {
	p := "Act as a " + language + "-English dictionary. Give response like an Dictionary article. Add the following information: "
	p += "[ transcription ] "
	p += "- gender, empty if not applicable "
	p += "- grammar form, empty if not applicable "
	p += "- translation "
	p += "- examples of use "
	p += "- for verbs add: conjugation in present, past and future. "
	p += "Here is the word to translate: "
	return p
}

func (c *ChatGPT) buildPreferencesPrompt(prefs *storage.UserPreferences) string {
	var parts []string
	parts = append(parts, "User preferences (adapt your responses accordingly):")
	if prefs.PreferredLanguage != "" {
		parts = append(parts, fmt.Sprintf("- Preferred language: %s", prefs.PreferredLanguage))
	}
	if prefs.Formality != "" {
		parts = append(parts, fmt.Sprintf("- Communication style: %s", prefs.Formality))
	}
	if prefs.Verbosity != "" {
		parts = append(parts, fmt.Sprintf("- Detail level: %s", prefs.Verbosity))
	}
	if prefs.TechnicalLevel != "" {
		parts = append(parts, fmt.Sprintf("- Technical level: %s", prefs.TechnicalLevel))
	}
	if prefs.HumorPreference != "" && prefs.HumorPreference != "none" {
		parts = append(parts, fmt.Sprintf("- Humor: %s", prefs.HumorPreference))
	}
	if prefs.ResponseLength != "" {
		parts = append(parts, fmt.Sprintf("- Response length: %s", prefs.ResponseLength))
	}
	if len(prefs.FavoriteTopics) > 0 {
		parts = append(parts, fmt.Sprintf("- Interests: %s", strings.Join(prefs.FavoriteTopics, ", ")))
	}
	return strings.Join(parts, "\n")
}
