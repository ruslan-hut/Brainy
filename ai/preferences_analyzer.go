package ai

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"Brainy/storage"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const (
	analysisInterval       = 24 * time.Hour
	backgroundCheckFreq    = 1 * time.Hour
	minMessagesForAnalysis = 3
)

// PreferencesAnalyzer handles user preferences analysis via AI
type PreferencesAnalyzer struct {
	conf             *core.Config
	log              *slog.Logger
	contextStorage   storage.ContextStorage
	prefsStorage     storage.PreferencesStorage
	client           openai.Client
	stopChan         chan struct{}
	wg               sync.WaitGroup
	analysisInFlight sync.Map // map[int64]bool
}

func NewPreferencesAnalyzer(
	conf *core.Config,
	log *slog.Logger,
	contextStorage storage.ContextStorage,
	prefsStorage storage.PreferencesStorage,
) *PreferencesAnalyzer {
	return &PreferencesAnalyzer{
		conf:           conf,
		log:            log.With(sl.Module("prefs-analyzer")),
		contextStorage: contextStorage,
		prefsStorage:   prefsStorage,
		client:         openai.NewClient(option.WithAPIKey(conf.OpenAIApiKey)),
		stopChan:       make(chan struct{}),
	}
}

func (pa *PreferencesAnalyzer) StartBackgroundAnalysis() {
	pa.wg.Add(1)
	go func() {
		defer pa.wg.Done()
		ticker := time.NewTicker(backgroundCheckFreq)
		defer ticker.Stop()

		pa.log.Info("background analysis started", slog.Duration("interval", backgroundCheckFreq))

		for {
			select {
			case <-ticker.C:
				pa.runBackgroundAnalysis()
			case <-pa.stopChan:
				pa.log.Info("background analysis stopped")
				return
			}
		}
	}()
}

func (pa *PreferencesAnalyzer) Stop() {
	close(pa.stopChan)
	pa.wg.Wait()
}

func (pa *PreferencesAnalyzer) runBackgroundAnalysis() {
	users, err := pa.prefsStorage.GetUsersNeedingAnalysis(analysisInterval)
	if err != nil {
		pa.log.Error("getting users for analysis", sl.Err(err))
		return
	}
	if len(users) > 0 {
		pa.log.Info("users needing analysis", slog.Int("count", len(users)))
	}
	for _, userId := range users {
		pa.TriggerAnalysisAsync(userId)
	}
}

func (pa *PreferencesAnalyzer) TriggerAnalysisAsync(userId int64) {
	if _, loaded := pa.analysisInFlight.LoadOrStore(userId, true); loaded {
		return
	}
	pa.wg.Add(1)
	go func() {
		defer pa.wg.Done()
		defer pa.analysisInFlight.Delete(userId)
		if err := pa.AnalyzeUser(userId); err != nil {
			pa.log.With(slog.Int64("user", userId)).Error("analyzing user preferences", sl.Err(err))
		}
	}()
}

func (pa *PreferencesAnalyzer) AnalyzeUser(userId int64) error {
	if existing, _ := pa.prefsStorage.GetUserPreferences(userId); existing != nil && existing.ManuallySet {
		return nil
	}
	dialogCtx, err := pa.contextStorage.GetUserContext(userId)
	if err != nil {
		return fmt.Errorf("getting user context: %w", err)
	}
	if dialogCtx == nil || len(dialogCtx.Messages) < minMessagesForAnalysis {
		return nil
	}

	var userMessages []string
	for _, msg := range dialogCtx.Messages {
		if msg.IsUser {
			userMessages = append(userMessages, msg.Text)
		}
	}
	if len(userMessages) < minMessagesForAnalysis {
		return nil
	}

	pa.log.With(slog.Int64("user", userId)).Info("starting preferences analysis",
		slog.Int("messages", len(userMessages)))

	analysis, err := pa.callOpenAI(userMessages)
	if err != nil {
		return fmt.Errorf("calling OpenAI: %w", err)
	}

	prefs := pa.buildUserPreferences(userId, analysis)
	if err := pa.prefsStorage.SaveUserPreferences(prefs); err != nil {
		return fmt.Errorf("saving preferences: %w", err)
	}

	pa.log.With(slog.Int64("user", userId)).Info("preferences analysis completed",
		slog.String("language", prefs.PreferredLanguage),
		slog.String("formality", prefs.Formality))

	return nil
}

var preferencesSchema = generateSchema[storage.PreferencesAnalysis]()

func generateSchema[T any]() map[string]any {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T
	schema := reflector.Reflect(v)
	data, _ := json.Marshal(schema)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func (pa *PreferencesAnalyzer) callOpenAI(userMessages []string) (*storage.PreferencesAnalysis, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Analyze the following user messages and infer their communication preferences.

User Messages:
%s

Fields to populate:
- preferred_language: language the user writes in most (e.g., English, Ukrainian, Spanish)
- formality: formal, informal, or neutral
- verbosity: verbose, concise, or balanced
- favorite_topics: topics they discuss frequently
- technical_level: beginner, intermediate, or expert
- humor_preference: none, occasional, or frequent
- response_length: short, medium, or long`, strings.Join(userMessages, "\n---\n"))

	completion, err := pa.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(pa.conf.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "user_preferences",
					Schema: preferencesSchema,
					Strict: openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}

	var analysis storage.PreferencesAnalysis
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &analysis); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w (response: %s)", err, completion.Choices[0].Message.Content)
	}
	return &analysis, nil
}

func (pa *PreferencesAnalyzer) buildUserPreferences(userId int64, analysis *storage.PreferencesAnalysis) *storage.UserPreferences {
	existing, _ := pa.prefsStorage.GetUserPreferences(userId)

	prefs := &storage.UserPreferences{
		UserId:            userId,
		PreferredLanguage: analysis.PreferredLanguage,
		Formality:         analysis.Formality,
		Verbosity:         analysis.Verbosity,
		FavoriteTopics:    analysis.FavoriteTopics,
		TechnicalLevel:    analysis.TechnicalLevel,
		HumorPreference:   analysis.HumorPreference,
		ResponseLength:    analysis.ResponseLength,
		LastAnalysisAt:    time.Now(),
	}
	if existing != nil {
		prefs.CreatedAt = existing.CreatedAt
		prefs.LastMessageAt = existing.LastMessageAt
	} else {
		prefs.CreatedAt = time.Now()
	}
	return prefs
}

// GetUserPreferences returns preferences for prompt injection
func (pa *PreferencesAnalyzer) GetUserPreferences(userId int64) *storage.UserPreferences {
	prefs, err := pa.prefsStorage.GetUserPreferences(userId)
	if err != nil {
		pa.log.Error("getting user preferences", sl.Err(err))
		return nil
	}
	return prefs
}

// UpdateLastMessageTime should be called when user sends a message
func (pa *PreferencesAnalyzer) UpdateLastMessageTime(userId int64) {
	if err := pa.prefsStorage.UpdateLastMessageTime(userId); err != nil {
		pa.log.Error("updating last message time", sl.Err(err))
	}
}
