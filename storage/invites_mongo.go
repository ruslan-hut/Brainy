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

const invitesCollectionName = "invite_codes"

type MongoInvitesStorage struct {
	collection *mongo.Collection
	log        *slog.Logger
}

func NewMongoInvitesStorage(client *mongo.Client, database string, log *slog.Logger) (*MongoInvitesStorage, error) {
	collection := client.Database(database).Collection(invitesCollectionName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "code", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		log.Warn("creating invites index", slog.String("error", err.Error()))
	}

	return &MongoInvitesStorage{collection: collection, log: log}, nil
}

func (m *MongoInvitesStorage) GetInvite(code string) (*InviteCode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var i InviteCode
	err := m.collection.FindOne(ctx, bson.M{"code": code}).Decode(&i)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding invite: %w", err)
	}
	return &i, nil
}

func (m *MongoInvitesStorage) SaveInvite(invite *InviteCode) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if invite.CreatedAt.IsZero() {
		invite.CreatedAt = time.Now()
	}
	_, err := m.collection.InsertOne(ctx, invite)
	return err
}

// RedeemInvite atomically claims an unused invite for userId.
func (m *MongoInvitesStorage) RedeemInvite(code string, userId int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := m.collection.UpdateOne(ctx,
		bson.M{"code": code, "used_by": int64(0)},
		bson.M{"$set": bson.M{"used_by": userId, "used_at": time.Now()}},
	)
	if err != nil {
		return false, fmt.Errorf("redeeming invite: %w", err)
	}
	return res.ModifiedCount == 1, nil
}

func (m *MongoInvitesStorage) ListInvites(limit int) ([]*InviteCode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if limit > 0 {
		opts = opts.SetLimit(int64(limit))
	}
	cursor, err := m.collection.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("listing invites: %w", err)
	}
	defer cursor.Close(ctx)
	var out []*InviteCode
	for cursor.Next(ctx) {
		var i InviteCode
		if err := cursor.Decode(&i); err != nil {
			continue
		}
		cc := i
		out = append(out, &cc)
	}
	return out, nil
}

func (m *MongoInvitesStorage) Close() error { return nil }
