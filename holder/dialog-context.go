package holder

import (
	"log"
	"sync"
	"time"
)

const maxTokens = 20000

type Message struct {
	IsUser    bool
	Text      string
	Tokens    int
	Timestamp time.Time
}

type DialogContext struct {
	UserId    int64
	Topic     string
	Messages  []Message
	Tokens    int
	UpdatedAt time.Time
}

type ContextManager struct {
	Contexts map[int64]*DialogContext
	mutex    sync.RWMutex
}

func NewContextManager() *ContextManager {
	return &ContextManager{
		Contexts: make(map[int64]*DialogContext),
		mutex:    sync.RWMutex{},
	}
}

func (cm *ContextManager) GetUserContext(userId int64) *DialogContext {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.Contexts[userId]
}

func (cm *ContextManager) UpdateUserContext(userId int64, message Message) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	// Calculate the number of tokens in the new message
	message.Tokens = len([]rune(message.Text))

	if context, ok := cm.Contexts[userId]; ok {

		// Add the tokens from the new message to the total
		context.Tokens += message.Tokens

		// If the total tokens with the new message exceed the maximum,
		// remove messages from the beginning until it is within the limit.
		for context.Tokens > maxTokens {
			log.Printf("ContextManager: removing message from context of user %d", userId)
			context.Messages = context.Messages[1:]
			context.Tokens -= context.Messages[0].Tokens
		}

		context.Messages = append(context.Messages, message)
		context.UpdatedAt = time.Now()

	} else {
		cm.Contexts[userId] = &DialogContext{
			UserId:    userId,
			Messages:  []Message{message},
			Tokens:    message.Tokens,
			UpdatedAt: time.Now(),
		}
	}
}

func (cm *ContextManager) SetTopic(userId int64, topic string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if context, ok := cm.Contexts[userId]; ok {
		context.Topic = topic
	} else {
		cm.Contexts[userId] = &DialogContext{
			UserId:    userId,
			Topic:     topic,
			Messages:  []Message{},
			Tokens:    0,
			UpdatedAt: time.Now(),
		}
	}
}

func (cm *ContextManager) ClearUserContext(userId int64) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	delete(cm.Contexts, userId)
}
