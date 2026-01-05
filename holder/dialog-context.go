package holder

import (
	"Brainy/storage"
	"log"
)

// Message is an alias for storage.Message for backward compatibility
type Message = storage.Message

// DialogContext is an alias for storage.DialogContext for backward compatibility
type DialogContext = storage.DialogContext

type ContextManager struct {
	storage storage.ContextStorage
}

func NewContextManager(store storage.ContextStorage) *ContextManager {
	return &ContextManager{
		storage: store,
	}
}

func (cm *ContextManager) GetUserContext(userId int64) *DialogContext {
	ctx, err := cm.storage.GetUserContext(userId)
	if err != nil {
		log.Printf("error getting user context: %v", err)
		return nil
	}
	return ctx
}

func (cm *ContextManager) UpdateUserContext(userId int64, message Message) {
	if err := cm.storage.UpdateUserContext(userId, message); err != nil {
		log.Printf("error updating user context: %v", err)
	}
}

func (cm *ContextManager) SetTopic(userId int64, topic string) {
	if err := cm.storage.SetTopic(userId, topic); err != nil {
		log.Printf("error setting topic: %v", err)
	}
}

func (cm *ContextManager) ClearUserContext(userId int64) {
	if err := cm.storage.ClearUserContext(userId); err != nil {
		log.Printf("error clearing user context: %v", err)
	}
}

func (cm *ContextManager) Close() error {
	return cm.storage.Close()
}
