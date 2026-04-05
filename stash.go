package parchment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	defaultStashTTL   = 10 * time.Minute
	defaultStashLimit = 50
)

// StashedArtifact holds a partial artifact that failed validation.
type StashedArtifact struct {
	Input     CreateInput
	CreatedAt time.Time
}

// StashStore is an in-memory store for partial artifacts that failed validation.
type StashStore struct {
	mu      sync.Mutex
	stashes map[string]*StashedArtifact
	ttl     time.Duration
	limit   int
}

// NewStashStore creates a stash store with the given TTL and limit.
func NewStashStore(ttl time.Duration, limit int) *StashStore {
	if ttl <= 0 {
		ttl = defaultStashTTL
	}
	if limit <= 0 {
		limit = defaultStashLimit
	}
	return &StashStore{
		stashes: make(map[string]*StashedArtifact),
		ttl:     ttl,
		limit:   limit,
	}
}

// Put stores a partial artifact and returns the stash ID.
func (s *StashStore) Put(in CreateInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Expire old entries first
	s.expireLocked()

	if len(s.stashes) >= s.limit {
		return "", fmt.Errorf("stash limit exceeded (%d)", s.limit)
	}

	var buf [16]byte
	rand.Read(buf[:])
	id := hex.EncodeToString(buf[:])
	s.stashes[id] = &StashedArtifact{
		Input:     in,
		CreatedAt: time.Now(),
	}
	return id, nil
}

// Get retrieves a stashed artifact. Returns error if not found or expired.
func (s *StashStore) Get(id string) (*StashedArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stash, ok := s.stashes[id]
	if !ok {
		return nil, fmt.Errorf("stash not found")
	}
	if time.Since(stash.CreatedAt) > s.ttl {
		delete(s.stashes, id)
		return nil, fmt.Errorf("stash expired")
	}
	return stash, nil
}

// Delete removes a stash entry.
func (s *StashStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stashes, id)
}

// Len returns the number of active stashes.
func (s *StashStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stashes)
}

// expireLocked removes expired entries. Must be called with mu held.
func (s *StashStore) expireLocked() {
	now := time.Now()
	for id, stash := range s.stashes {
		if now.Sub(stash.CreatedAt) > s.ttl {
			delete(s.stashes, id)
		}
	}
}

// MergeInput applies patch fields onto a stashed CreateInput.
// Non-empty patch values override, sections are appended (deduped by name).
func MergeInput(base CreateInput, patch CreateInput) CreateInput {
	if patch.Title != "" {
		base.Title = patch.Title
	}
	if patch.Goal != "" {
		base.Goal = patch.Goal
	}
	if patch.Scope != "" {
		base.Scope = patch.Scope
	}
	if patch.Priority != "" {
		base.Priority = patch.Priority
	}
	if patch.Status != "" {
		base.Status = patch.Status
	}
	if patch.Parent != "" {
		base.Parent = patch.Parent
	}
	if patch.Kind != "" {
		base.Kind = patch.Kind
	}
	if len(patch.Labels) > 0 {
		base.Labels = patch.Labels
	}
	if len(patch.DependsOn) > 0 {
		base.DependsOn = patch.DependsOn
	}
	if len(patch.Links) > 0 {
		if base.Links == nil {
			base.Links = make(map[string][]string)
		}
		for k, v := range patch.Links {
			base.Links[k] = v
		}
	}

	// Merge sections: patch sections override by name, new ones appended
	if len(patch.Sections) > 0 {
		existing := make(map[string]int, len(base.Sections))
		for i, s := range base.Sections {
			existing[s.Name] = i
		}
		for _, s := range patch.Sections {
			if idx, ok := existing[s.Name]; ok {
				base.Sections[idx] = s
			} else {
				base.Sections = append(base.Sections, s)
			}
		}
	}

	return base
}
