package storage

import "time"

// UserPreferences stores analyzed user communication preferences
type UserPreferences struct {
	UserId            int64     `bson:"user_id"`
	PreferredLanguage string    `bson:"preferred_language"` // e.g., "English", "Ukrainian", "Spanish"
	Formality         string    `bson:"formality"`          // "formal", "informal", "neutral"
	Verbosity         string    `bson:"verbosity"`          // "verbose", "concise", "balanced"
	FavoriteTopics    []string  `bson:"favorite_topics"`    // e.g., ["technology", "science"]
	TechnicalLevel    string    `bson:"technical_level"`    // "beginner", "intermediate", "expert"
	HumorPreference   string    `bson:"humor_preference"`   // "none", "occasional", "frequent"
	ResponseLength    string    `bson:"response_length"`    // "short", "medium", "long"
	LastAnalysisAt    time.Time `bson:"last_analysis_at"`
	LastMessageAt     time.Time `bson:"last_message_at"`
	CreatedAt         time.Time `bson:"created_at"`
	UpdatedAt         time.Time `bson:"updated_at"`
}

// PreferencesAnalysis is used for parsing AI analysis response
type PreferencesAnalysis struct {
	PreferredLanguage string   `json:"preferred_language"`
	Formality         string   `json:"formality"`
	Verbosity         string   `json:"verbosity"`
	FavoriteTopics    []string `json:"favorite_topics"`
	TechnicalLevel    string   `json:"technical_level"`
	HumorPreference   string   `json:"humor_preference"`
	ResponseLength    string   `json:"response_length"`
}

// PreferencesStorage defines the interface for user preferences persistence
type PreferencesStorage interface {
	// GetUserPreferences retrieves preferences for a user (returns nil if none exist)
	GetUserPreferences(userId int64) (*UserPreferences, error)
	// SaveUserPreferences creates or updates user preferences
	SaveUserPreferences(prefs *UserPreferences) error
	// UpdateLastMessageTime updates the LastMessageAt timestamp when user sends a message
	UpdateLastMessageTime(userId int64) error
	// GetUsersNeedingAnalysis returns user IDs where LastMessageAt > LastAnalysisAt
	// AND time.Since(LastAnalysisAt) > cutoffDuration
	GetUsersNeedingAnalysis(cutoffDuration time.Duration) ([]int64, error)
	// Close closes the storage connection
	Close() error
}
