package ai

import (
	"Brainy/core"
	"Brainy/lib/sl"
	"Brainy/storage"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
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
	httpClient       *http.Client
	stopChan         chan struct{}
	wg               sync.WaitGroup
	analysisInFlight sync.Map // map[int64]bool to prevent concurrent analysis for same user
}

// NewPreferencesAnalyzer creates a new preferences analyzer
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
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		stopChan: make(chan struct{}),
	}
}

// StartBackgroundAnalysis starts the background analysis ticker
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

// Stop stops the background analysis and waits for all goroutines to complete
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

// TriggerAnalysisAsync triggers analysis for a user in a goroutine
func (pa *PreferencesAnalyzer) TriggerAnalysisAsync(userId int64) {
	// Prevent concurrent analysis for same user
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

// AnalyzeUser performs the actual AI analysis of user messages
func (pa *PreferencesAnalyzer) AnalyzeUser(userId int64) error {
	// Get user's dialog context
	dialogCtx, err := pa.contextStorage.GetUserContext(userId)
	if err != nil {
		return fmt.Errorf("getting user context: %w", err)
	}
	if dialogCtx == nil || len(dialogCtx.Messages) < minMessagesForAnalysis {
		return nil // Not enough messages to analyze
	}

	// Extract only user messages for analysis
	var userMessages []string
	for _, msg := range dialogCtx.Messages {
		if msg.IsUser {
			userMessages = append(userMessages, msg.Text)
		}
	}

	if len(userMessages) < minMessagesForAnalysis {
		return nil // Not enough user messages
	}

	pa.log.With(slog.Int64("user", userId)).Info("starting preferences analysis",
		slog.Int("messages", len(userMessages)))

	// Build analysis prompt
	analysisPrompt := pa.buildAnalysisPrompt(userMessages)

	// Call OpenAI for analysis
	analysis, err := pa.callOpenAI(analysisPrompt)
	if err != nil {
		return fmt.Errorf("calling OpenAI: %w", err)
	}

	// Parse and save preferences
	prefs, err := pa.parseAnalysisResponse(userId, analysis)
	if err != nil {
		return fmt.Errorf("parsing analysis: %w", err)
	}

	if err := pa.prefsStorage.SaveUserPreferences(prefs); err != nil {
		return fmt.Errorf("saving preferences: %w", err)
	}

	pa.log.With(slog.Int64("user", userId)).Info("preferences analysis completed",
		slog.String("language", prefs.PreferredLanguage),
		slog.String("formality", prefs.Formality))

	return nil
}

func (pa *PreferencesAnalyzer) buildAnalysisPrompt(userMessages []string) string {
	messagesText := strings.Join(userMessages, "\n---\n")

	return fmt.Sprintf(`Analyze the following user messages and infer their communication preferences.

User Messages:
%s

Based on these messages, provide a JSON response with the following fields:
{
  "preferred_language": "the language the user writes in most (e.g., English, Ukrainian, Spanish)",
  "formality": "formal, informal, or neutral based on how they communicate",
  "verbosity": "verbose, concise, or balanced based on their message length and detail",
  "favorite_topics": ["list", "of", "topics", "they", "discuss", "frequently"],
  "technical_level": "beginner, intermediate, or expert based on technical vocabulary usage",
  "humor_preference": "none, occasional, or frequent based on humor in their messages",
  "response_length": "short, medium, or long based on the detail they seem to expect"
}

Respond ONLY with the JSON object, no other text.`, messagesText)
}

func (pa *PreferencesAnalyzer) callOpenAI(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	request := NewRequest(prompt, pa.conf.Model)
	jsonBytes, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(string(jsonBytes)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", pa.conf.OpenAIApiKey))
	req.Header.Set("Content-Type", "application/json")

	resp, err := pa.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			pa.log.Warn("closing body", slog.String("error", err.Error()))
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var chatCompletion ChatCompletion
	if err := json.Unmarshal(body, &chatCompletion); err != nil {
		return "", err
	}

	if chatCompletion.Error != nil && chatCompletion.Error.Code != "" {
		return "", fmt.Errorf("OpenAI error: %s", chatCompletion.Error.Message)
	}

	if len(chatCompletion.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}

	return chatCompletion.Choices[0].Message.Content, nil
}

func (pa *PreferencesAnalyzer) parseAnalysisResponse(userId int64, response string) (*storage.UserPreferences, error) {
	// Clean up response (remove markdown code blocks if present)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var analysis storage.PreferencesAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w (response: %s)", err, response)
	}

	// Get existing preferences to preserve metadata
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

	return prefs, nil
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
