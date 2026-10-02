package holder

import (
	"Brainy/lib/sl"
	"Brainy/storage"
	"log/slog"
)

// Message is an alias for storage.Message for backward compatibility
type Message = storage.Message

// DialogContext is an alias for storage.DialogContext for backward compatibility
type DialogContext = storage.DialogContext

type ContextManager struct {
	storage storage.ContextStorage
	log     *slog.Logger
}

func NewContextManager(store storage.ContextStorage, log *slog.Logger) *ContextManager {
	return &ContextManager{
		storage: store,
		log:     log,
	}
}

func (cm *ContextManager) GetUserContext(userId int64) *DialogContext {
	ctx, err := cm.storage.GetUserContext(userId)
	if err != nil {
		cm.log.With(slog.Int64("user", userId)).Error("getting user context", sl.Err(err))
		return nil
	}
	return ctx
}

func (cm *ContextManager) UpdateUserContext(userId int64, message Message) {
	if err := cm.storage.UpdateUserContext(userId, message); err != nil {
		cm.log.With(slog.Int64("user", userId)).Error("updating user context", sl.Err(err))
	}
}

func (cm *ContextManager) SetTokens(userId int64, total int) {
	if err := cm.storage.SetTokens(userId, total); err != nil {
		cm.log.With(slog.Int64("user", userId)).Error("setting tokens", sl.Err(err))
	}
}

func (cm *ContextManager) SetTopic(userId int64, topic string) {
	if err := cm.storage.SetTopic(userId, topic); err != nil {
		cm.log.With(slog.Int64("user", userId)).Error("setting topic", sl.Err(err))
	}
}

func (cm *ContextManager) ClearUserContext(userId int64) {
	if err := cm.storage.ClearUserContext(userId); err != nil {
		cm.log.With(slog.Int64("user", userId)).Error("clearing user context", sl.Err(err))
	}
}

func (cm *ContextManager) Close() error {
	return cm.storage.Close()
}
