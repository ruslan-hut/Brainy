package storage

import "time"

type Message struct {
	IsUser    bool      `bson:"is_user"`
	Text      string    `bson:"text"`
	Tokens    int       `bson:"tokens"`
	Timestamp time.Time `bson:"timestamp"`
}

type DialogContext struct {
	UserId    int64     `bson:"user_id"`
	Topic     string    `bson:"topic"`
	Messages  []Message `bson:"messages"`
	Tokens    int       `bson:"tokens"`
	UpdatedAt time.Time `bson:"updated_at"`
}

type ContextStorage interface {
	GetUserContext(userId int64) (*DialogContext, error)
	UpdateUserContext(userId int64, message Message) error
	// SetTokens overwrites the conversation's cumulative token count.
	// Used to reconcile estimated counts with the model's reported usage.
	SetTokens(userId int64, total int) error
	SetTopic(userId int64, topic string) error
	ClearUserContext(userId int64) error
	Close() error
}
