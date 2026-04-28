package main

// MIGRATION (one-shot): register every user who already has a user_preferences
// document as an approved user (role=user). Safe to run repeatedly — skips
// anyone already in the users collection. Remove this file after the first
// successful deploy.

import (
	"Brainy/lib/sl"
	"Brainy/storage"
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func migrateApprovedUsers(client *mongo.Client, database string, users storage.UsersStorage, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cursor, err := client.Database(database).Collection("user_preferences").
		Find(ctx, bson.M{}, nil)
	if err != nil {
		log.Error("migration: listing preferences", sl.Err(err))
		return
	}
	defer cursor.Close(ctx)

	var migrated, skipped int
	for cursor.Next(ctx) {
		var doc struct {
			UserId int64 `bson:"user_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		if doc.UserId == 0 {
			continue
		}
		existing, _ := users.GetUser(doc.UserId)
		if existing != nil {
			skipped++
			continue
		}
		err := users.SaveUser(&storage.User{
			UserId:   doc.UserId,
			Role:     storage.RoleUser,
			JoinedAt: time.Now(),
		})
		if err != nil {
			log.With(slog.Int64("user", doc.UserId)).Warn("migration: saving user", sl.Err(err))
			continue
		}
		migrated++
	}
	log.Info("migration: approved-users complete",
		slog.Int("migrated", migrated),
		slog.Int("already_present", skipped))
}
