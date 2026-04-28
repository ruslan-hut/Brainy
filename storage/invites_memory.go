package storage

import (
	"sort"
	"sync"
	"time"
)

type MemoryInvitesStorage struct {
	invites map[string]*InviteCode
	mu      sync.RWMutex
}

func NewMemoryInvitesStorage() *MemoryInvitesStorage {
	return &MemoryInvitesStorage{invites: make(map[string]*InviteCode)}
}

func (m *MemoryInvitesStorage) GetInvite(code string) (*InviteCode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if i, ok := m.invites[code]; ok {
		cc := *i
		return &cc, nil
	}
	return nil, nil
}

func (m *MemoryInvitesStorage) SaveInvite(invite *InviteCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if invite.CreatedAt.IsZero() {
		invite.CreatedAt = time.Now()
	}
	cc := *invite
	m.invites[invite.Code] = &cc
	return nil
}

func (m *MemoryInvitesStorage) RedeemInvite(code string, userId int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.invites[code]
	if !ok || i.UsedBy != 0 {
		return false, nil
	}
	i.UsedBy = userId
	i.UsedAt = time.Now()
	return true, nil
}

func (m *MemoryInvitesStorage) ListInvites(limit int) ([]*InviteCode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*InviteCode, 0, len(m.invites))
	for _, i := range m.invites {
		cc := *i
		out = append(out, &cc)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryInvitesStorage) Close() error { return nil }
