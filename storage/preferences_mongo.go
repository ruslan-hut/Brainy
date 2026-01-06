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

const preferencesCollectionName = "user_preferences"

// MongoPreferencesStorage is a MongoDB implementation of PreferencesStorage
type MongoPreferencesStorage struct {
	client     *mongo.Client
	collection *mongo.Collection
	log        *slog.Logger
}

// NewMongoPreferencesStorage creates a new MongoDB preferences storage
func NewMongoPreferencesStorage(client *mongo.Client, database string, log *slog.Logger) (*MongoPreferencesStorage, error) {
	collection := client.Database(database).Collection(preferencesCollectionName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create unique index on user_id
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Warn("creating preferences index", slog.String("error", err.Error()))
	}

	return &MongoPreferencesStorage{
		client:     client,
		collection: collection,
		log:        log,
	}, nil
}

// GetUserPreferences retrieves preferences for a user
func (m *MongoPreferencesStorage) GetUserPreferences(userId int64) (*UserPreferences, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var prefs UserPreferences
	err := m.collection.FindOne(ctx, bson.M{"user_id": userId}).Decode(&prefs)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding preferences: %w", err)
	}
	return &prefs, nil
}

// SaveUserPreferences creates or updates user preferences
func (m *MongoPreferencesStorage) SaveUserPreferences(prefs *UserPreferences) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prefs.UpdatedAt = time.Now()
	if prefs.CreatedAt.IsZero() {
		prefs.CreatedAt = time.Now()
	}

	opts := options.Replace().SetUpsert(true)
	_, err := m.collection.ReplaceOne(ctx, bson.M{"user_id": prefs.UserId}, prefs, opts)
	return err
}

// UpdateLastMessageTime updates the LastMessageAt timestamp
func (m *MongoPreferencesStorage) UpdateLastMessageTime(userId int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"last_message_at": now,
			"updated_at":      now,
		},
		"$setOnInsert": bson.M{
			"user_id":    userId,
			"created_at": now,
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := m.collection.UpdateOne(ctx, bson.M{"user_id": userId}, update, opts)
	return err
}

// GetUsersNeedingAnalysis returns users who need preference analysis
func (m *MongoPreferencesStorage) GetUsersNeedingAnalysis(cutoffDuration time.Duration) ([]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cutoffTime := time.Now().Add(-cutoffDuration)

	// Find users where:
	// 1. last_message_at > last_analysis_at (has new messages)
	// 2. last_analysis_at < cutoff OR last_analysis_at doesn't exist (needs analysis)
	filter := bson.M{
		"$expr": bson.M{
			"$gt": []string{"$last_message_at", "$last_analysis_at"},
		},
		"$or": []bson.M{
			{"last_analysis_at": bson.M{"$lt": cutoffTime}},
			{"last_analysis_at": bson.M{"$exists": false}},
		},
	}

	cursor, err := m.collection.Find(ctx, filter, options.Find().SetProjection(bson.M{"user_id": 1}))
	if err != nil {
		return nil, fmt.Errorf("finding users for analysis: %w", err)
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		err := cursor.Close(ctx)
		if err != nil {
			m.log.Warn("closing cursor", slog.String("error", err.Error()))
		}
	}(cursor, ctx)

	var users []int64
	for cursor.Next(ctx) {
		var doc struct {
			UserId int64 `bson:"user_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		users = append(users, doc.UserId)
	}
	return users, nil
}

// Close closes the storage (client is shared, don't disconnect here)
func (m *MongoPreferencesStorage) Close() error {
	return nil
}
