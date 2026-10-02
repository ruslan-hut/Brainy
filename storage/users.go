package storage

import (
	"context"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User represents a registered bot user.
type User struct {
	UserId     int64     `bson:"user_id"`
	Username   string    `bson:"username"`
	Role       string    `bson:"role"`
	InviteCode string    `bson:"invite_code"`
	JoinedAt   time.Time `bson:"joined_at"`
}

// UsersStorage persists registered users and their roles.
type UsersStorage interface {
	GetUser(ctx context.Context, userId int64) (*User, error)
	SaveUser(ctx context.Context, user *User) error
	ListUsers(ctx context.Context) ([]*User, error)
	Close() error
}
