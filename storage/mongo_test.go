package storage

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"
)

// newTestMongo connects to the MongoDB at MONGO_TEST_URI using a fresh
// database per test. Skipped with -short or when the variable is unset.
func newTestMongo(t *testing.T) *MongoStorage {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := fmt.Sprintf("brainy_test_%d", time.Now().UnixNano())
	m, err := NewMongoStorage(t.Context(), uri, db, log)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() {
		_ = m.client.Database(db).Drop(t.Context())
		_ = m.Close()
	})
	return m
}

func TestMongoContext(t *testing.T) {
	m := newTestMongo(t)
	const user = int64(42)

	if err := m.SetTopic(t.Context(), user, "space"); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	if err := m.UpdateUserContext(t.Context(), user, Message{IsUser: true, Text: "hi", Tokens: 5}); err != nil {
		t.Fatalf("update: %v", err)
	}
	// Exceeds the limit, so the first message must be trimmed.
	if err := m.UpdateUserContext(t.Context(), user, Message{Text: "long", Tokens: maxTokensMongo}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := m.GetUserContext(t.Context(), user)
	if err != nil || got == nil {
		t.Fatalf("get: %v, %v", got, err)
	}
	if got.Topic != "space" || len(got.Messages) != 1 || got.Messages[0].Text != "long" || got.Tokens != maxTokensMongo {
		t.Errorf("unexpected context: %+v", got)
	}

	if err := m.ClearUserContext(t.Context(), user); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, err := m.GetUserContext(t.Context(), user); err != nil || got != nil {
		t.Errorf("after clear: %v, %v", got, err)
	}
}

func TestMongoPreferences(t *testing.T) {
	m := newTestMongo(t)
	p, err := NewMongoPreferencesStorage(t.Context(), m.GetClient(), m.GetDatabase(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tests := []struct {
		name     string
		user     int64
		analyzed time.Time
		want     bool
	}{
		{"never analyzed", 1, time.Time{}, true},
		{"analyzed long ago", 2, time.Now().Add(-48 * time.Hour), true},
		{"analyzed recently", 3, time.Now().Add(-time.Hour), false},
	}
	for _, tt := range tests {
		if !tt.analyzed.IsZero() {
			if err := p.SaveUserPreferences(t.Context(), &UserPreferences{UserId: tt.user, LastAnalysisAt: tt.analyzed, FavoriteTopics: []string{"go"}}); err != nil {
				t.Fatalf("%s: save: %v", tt.name, err)
			}
		}
		if err := p.UpdateLastMessageTime(t.Context(), tt.user); err != nil {
			t.Fatalf("%s: touch: %v", tt.name, err)
		}
	}

	users, err := p.GetUsersNeedingAnalysis(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatalf("needing analysis: %v", err)
	}
	for _, tt := range tests {
		if got := slices.Contains(users, tt.user); got != tt.want {
			t.Errorf("%s: needs analysis = %v, want %v", tt.name, got, tt.want)
		}
	}

	got, err := p.GetUserPreferences(t.Context(), 2)
	if err != nil || got == nil || len(got.FavoriteTopics) != 1 || got.LastMessageAt.IsZero() {
		t.Errorf("get: %+v, %v", got, err)
	}
}

func TestMongoUsersAndInvites(t *testing.T) {
	m := newTestMongo(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	u, err := NewMongoUsersStorage(t.Context(), m.GetClient(), m.GetDatabase(), log)
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	inv, err := NewMongoInvitesStorage(t.Context(), m.GetClient(), m.GetDatabase(), log)
	if err != nil {
		t.Fatalf("invites: %v", err)
	}

	if err := u.SaveUser(t.Context(), &User{UserId: 7, Role: RoleUser, JoinedAt: time.Now()}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if got, err := u.GetUser(t.Context(), 7); err != nil || got == nil || got.Role != RoleUser {
		t.Errorf("get user: %+v, %v", got, err)
	}
	if list, err := u.ListUsers(t.Context()); err != nil || len(list) != 1 {
		t.Errorf("list users: %d, %v", len(list), err)
	}

	if err := inv.SaveInvite(t.Context(), &InviteCode{Code: "abc", CreatedBy: 1, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("save invite: %v", err)
	}
	if ok, err := inv.RedeemInvite(t.Context(), "abc", 7); err != nil || !ok {
		t.Errorf("first redeem: %v, %v", ok, err)
	}
	if ok, err := inv.RedeemInvite(t.Context(), "abc", 8); err != nil || ok {
		t.Errorf("second redeem: %v, %v", ok, err)
	}
	if got, err := inv.GetInvite(t.Context(), "abc"); err != nil || got == nil || got.UsedBy != 7 {
		t.Errorf("get invite: %+v, %v", got, err)
	}
	if list, err := inv.ListInvites(t.Context(), 10); err != nil || len(list) != 1 {
		t.Errorf("list invites: %d, %v", len(list), err)
	}
}
