package storage

import (
	"log"
	"sync"
	"time"
)

const maxTokens = 20000

type MemoryStorage struct {
	contexts map[int64]*DialogContext
	mutex    sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		contexts: make(map[int64]*DialogContext),
	}
}

func (m *MemoryStorage) GetUserContext(userId int64) (*DialogContext, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.contexts[userId], nil
}

func (m *MemoryStorage) UpdateUserContext(userId int64, message Message) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	message.Tokens = len([]rune(message.Text))
	message.Timestamp = time.Now()

	if context, ok := m.contexts[userId]; ok {
		context.Tokens += message.Tokens

		// Remove old messages if over token limit
		for context.Tokens > maxTokens && len(context.Messages) > 0 {
			log.Printf("MemoryStorage: removing message from context of user %d", userId)
			tokensToRemove := context.Messages[0].Tokens
			context.Messages = context.Messages[1:]
			context.Tokens -= tokensToRemove
		}

		context.Messages = append(context.Messages, message)
		context.UpdatedAt = time.Now()
	} else {
		m.contexts[userId] = &DialogContext{
			UserId:    userId,
			Messages:  []Message{message},
			Tokens:    message.Tokens,
			UpdatedAt: time.Now(),
		}
	}
	return nil
}

func (m *MemoryStorage) SetTopic(userId int64, topic string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if context, ok := m.contexts[userId]; ok {
		context.Topic = topic
	} else {
		m.contexts[userId] = &DialogContext{
			UserId:    userId,
			Topic:     topic,
			Messages:  []Message{},
			Tokens:    0,
			UpdatedAt: time.Now(),
		}
	}
	return nil
}

func (m *MemoryStorage) ClearUserContext(userId int64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.contexts, userId)
	return nil
}

func (m *MemoryStorage) Close() error {
	return nil
}
