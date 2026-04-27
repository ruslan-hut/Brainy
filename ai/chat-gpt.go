package ai

import (
	"Brainy/core"
	"Brainy/holder"
	"Brainy/lib/sl"
	"Brainy/lib/tokens"
	"Brainy/storage"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const imageToolName = "generate_image"

type ChatGPT struct {
	conf           *core.Config
	log            *slog.Logger
	contextManager *holder.ContextManager
	client         openai.Client
	prefsAnalyzer  *PreferencesAnalyzer
	tools          []openai.ChatCompletionToolUnionParam
}

func NewChat(conf *core.Config, log *slog.Logger, store storage.ContextStorage) *ChatGPT {
	return &ChatGPT{
		conf:           conf,
		log:            log.With(sl.Module("chat-gpt")),
		contextManager: holder.NewContextManager(store),
		client:         openai.NewClient(option.WithAPIKey(conf.OpenAIApiKey)),
		tools:          buildTools(),
	}
}

func buildTools() []openai.ChatCompletionToolUnionParam {
	return []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        imageToolName,
			Description: openai.String("Call this when the user explicitly asks to create, generate, draw, paint, design, or visualize an image, picture, photo, or illustration. Do not call it for questions about images or general conversation."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "Detailed image generation prompt suitable for an image model, derived from the user's request.",
					},
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		}),
	}
}

func (c *ChatGPT) Close() error {
	return c.contextManager.Close()
}

func (c *ChatGPT) ClearContext(userId int64) {
	if c.prefsAnalyzer != nil {
		c.prefsAnalyzer.TriggerAnalysisAsync(userId)
	}
	c.contextManager.ClearUserContext(userId)
}

func (c *ChatGPT) SetTopic(userId int64, topic string) {
	c.contextManager.SetTopic(userId, topic)
}

// SetPreferencesAnalyzer sets the preferences analyzer for prompt injection
func (c *ChatGPT) SetPreferencesAnalyzer(pa *PreferencesAnalyzer) {
	c.prefsAnalyzer = pa
}

// GenerateImage generates an image using the configured image model and returns
// the raw image bytes (PNG). Works with gpt-image-* (always b64) and dall-e-*
// (forced to b64 via response_format).
func (c *ChatGPT) GenerateImage(userId int64, prompt string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	params := openai.ImageGenerateParams{
		Prompt: prompt + c.conf.ImageStyle,
		Model:  openai.ImageModel(c.conf.ImageModel),
		Size:   openai.ImageGenerateParamsSize(c.conf.ImageSize),
		N:      openai.Int(1),
	}
	// gpt-image-* always returns b64 and rejects response_format.
	if !strings.HasPrefix(c.conf.ImageModel, "gpt-image") {
		params.ResponseFormat = openai.ImageGenerateParamsResponseFormatB64JSON
	}

	resp, err := c.client.Images.Generate(ctx, params)
	if err != nil {
		c.log.With(slog.Int64("user", userId)).Error("image generation", sl.Err(err))
		return nil, fmt.Errorf("image generation: %w", err)
	}
	if len(resp.Data) == 0 || resp.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("image generation: empty response")
	}

	data, err := base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}

	c.log.With(
		slog.Int64("user", userId),
		slog.String("prompt", prompt),
		slog.Int("bytes", len(data)),
	).Info("image generated")

	return data, nil
}

// Ask runs a conversational turn with full history and tools.
// On image-intent tool call, returns Response{ImagePrompt:...} without
// touching conversation history.
func (c *ChatGPT) Ask(userId int64, question string) (core.Response, error) {
	return c.askInternal(userId, question, nil)
}

// AskStream is like Ask but reports incremental cumulative content via onDelta
// as the model streams its reply. onDelta is not called when the model decides
// to call a tool instead of producing text.
func (c *ChatGPT) AskStream(userId int64, question string, onDelta func(content string)) (core.Response, error) {
	return c.askInternal(userId, question, onDelta)
}

