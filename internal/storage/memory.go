package storage

import (
	"fmt"
	"sync"
	"time"

	"github.com/sentinel/sentinel-go/pkg/types"
)

// InMemoryStore implements MemoryStore interface
type InMemoryStore struct {
	data    map[string]interface{}
	history []types.HistoryEntry
	mu      sync.RWMutex
}

// NewInMemoryStore creates a new in-memory storage instance
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		data:    make(map[string]interface{}),
		history: []types.HistoryEntry{},
	}
}

// Store saves a key-value pair
func (s *InMemoryStore) Store(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

// Retrieve gets a value by key
func (s *InMemoryStore) Retrieve(key string) (interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	value, exists := s.data[key]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return value, nil
}

// Delete removes a key from storage
func (s *InMemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// GetHistory returns the action history
func (s *InMemoryStore) GetHistory() []types.HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.history
}

// AddHistoryEntry adds an entry to the history
func (s *InMemoryStore) AddHistoryEntry(action, input, output, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	entry := types.HistoryEntry{
		Timestamp: time.Now(),
		Action:    action,
		Input:     input,
		Output:    output,
		Status:    status,
	}
	s.history = append(s.history, entry)
}
