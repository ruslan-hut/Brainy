package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	collectionName = "dialog_contexts"
	maxTokensMongo = 20000
)

type MongoStorage struct {
	client     *mongo.Client
	collection *mongo.Collection
	log        *slog.Logger
}

func NewMongoStorage(uri, database string, log *slog.Logger) (*MongoStorage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connecting to MongoDB: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("pinging MongoDB: %w", err)
	}

	collection := client.Database(database).Collection(collectionName)

	// Create index on user_id for faster lookups
	_, err = collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Warn("creating index", slog.String("error", err.Error()))
	}

	return &MongoStorage{
		client:     client,
		collection: collection,
		log:        log,
	}, nil
}

func (m *MongoStorage) GetUserContext(userId int64) (*DialogContext, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var dialogCtx DialogContext
	err := m.collection.FindOne(ctx, bson.M{"user_id": userId}).Decode(&dialogCtx)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding context: %w", err)
	}
	return &dialogCtx, nil
}

func (m *MongoStorage) UpdateUserContext(userId int64, message Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	message.Tokens = len([]rune(message.Text))
	message.Timestamp = time.Now()

	existing, err := m.GetUserContext(userId)
	if err != nil {
		return err
	}

	if existing == nil {
		newCtx := &DialogContext{
			UserId:    userId,
			Messages:  []Message{message},
			Tokens:    message.Tokens,
			UpdatedAt: time.Now(),
		}
		_, err = m.collection.InsertOne(ctx, newCtx)
		return err
	}

	existing.Tokens += message.Tokens

	for existing.Tokens > maxTokensMongo && len(existing.Messages) > 0 {
		tokensToRemove := existing.Messages[0].Tokens
		existing.Messages = existing.Messages[1:]
		existing.Tokens -= tokensToRemove
	}

	existing.Messages = append(existing.Messages, message)
	existing.UpdatedAt = time.Now()

	_, err = m.collection.ReplaceOne(ctx, bson.M{"user_id": userId}, existing)
	return err
}

func (m *MongoStorage) SetTopic(userId int64, topic string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"topic":      topic,
			"updated_at": time.Now(),
		},
		"$setOnInsert": bson.M{
			"user_id":  userId,
			"messages": []Message{},
			"tokens":   0,
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := m.collection.UpdateOne(ctx, bson.M{"user_id": userId}, update, opts)
	return err
}

func (m *MongoStorage) ClearUserContext(userId int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := m.collection.DeleteOne(ctx, bson.M{"user_id": userId})
	return err
}

func (m *MongoStorage) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Disconnect(ctx)
}

// GetClient returns the MongoDB client for sharing with other storages
func (m *MongoStorage) GetClient() *mongo.Client {
	return m.client
}

// GetDatabase returns the database name
func (m *MongoStorage) GetDatabase() string {
	return m.collection.Database().Name()
}
