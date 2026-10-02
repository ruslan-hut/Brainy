package storage

import (
	"context"
	"sync"
	"time"
)

type MemoryUsersStorage struct {
	users map[int64]*User
	mu    sync.RWMutex
}

func NewMemoryUsersStorage() *MemoryUsersStorage {
	return &MemoryUsersStorage{users: make(map[int64]*User)}
}

func (m *MemoryUsersStorage) GetUser(ctx context.Context, userId int64) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if u, ok := m.users[userId]; ok {
		cc := *u
		return &cc, nil
	}
	return nil, nil
}

func (m *MemoryUsersStorage) SaveUser(ctx context.Context, user *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user.JoinedAt.IsZero() {
		user.JoinedAt = time.Now()
	}
	cc := *user
	m.users[user.UserId] = &cc
	return nil
}

func (m *MemoryUsersStorage) ListUsers(ctx context.Context) ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cc := *u
		out = append(out, &cc)
	}
	return out, nil
}

func (m *MemoryUsersStorage) Close() error { return nil }
