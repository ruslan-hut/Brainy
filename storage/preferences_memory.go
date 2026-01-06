package storage

import (
	"sync"
	"time"
)

// MemoryPreferencesStorage is an in-memory implementation of PreferencesStorage
type MemoryPreferencesStorage struct {
	preferences map[int64]*UserPreferences
	mutex       sync.RWMutex
}

// NewMemoryPreferencesStorage creates a new in-memory preferences storage
func NewMemoryPreferencesStorage() *MemoryPreferencesStorage {
	return &MemoryPreferencesStorage{
		preferences: make(map[int64]*UserPreferences),
	}
}

// GetUserPreferences retrieves preferences for a user
func (m *MemoryPreferencesStorage) GetUserPreferences(userId int64) (*UserPreferences, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	if prefs, ok := m.preferences[userId]; ok {
		// Return a copy to prevent external mutation
		cc := *prefs
		if prefs.FavoriteTopics != nil {
			cc.FavoriteTopics = make([]string, len(prefs.FavoriteTopics))
			for i, t := range prefs.FavoriteTopics {
				cc.FavoriteTopics[i] = t
			}
		}
		return &cc, nil
	}
	return nil, nil
}

// SaveUserPreferences creates or updates user preferences
func (m *MemoryPreferencesStorage) SaveUserPreferences(prefs *UserPreferences) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()
	prefs.UpdatedAt = now

	if existing, ok := m.preferences[prefs.UserId]; ok {
		prefs.CreatedAt = existing.CreatedAt
		// Preserve LastMessageAt if not set in new prefs
		if prefs.LastMessageAt.IsZero() {
			prefs.LastMessageAt = existing.LastMessageAt
		}
	} else {
		prefs.CreatedAt = now
	}

	// Store a copy
	cc := *prefs
	if prefs.FavoriteTopics != nil {
		cc.FavoriteTopics = make([]string, len(prefs.FavoriteTopics))
		for i, t := range prefs.FavoriteTopics {
			cc.FavoriteTopics[i] = t
		}
	}
	m.preferences[prefs.UserId] = &cc
	return nil
}

// UpdateLastMessageTime updates the LastMessageAt timestamp
func (m *MemoryPreferencesStorage) UpdateLastMessageTime(userId int64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()
	if prefs, ok := m.preferences[userId]; ok {
		prefs.LastMessageAt = now
		prefs.UpdatedAt = now
	} else {
		m.preferences[userId] = &UserPreferences{
			UserId:        userId,
			LastMessageAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	}
	return nil
}

// GetUsersNeedingAnalysis returns users who need preference analysis
func (m *MemoryPreferencesStorage) GetUsersNeedingAnalysis(cutoffDuration time.Duration) ([]int64, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var users []int64
	now := time.Now()
	cutoffTime := now.Add(-cutoffDuration)

	for userId, prefs := range m.preferences {
		// User has messages after last analysis AND last analysis is older than cutoff
		hasNewMessages := prefs.LastMessageAt.After(prefs.LastAnalysisAt)
		needsAnalysis := prefs.LastAnalysisAt.Before(cutoffTime) || prefs.LastAnalysisAt.IsZero()

		if hasNewMessages && needsAnalysis {
			users = append(users, userId)
		}
	}
	return users, nil
}

// Close closes the storage (no-op for memory)
func (m *MemoryPreferencesStorage) Close() error {
	return nil
}
