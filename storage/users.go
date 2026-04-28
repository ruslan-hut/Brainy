package storage

import "time"

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
	GetUser(userId int64) (*User, error)
	SaveUser(user *User) error
	ListUsers() ([]*User, error)
	Close() error
}
