package storage

import "time"

// InviteCode is a one-time-use code that grants service access.
type InviteCode struct {
	Code      string    `bson:"code"`
	CreatedBy int64     `bson:"created_by"`
	CreatedAt time.Time `bson:"created_at"`
	UsedBy    int64     `bson:"used_by"` // 0 if unused
	UsedAt    time.Time `bson:"used_at"`
}

// Used reports whether the code has already been redeemed.
func (i *InviteCode) Used() bool { return i.UsedBy != 0 }

// InvitesStorage persists invite codes.
type InvitesStorage interface {
	GetInvite(code string) (*InviteCode, error)
	SaveInvite(invite *InviteCode) error
	// RedeemInvite atomically marks code as used by userId.
	// Returns (false, nil) if code does not exist or is already used.
	RedeemInvite(code string, userId int64) (bool, error)
	ListInvites(limit int) ([]*InviteCode, error)
	Close() error
}
