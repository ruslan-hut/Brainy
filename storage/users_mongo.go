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

const usersCollectionName = "users"

type MongoUsersStorage struct {
	collection *mongo.Collection
	log        *slog.Logger
}

func NewMongoUsersStorage(client *mongo.Client, database string, log *slog.Logger) (*MongoUsersStorage, error) {
	collection := client.Database(database).Collection(usersCollectionName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Warn("creating users index", slog.String("error", err.Error()))
	}

	return &MongoUsersStorage{collection: collection, log: log}, nil
}

func (m *MongoUsersStorage) GetUser(userId int64) (*User, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var u User
	err := m.collection.FindOne(ctx, bson.M{"user_id": userId}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding user: %w", err)
	}
	return &u, nil
}

func (m *MongoUsersStorage) SaveUser(user *User) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if user.JoinedAt.IsZero() {
		user.JoinedAt = time.Now()
	}
	opts := options.Replace().SetUpsert(true)
	_, err := m.collection.ReplaceOne(ctx, bson.M{"user_id": user.UserId}, user, opts)
	return err
}

func (m *MongoUsersStorage) ListUsers() ([]*User, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cursor, err := m.collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer cursor.Close(ctx)
	var out []*User
	for cursor.Next(ctx) {
		var u User
		if err := cursor.Decode(&u); err != nil {
			continue
		}
		cc := u
		out = append(out, &cc)
	}
	return out, nil
}

func (m *MongoUsersStorage) Close() error { return nil }