func (c *ChatGPT) askInternal(userId int64, question string, onDelta func(content string)) (core.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if c.prefsAnalyzer != nil {
		c.prefsAnalyzer.UpdateLastMessageTime(userId)
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.conf.Model),
		Messages: c.buildMessages(userId, question),
		Tools:    c.tools,
	}

	var (
		text          string
		toolCallName  string
		toolCallArgs  string
		promptTokens  int64
		completionTok int64
	)

	if onDelta == nil {
		completion, err := c.client.Chat.Completions.New(ctx, params)
		if err != nil {
			return core.Response{}, fmt.Errorf("chat completion: %w", err)
		}
		if len(completion.Choices) == 0 {
			return core.Response{}, fmt.Errorf("chat completion: empty choices")
		}
		msg := completion.Choices[0].Message
		text = msg.Content
		for _, tc := range msg.ToolCalls {
			if tc.Type == "function" {
				toolCallName = tc.Function.Name
				toolCallArgs = tc.Function.Arguments
				break
			}
		}
		promptTokens = completion.Usage.PromptTokens
		completionTok = completion.Usage.CompletionTokens
	} else {
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}
		stream := c.client.Chat.Completions.NewStreaming(ctx, params)
		acc := openai.ChatCompletionAccumulator{}
		started := time.Now()
		var firstChunk, firstContent time.Time
		var contentChunks int
		for stream.Next() {
			chunk := stream.Current()
			if firstChunk.IsZero() {
				firstChunk = time.Now()
			}
			acc.AddChunk(chunk)
			if content, ok := acc.JustFinishedContent(); ok {
				text = content
			} else if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				if firstContent.IsZero() {
					firstContent = time.Now()
				}
				contentChunks++
				text += chunk.Choices[0].Delta.Content
				onDelta(text)
			}
		}
		c.log.With(
			slog.Int64("user", userId),
			slog.Duration("ttfc", firstChunk.Sub(started)),
			slog.Duration("ttfcontent", firstContent.Sub(started)),
			slog.Int("content_chunks", contentChunks),
		).Debug("stream timing")
		if err := stream.Err(); err != nil {
			return core.Response{}, fmt.Errorf("chat completion stream: %w", err)
		}
		if len(acc.Choices) > 0 {
			for _, tc := range acc.Choices[0].Message.ToolCalls {
				if tc.Type == "function" {
					toolCallName = tc.Function.Name
					toolCallArgs = tc.Function.Arguments
					break
				}
			}
			if text == "" {
				text = acc.Choices[0].Message.Content
			}
		}
		promptTokens = acc.Usage.PromptTokens
		completionTok = acc.Usage.CompletionTokens
	}

	if toolCallName == imageToolName {
		var args struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal([]byte(toolCallArgs), &args); err == nil && args.Prompt != "" {
			c.log.With(
				slog.Int64("user", userId),
				slog.String("prompt", args.Prompt),
			).Info("image intent")
			return core.Response{ImagePrompt: args.Prompt}, nil
		}
	}

	c.contextManager.UpdateUserContext(userId, holder.Message{
		Text:   question,
		IsUser: true,
		Tokens: tokens.Count(question),
	})
	completionTokens := int(completionTok)
	if completionTokens == 0 {
		completionTokens = tokens.Count(text)
	}
	c.contextManager.UpdateUserContext(userId, holder.Message{
		Text:   text,
		IsUser: false,
		Tokens: completionTokens,
	})
	if promptTokens > 0 {
		c.contextManager.SetTokens(userId, int(promptTokens)+completionTokens)
	}

	logText := text
	if len(logText) > 50 {
		logText = logText[:50] + "..."
	}
	c.log.With(
		slog.Int64("user", userId),
		slog.Int64("prompt_tokens", promptTokens),
		slog.Int64("completion_tokens", completionTok),
		slog.String("text", logText),
	).Info("outgoing message")

	return core.Response{Text: text}, nil
}

// OneShot runs a single-message completion with no history, no tools, no preferences.
func (c *ChatGPT) OneShot(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	completion, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.conf.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
	})
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("chat completion: empty choices")
	}
	return completion.Choices[0].Message.Content, nil
}

// Translate returns a dictionary-style translation for a single word.
func (c *ChatGPT) Translate(language, word string) (string, error) {
	return c.OneShot(languageTranslatePrompt(language) + word)
}

func (c *ChatGPT) buildMessages(userId int64, question string) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion

	if sys := c.systemPrompt(userId); sys != "" {
		msgs = append(msgs, openai.SystemMessage(sys))
	}

	if dialog := c.contextManager.GetUserContext(userId); dialog != nil {
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
	}

	msgs = append(msgs, openai.UserMessage(question))
	return msgs
}

func (c *ChatGPT) systemPrompt(userId int64) string {
	var parts []string

	if c.prefsAnalyzer != nil {
		if prefs := c.prefsAnalyzer.GetUserPreferences(userId); prefs != nil {
			parts = append(parts, c.buildPreferencesPrompt(prefs))
		}
	}

	if dialog := c.contextManager.GetUserContext(userId); dialog != nil && dialog.Topic != "" {
		parts = append(parts, "Current subject: "+dialog.Topic)
	}

	return strings.Join(parts, "\n\n")
}

func languageTranslatePrompt(language string) string {
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
